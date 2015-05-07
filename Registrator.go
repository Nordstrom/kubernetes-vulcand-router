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

	listenForPods(kubernetesEndpoint)
}

func listenForPods(endpoint string) {
	u, err := url.Parse(endpoint)
	if err != nil {
		log.Println(err)
		return
	}

	rawConn, err := net.Dial("tcp", u.Host)
	if err != nil {
		log.Println(err)
		return
	}

	wsHeaders := http.Header{
		"Origin":                   {endpoint},
		"Sec-WebSocket-Extensions": {"permessage-deflate; client_max_window_bits, x-webkit-deflate-frame"},
	}

	wsConn, resp, err := websocket.NewClient(rawConn, u, wsHeaders, 1024, 1024)
	if err != nil {
		log.Println(fmt.Errorf("websocket.NewClient Error: %s\nResp:%+v", err, resp))
		return
	}

	for {
		_, r, err := wsConn.NextReader()
		if err != nil {
			log.Println(err)
			return
		}


		dec := json.NewDecoder(r)
		var objmap map[string]*json.RawMessage
		dec.Decode(&objmap)

		var actionType string
		json.Unmarshal(*objmap["type"], &actionType)
		println(actionType)

		var pod api.Pod
		err = json.Unmarshal(*objmap["object"], &pod)

		switch actionType {
			case "MODIFIED":
				registrate(pod)
			case "DELETED":
				println("deleted stuff")
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

	if _, err := client.Set("vulcan/backends/todo_app_backend/servers/" + pod.Status.PodIP, `{"URL": "` + podUrl + `"}`, 0); err != nil {
		log.Fatal(err)
	}
}

func deletePod(pod api.Pod) {
	fmt.Printf("Deleting pod %v from %v\n", pod.Name, etcdAddress)

	machines := []string{etcdAddress}
	client := etcd.NewClient(machines)

	_, err := client.Delete("vulcan/backends/todo_app_backend/servers/" + pod.Status.PodIP, false);

	if err != nil {
		log.Fatal(err)
	}
}
