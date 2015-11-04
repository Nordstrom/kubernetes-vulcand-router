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
This tool watches the Kubernetes API server for Service (de)registration. New Services are registered to
Vulcan by setting the correct etcd keys. A deleted Service is deleted from Vulcan as well by removing it's key in etcd.
Services will be registered using the following key pattern in etcd: /vulcan/backends/[svc label name]/servers/[svc IP]. Make sure
your Vulcan backend/frontend configuration is configured to use backend servers based on the svc name.
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"
	etcdclient "github.com/coreos/etcd/client"
	"github.com/gorilla/websocket"
	vulcand "github.com/mailgun/vulcand/engine"
	"k8s.io/kubernetes/pkg/api"
)

const (
	AnnotationsRouteKey = "vulcand.io/Route"
)

var apiserverEndpoint string
var etcdAddress string

type Registration struct {
	URL string
}

func init() {
	flag.StringVar(&apiserverEndpoint, "apiserver", "", "Kubernetes apiserver host:port")
	flag.StringVar(&etcdAddress, "etcd", "", "etcd address(es); comma-separated")

	flag.Parse()

	if apiserverEndpoint == "" || etcdAddress == "" {
		log.Fatal(`Missing required properties. Usage: kubernetes-vulcand-router -apiserver "[kubernetes-server]" -etcd "[etcd-address]"`)
	}
}

func main() {
	listenForServices(apiserverEndpoint, etcdAddress)
}

/**
Open WS connection and start Go routines to listen for services.
*/
func listenForServices(servicesHost, etcdAddresses string) {
	log.Printf("Listening for Services from %v and registering them to %v", servicesHost, etcdAddresses)
	servicesEndpoint := fmt.Sprintf("ws://%v/api/v1/services?watch=true", servicesHost)
	servicesURL, err := url.Parse(servicesEndpoint)
	if err != nil {
		log.Fatalf("Unable to parse URL. Error: %v", err)
	}
	etcd, err2 := newEtcdClient(etcdAddresses)
	if err2 != nil {
		log.Fatalf("Unable to create etcd client. Error: %v", err2)
	}

	wsConn := openConnection(*servicesURL)

	var wsErrors chan string = make(chan string)
	go listen(etcd, wsConn, wsErrors)
	go reconnector(etcd, *servicesURL, wsErrors)
	select {}

}

/**
Open WS connection and start Go routines to listen for services.
*/
func newEtcdClient(etcdAddresses string) (etcdclient.Client, error) {
	cfg := etcdclient.Config{
		Endpoints: strings.Split(etcdAddresses, ","),
		Transport: etcdclient.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}

	return etcdclient.New(cfg)
}

/**
Open WebSocket connection to Kubernetes API server
*/
func openConnection(apiserverURL url.URL) *websocket.Conn {
	rawConn, err := net.Dial("tcp", apiserverURL.Host)
	if err != nil {
		log.Fatalf("Unable to open a connection to apiserver at %v. Error: %v", apiserverURL.String(), err)
	}

	wsHeaders := http.Header{
		"Origin":                   {apiserverURL.Host},
		"Sec-WebSocket-Extensions": {"permessage-deflate; client_max_window_bits, x-webkit-deflate-frame"},
	}

	wsConn, resp, err := websocket.NewClient(rawConn, &apiserverURL, wsHeaders, 1024, 1024)
	if err != nil {
		log.Fatalf("websocket.NewClient Error: %s\nResp:%+v", err, resp)
	}

	return wsConn
}

/**
When the WebSocket connection disconnects for some reason, try to reconnect.
*/
func reconnector(etcd etcdclient.Client, apiserverURL url.URL, wsErrors chan string) {
	for {
		_ = <-wsErrors
		log.Println("Reconnecting...")
		go listen(etcd, openConnection(apiserverURL), wsErrors)
	}
}

/**
Listen for Services. We're only interested in MODIFIED and DELETED events.
*/
func listen(etcdC etcdclient.Client, wsConn *websocket.Conn, wsErrors chan string) {
	log.Println("Listening for services")
	etcd := etcdclient.NewKeysAPI(etcdC)

	for {
		_, r, err := wsConn.NextReader()

		if err != nil {
			log.Printf("Error getting reader: %v", err)
			wsErrors <- "Error"
			return
		}

		dec := json.NewDecoder(r)
		var objmap map[string]*json.RawMessage
		dec.Decode(&objmap)

		var actionType string
		json.Unmarshal(*objmap["type"], &actionType)
		log.Printf(actionType)

		var service api.Service
		err = json.Unmarshal(*objmap["object"], &service)
		log.Printf("%v", service)

		switch actionType {
		case "MODIFIED":
			register(etcd, service)
		case "DELETED":
			deleteService(etcd, service)
		}
	}
}

/**
Register a new backend server in Vulcan based on the new Service
*/
func register(etcd etcdclient.KeysAPI, svc api.Service) {
	log.Printf("Register Service %v listening on %v\n", svc.Name, svc.Spec.ClusterIP)

	// if len(svc.Spec.Containers[0].Ports) == 0 {
	// 	log.Println("No ports exposed by container, skipping registration")
	// 	return
	// }

	serviceID := fmt.Sprintf("%v-%v", svc.Namespace, svc.Name)

	backendKey := fmt.Sprintf("/vulcan/backends/%v/backend", serviceID)
	_, err := etcd.Get(context.Background(), backendKey, nil)
	if err != nil {
		log.Printf("Can't retrieve backend on key %v\n", backendKey)
		log.Print(err)
		return
	}
	backend, err := vulcandBackendFromService(svc)
	if err != nil {
		log.Fatal("Unable to build Vulcand Backend from Service: %v. Error: %v", svc, err)
	}
	backendJSON, err2 := json.Marshal(backend)
	if err2 != nil {
		log.Fatal("Unable to JSON encode Vulcand Backend: %v. Error: %v", backend, err2)
	}
	if _, err := etcd.Set(context.Background(), backendKey, string(backendJSON), nil); err != nil {
		log.Fatal("Unable to save Vulcand Backend JSON to etcd. Error: %v", err)
	}

	frontendKey := fmt.Sprintf("/vulcan/frontends/%v/frontend", serviceID)
	frontend, err := vulcandFrontendFromBackendAndService(backend, svc)
	if err != nil {
		log.Fatal("Unable to build Vulcand Frontend from Backend (%v) and Service (%v). Error: %v", backend, svc, err)
	}
	frontendJSON, err2 := json.Marshal(frontend)
	if err2 != nil {
		log.Fatal("Unable to JSON encode Vulcand Frontend: %v. Error: %v", frontend, err2)
	}
	if _, err := etcd.Set(context.Background(), frontendKey, string(frontendJSON), nil); err != nil {
		log.Fatal("Unable to save Vulcand Frontend JSON to etcd. Error: %v", err)
	}

	// serverKey := fmt.Sprintf("/vulcan/backends/%v-%v/servers/%v", svc.Namespace, svc.Labels["name"], svc.Status.ClusterIP)
	// if _, err := client.Set(serverKey, `{"URL": "`+svcUrl+`"}`, nil); err != nil {
	// 	log.Fatal(err)
	// }
}

func vulcandFrontendFromBackendAndService(backend *vulcand.Backend, svc api.Service) (*vulcand.Frontend, error) {
	serviceID := fmt.Sprintf("%v-%v", svc.Namespace, svc.Labels["name"])
	routeExpr := svc.Annotations[AnnotationsRouteKey]
	settings := vulcand.HTTPFrontendSettings{}
	return vulcand.NewHTTPFrontend(serviceID, backend.Id, routeExpr, settings)
}

func vulcandBackendFromService(svc api.Service) (*vulcand.Backend, error) {
	serviceID := fmt.Sprintf("%v-%v", svc.Namespace, svc.Labels["name"])
	return vulcand.NewHTTPBackend(serviceID, vulcand.HTTPBackendSettings{})
}

/**
Delete a frontend and backend from Vulcan when a Service is deleted
*/
func deleteService(etcd etcdclient.KeysAPI, svc api.Service) error {
	serviceID := fmt.Sprintf("%v-%v", svc.Namespace, svc.Labels["name"])
	key := fmt.Sprintf("/vulcan/backends/%v/servers/%v", serviceID, svc.Spec.ClusterIP)

	log.Printf("Deleting backend %v from %v", key, etcdAddress)

	_, err := etcd.Delete(context.Background(), key, nil)
	if err != nil {
		log.Fatalf("Unable to delete backend from etcd (key: %v). Error: %v", key, err)
	}

	return nil
}
