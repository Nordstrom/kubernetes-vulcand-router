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

package main

import (
	"fmt"
	"net/url"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	vulcandAPI "github.com/mailgun/vulcand/api"
	vulcand "github.com/mailgun/vulcand/engine"
	// "github.com/mailgun/vulcand/plugin"
	vulcandRegistry "github.com/mailgun/vulcand/plugin/registry"
)

/**
Initialize an etcd API client
*/
func newVulcandClient(vulcandAdminURLString string) (*vulcandAPI.Client, error) {
	var u *url.URL
	var err error
	if u, err = url.Parse(os.ExpandEnv(vulcandAdminURLString)); err != nil {
		return nil, fmt.Errorf("Could not parse Vulcand admin URL: %v. Error: %v", vulcandAdminURLString, err)
	}
	if u.Scheme == "" || u.Host == "" || u.Host == ":" || u.Path != "" {
		return nil, fmt.Errorf("Invalid URL provided for Vulcand API server: %v.", u)
	}

	c := vulcandAPI.NewClient(vulcandAdminURLString, vulcandRegistry.GetRegistry())
	if c == nil {
		return nil, fmt.Errorf("Could not initialize Vulcand API client")
	}
	return c, nil
}

func updateVulcandOrDie(timeout time.Duration, mutator func() error) {
	timeoutC := time.After(timeout)
	for {
		select {
		case <-timeoutC:
			log.WithFields(log.Fields{"timeout": timeout}).Fatal("Failed to update vulcand within timeout")
		default:
			if err := mutator(); err != nil {
				delay := 50 * time.Millisecond
				log.WithFields(log.Fields{"error": err, "retryIn": delay}).Info("Failed attempt to update vulcand. May retry if time remains.")
				time.Sleep(delay)
			} else {
				log.Debug("Updated vulcand using mutator")
				return
			}
		}
	}
}

func getServersForService(client VulcandClient, serviceID string) (map[vulcand.ServerKey]vulcand.Server, error) {
	backendKey := vulcand.BackendKey{Id: serviceID}
	serverMap := make(map[vulcand.ServerKey]vulcand.Server)
	servers, err := client.GetServers(backendKey)
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		serverKey := serverKeyFromBackendKeyAndServer(backendKey, server)
		serverMap[serverKey] = server
	}
	return serverMap, nil
}

func removeServersForService(client VulcandClient, serviceID string, servers map[vulcand.ServerKey]vulcand.Server) error {
	backendKey := vulcand.BackendKey{Id:serviceID}
	for _, s := range servers {
		serverKey := serverKeyFromBackendKeyAndServer(backendKey, s)
		if err := client.DeleteServer(serverKey); err != nil {
			log.WithFields(log.Fields{
				"serverID":  s.Id,
				"error":     err,
				"serviceID": serviceID,
			}).Debug("Error deleting Vulcand server for Kubernetes service")
			return err
		}
		delete(servers, serverKey)
	}
	return nil
}

func testVulcandConnectivity(client *vulcandAPI.Client) error {
	return client.GetStatus()
}

func newVulcandBackend(id string) (*vulcand.Backend, error) {
	return vulcand.NewHTTPBackend(id, vulcand.HTTPBackendSettings{})
}

func newVulcandFrontend(id, backendID, routeExpr string) (*vulcand.Frontend, error) {
	return vulcand.NewHTTPFrontend(id, backendID, routeExpr, vulcand.HTTPFrontendSettings{})
}

func newVulcandServer(ip string, port int) (*vulcand.Server, error) {
	serverURL, err := url.Parse(fmt.Sprintf("http://%v:%v/", ip, port))
	if err != nil {
		return nil, err
	}
	serverID := fmt.Sprintf("%v.%v", ip, port)
	return vulcand.NewServer(serverID, serverURL.String())
}
