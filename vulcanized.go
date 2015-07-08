/*
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/**
This tool watches the Kubernetes API server for Pod (de)registration. New Pods are registered to
Vulcan by setting the correct etcd keys. A deleted Pod is deleted from Vulcan as well by removing it's key in etcd.
Pods will be registered using the following key pattern in etcd: /vulcan/backends/[pod label name]/servers/[pod IP]. Make sure
your Vulcan backend/frontend configuration is configured to use backend servers based on the pod name.
 */
package main

import (
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"net/url"
	"log"
	"encoding/json"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"flag"
	"github.com/coreos/go-etcd/etcd"
	"fmt"
	"strings"
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

	if kubernetesEndpoint == "" || etcdAddress == "" {
		log.Fatal(`Missing required properties. Usage: Registrator -pods "ws://[kubernetes-server]/api/v1/pods?watch=true" -etcd "[etcd-address]"`)
	}
}

func main() {
	listenForPods()
}

/**
Open WS connection and start Go routines to listen for pods.
 */
func listenForPods() {

	wsConn := openConnection()

	var wsErrors chan string = make(chan string)
	go listen(wsConn, wsErrors)
	go reconnector(wsErrors)
	select {}

}

/**
Open WebSocket connection to Kubernetes API server
 */
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
		log.Fatalf("websocket.NewClient Error: %s\nResp:%+v", err, resp)

	}

	return wsConn
}

/**
When the WebSocket connection disconnects for some reason, try to reconnect.
 */
func reconnector(wsErrors chan string) {
	for {
		_ = <- wsErrors
		log.Println("Reconnecting...")
		go listen(openConnection(), wsErrors)
	}
}

/**
Listen for Pods. We're only interested in MODIFIED and DELETED events.
 */
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
			register(pod)
		case "DELETED":
			deletePod(pod)
		}
	}
}

/**
Register a new backend server in Vulcan based on the new Pod
 */
func register(pod api.Pod) {
	if(pod.Status.Phase != "Running") {
		return
	}


	log.Printf("Register pod %v listening on %v to %v\n", pod.Name, pod.Status.PodIP, etcdAddress)

	client := etcd.NewClient(strings.Split(etcdAddress, ","))

	if len(pod.Spec.Containers[0].Ports) == 0 {
		log.Println("No ports exposed by container, skipping registration")
		return
	}

	podUrl := fmt.Sprintf("http://%v:%v", pod.Status.PodIP, pod.Spec.Containers[0].Ports[0].ContainerPort)

	backendKey := fmt.Sprintf("/vulcan/backends/%v-%v/backend", pod.Namespace, pod.Labels["name"])
	_, err := client.Get(backendKey, false, false)
	if err != nil {
		log.Printf("Can't retrieve backend on key %v\n", backendKey)
		log.Print(err)
		return
	}

	serverKey := fmt.Sprintf("vulcan/backends/%v-%v/servers/%v", pod.Namespace, pod.Labels["name"], pod.Status.PodIP)

	if _, err := client.Set(serverKey, `{"URL": "` + podUrl + `"}`, 0); err != nil {
		log.Fatal(err)
	}
}

/**
Delete a backend server from Vulcan when a Pod is deleted.
 */
func deletePod(pod api.Pod) {
	key := fmt.Sprintf("vulcan/backends/%v-%v/servers/%v", pod.Namespace, pod.Labels["name"],pod.Status.PodIP)

	log.Printf("Deleting backend %v from %v", key, etcdAddress)

	client := etcd.NewClient(strings.Split(etcdAddress, ","))

	_, err := client.Delete(key, false);

	if err != nil {
		log.Printf("Failed to delete backend '%v'. It might already be removed", key);
		log.Println(err)
	}
}
