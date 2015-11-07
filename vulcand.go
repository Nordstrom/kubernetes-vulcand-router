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
	return vulcand.NewServer(ip, serverURL.String())
}
