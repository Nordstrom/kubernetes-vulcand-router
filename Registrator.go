package main

import (
	"github.com/gorilla/websocket"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"log"
	"encoding/json"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"flag"
	"github.com/coreos/go-etcd/etcd"
)

var kubernetesEndpoint string
var etcdAddress string

type Registration struct {
	URL string
}

func init() {
	flag.StringVar(&kubernetesEndpoint, "pods", "", "Endpoint of Kubernetes pods API")
	flag.StringVar(&etcdAddress, "etcd", "", "etcd address")

	flag.Parse()
}

func main() {
	listenForPods()
}

func listenForPods() {

	wsConn := openConnection()

	var wsErrors chan string = make(chan string)

	go listen(wsConn, wsErrors)
	go reconnector(wsErrors)

	var input string
	fmt.Scanln(&input)

}

func openConnection() *websocket.Conn {
	u, err := url.Parse(kubernetesEndpoint)
	if err != nil {
		log.Fatal(err)
	}


	rawConn, err := net.Dial("tcp", u.Host)
	if err != nil {
		log.Fatal(err)
	}

	wsHeaders := http.Header{
		"Origin":                   {kubernetesEndpoint},
		"Sec-WebSocket-Extensions": {"permessage-deflate; client_max_window_bits, x-webkit-deflate-frame"},
	}

	wsConn, resp, err := websocket.NewClient(rawConn, u, wsHeaders, 1024, 1024)
	if err != nil {
		log.Fatal(fmt.Errorf("websocket.NewClient Error: %s\nResp:%+v", err, resp))

	}

	return wsConn
}

func reconnector(wsErrors chan string) {
	for {
		_ = <- wsErrors
		log.Println("Reconnecting...")
		go listen(openConnection(), wsErrors)
	}
}

func listen(wsConn *websocket.Conn, wsErrors chan string) {
	log.Println("Listening for pods")

	for {
		_, r, err := wsConn.NextReader()

		if err != nil {
			log.Printf("Error getting reader: %v",err)
			wsErrors <- "Error"
			return
		}


		dec := json.NewDecoder(r)
		var objmap map[string]*json.RawMessage
		dec.Decode(&objmap)

		var actionType string
		json.Unmarshal(*objmap["type"], &actionType)

		var pod api.Pod
		err = json.Unmarshal(*objmap["object"], &pod)

		switch actionType {
		case "MODIFIED":
			registrate(pod)
		case "DELETED":
			deletePod(pod)
		}
	}
}

func registrate(pod api.Pod) {
	if(pod.Status.Phase != "Running") {
		return
	}


	fmt.Printf("Registrating pod %v listening on %v to %v\n", pod.Name, pod.Status.PodIP, etcdAddress)

	machines := []string{etcdAddress}
	client := etcd.NewClient(machines)

	podUrl := fmt.Sprintf("http://%v:%v", pod.Status.PodIP, pod.Spec.Containers[0].Ports[0].HostPort)

	if _, err := client.Set("vulcan/backends/" + pod.Labels["name"] + "/servers/" + pod.Status.PodIP, `{"URL": "` + podUrl + `"}`, 0); err != nil {
		log.Fatal(err)
	}
}

func deletePod(pod api.Pod) {
	fmt.Printf("Deleting pod %v from %v\n", pod.Name, etcdAddress)

	machines := []string{etcdAddress}
	client := etcd.NewClient(machines)

	_, err := client.Delete("vulcan/backends/" + pod.Labels["name"] + "/servers/" + pod.Status.PodIP, false);

	if err != nil {
		log.Printf("Failed to delete backend '%v'", pod.Labels["name"]);
	}
}
