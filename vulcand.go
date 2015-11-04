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

	"github.com/mailgun/vulcand/api"
	"github.com/mailgun/vulcand/engine"
	// "github.com/mailgun/vulcand/plugin"
	"github.com/mailgun/vulcand/plugin/registry"
)

type VulcandClient interface {
	UpsertBackend(engine.Backend) error
	DeleteBackend(engine.BackendKey) error
	UpsertFrontend(engine.Frontend, time.Duration) error
	DeleteFrontend(engine.FrontendKey) error
	GetServers(engine.BackendKey) ([]engine.Server, error)
	UpsertServer(engine.BackendKey, engine.Server, time.Duration) error
	DeleteServer(engine.ServerKey) error
}

/**
Initialize an etcd API client
*/
func newVulcandClient(vulcandAdminURLString string) (*api.Client, error) {
	var u *url.URL
	var err error
	if u, err = url.Parse(os.ExpandEnv(vulcandAdminURLString)); err != nil {
		return nil, fmt.Errorf("Could not parse Vulcand admin URL: %v. Error: %v", vulcandAdminURLString, err)
	}
	if u.Scheme == "" || u.Host == "" || u.Host == ":" || u.Path != "" {
		return nil, fmt.Errorf("Invalid URL provided for Vulcand API server: %v.", u)
	}

	c := api.NewClient(vulcandAdminURLString, registry.GetRegistry())
	if c == nil {
		return nil, fmt.Errorf("Could not initialize Vulcand API client")
	}
	return c, nil
}

func newVulcandBackend(id string) (*engine.Backend, error) {
	return engine.NewHTTPBackend(id, engine.HTTPBackendSettings{})
}

func newVulcandFrontend(id, backendID, routeExpr string) (*engine.Frontend, error) {
	return engine.NewHTTPFrontend(id, backendID, routeExpr, engine.HTTPFrontendSettings{})
}

func newVulcandServer(ip string, port int) (*engine.Server, error) {
	serverURL, err := url.Parse(fmt.Sprintf("http://%v:%v/", ip, port))
	if err != nil {
		return nil, err
	}
	return engine.NewServer(ip, serverURL.String())
}
