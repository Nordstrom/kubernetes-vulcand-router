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
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	vulcandAPI "github.com/mailgun/vulcand/api"
	vulcand "github.com/mailgun/vulcand/engine"

	kubeAPI "k8s.io/kubernetes/pkg/api"
	kubeCache "k8s.io/kubernetes/pkg/client/cache"
	kubeClient "k8s.io/kubernetes/pkg/client/unversioned"
	kubeFramework "k8s.io/kubernetes/pkg/controller/framework"
	kubeVersion "k8s.io/kubernetes/pkg/version"
)

const (
	labelRoutedService            = "vulcand.io/routed"
	annotationsKeyRouteExpression = "vulcand.io/route-expression"
	annotationsKeyRoutedPortNames = "vulcand.io/routed-port-names"
)

type KubernetesVulcandRouterRelay interface {
	Start() error
	Stop()
}

type KubeClient interface {
	ServerVersion() (*kubeVersion.Info, error)
	Endpoints(namespace string) kubeClient.EndpointsInterface
}

type VulcandClient interface {
	GetStatus() error
	UpsertBackend(vulcand.Backend) error
	DeleteBackend(vulcand.BackendKey) error
	UpsertFrontend(vulcand.Frontend, time.Duration) error
	DeleteFrontend(vulcand.FrontendKey) error
	GetServers(vulcand.BackendKey) ([]vulcand.Server, error)
	UpsertServer(vulcand.BackendKey, vulcand.Server, time.Duration) error
	DeleteServer(vulcand.ServerKey) error
}

type relay struct {
	kubeClient           KubeClient
	vulcandClient        VulcandClient
	serviceStore         kubeCache.Store
	endpointsStore       kubeCache.Store
	serviceController    *kubeFramework.Controller
	endpointsController  *kubeFramework.Controller
	vulcandUpdateTimeout time.Duration
	stopC                chan struct{}
	mlock                sync.Mutex
}

func NewRelay(apiserverURLString, vulcandAdminURLString string, resyncPeriod, vulcandUpdateTimeout time.Duration) (KubernetesVulcandRouterRelay, error) {
	var (
		err     error
		vClient *vulcandAPI.Client
		kClient *kubeClient.Client
	)

	if kClient, err = newKubeClient(apiserverURLString); err != nil {
		return nil, fmt.Errorf("Unable to create Kubernetes API client. Error: %v", err)
	}
	if vClient, err = newVulcandClient(vulcandAdminURLString); err != nil {
		return nil, fmt.Errorf("Unable to create Vulcand API client. Error: %v", err)
	}

	rly := relay{
		kubeClient:           kClient,
		vulcandClient:        vClient,
		vulcandUpdateTimeout: vulcandUpdateTimeout,
		stopC:                make(chan struct{}),
	}

	rly.serviceStore, rly.serviceController = buildServiceWatch(kClient, &rly, labelRoutedService, resyncPeriod)
	rly.endpointsStore, rly.endpointsController = buildEndpointsWatch(kClient, &rly, labelRoutedService, resyncPeriod)

	return &rly, nil
}

func (rly *relay) Start() error {
	log.Info("Starting relay")

	if err := rly.testKubeConnectivity(); err != nil {
		return err
	}

	if err := rly.testVulcandConnectivity(); err != nil {
		return err
	}

	go rly.serviceController.Run(rly.stopC)
	go rly.endpointsController.Run(rly.stopC)

	return nil
}

func (rly *relay) Stop() {
	log.Info("Stopping relay")

	defer close(rly.stopC)
}

func (rly *relay) testKubeConnectivity() error {
	// if kClient, ok := rly.kubeClient.(*kubeClient.Client); ok {
	// 	labelSelector := kubeLabels.Everything().Add(labelRoutedService, kubeLabels.ExistsOperator, nil)
	// 	return testKubeConnectivity(kClient, labelSelector)
	// }
	// return fmt.Errorf("Unable to type assert kubeClient as kubeAPI.Client.")
	_, err := rly.kubeClient.ServerVersion()
	return err
}

func (rly *relay) testVulcandConnectivity() error {
	// if vClient, ok := rly.vulcandClient.(*vulcandAPI.Client); ok {
	// 	return testVulcandConnectivity(vClient)
	// }
	// return fmt.Errorf("Unable to type assert vulcandClient as vulcandAPI.Client.")
	return rly.vulcandClient.GetStatus()
}

func (rly *relay) AddService(obj interface{}) {
	log.WithField("service", obj).Debug("Adding service")
	if s, ok := obj.(*kubeAPI.Service); ok {
		var (
			err      error
			backend  *vulcand.Backend
			frontend *vulcand.Frontend
		)
		serviceID := vulcandID(s)
		if backend, err = vulcandBackendFromService(s); err != nil {
			log.WithFields(log.Fields{"serviceID": serviceID, "error": err}).Warn("Could not create vulcand backend")
			return
		}
		if frontend, err = vulcandFrontendFromBackendAndService(*backend, s); err != nil {
			log.WithFields(log.Fields{"serviceID": serviceID, "error": err}).Warn("Could not create vulcand frontend")
			return
		}
		updateVulcandOrDie(rly.vulcandUpdateTimeout, func() error {
			if err = rly.vulcandClient.UpsertBackend(*backend); err != nil {
				return err
			}
			if err = rly.vulcandClient.UpsertFrontend(*frontend, 0); err != nil {
				return err
			}
			return nil
		})
	}
}

func (rly *relay) DeleteService(obj interface{}) {
	log.WithField("service", obj).Debug("Deleting service")

	if s, ok := obj.(*kubeAPI.Service); ok {
		existingServersMap, err := rly.getServersFromService(s)
		if err != nil {
			log.WithField("service", s).Info("Unable to retrieve Vulcand Servers for Kubernetes service")
		}
		updateVulcandOrDie(rly.vulcandUpdateTimeout, func() error {
			serviceID := vulcandID(s)
			if err = rly.removeServersForService(s, existingServersMap); err != nil {
				log.WithFields(log.Fields{
					"serviceID": serviceID,
					"servers":   existingServersMap,
					"error":     err,
				}).Warn("Unable to remove Vulcand Servers for Kubernetes service")
			}
			if err := rly.vulcandClient.DeleteFrontend(vulcand.FrontendKey{Id: serviceID}); err != nil {
				log.WithField("serviceID", serviceID).Warn("Unable to remove Vulcand Frontend for Kubernetes service")
				return err
			}
			if err := rly.vulcandClient.DeleteBackend(vulcand.BackendKey{Id: serviceID}); err != nil {
				log.WithField("serviceID", serviceID).Warn("Unable to remove Vulcand Backend for Kubernetes service")
				return err
			}
			return nil
		})
	}
}

func (rly *relay) UpdateService(oldObj interface{}, newObj interface{}) {
	rly.DeleteService(oldObj)
	rly.AddService(newObj)
}

func (rly *relay) AddEndpoint(obj interface{}) {
	if e, ok := obj.(*kubeAPI.Endpoints); ok {
		updateVulcandOrDie(rly.vulcandUpdateTimeout, func() error { return rly.addServersUsingEndpoints(e) })
	}
}

func (rly *relay) addServersUsingEndpoints(e *kubeAPI.Endpoints) error {
	rly.mlock.Lock()
	defer rly.mlock.Unlock()
	svc, err := rly.getServiceFromEndpoints(e)
	if err != nil {
		return err
	}
	if svc == nil || kubeAPI.IsServiceIPSet(svc) {
		log.WithField("endpoints", e).Info("No headless service found corresponding to Endpoints.")
		return nil
	}

	return rly.syncServersForHeadlessService(e, svc)
}

func (rly *relay) syncServersForHeadlessService(e *kubeAPI.Endpoints, svc *kubeAPI.Service) error {
	routedPorts, err := getRoutedPortNamesFromService(svc)
	if err != nil {
		return fmt.Errorf("Unable to retrieve routed ports from service: %v. Error: %v", svc, err)
	}

	backendKey := backendKeyFromService(svc)
	existingServersMap, err := rly.getServersFromService(svc)
	if err != nil {
		log.WithField("service", svc).Info("Unable to retrieve Vulcand Servers for Kubernetes service")
	}

	for idx := range e.Subsets {
		for subIdx := range e.Subsets[idx].Addresses {
			for portIdx := range e.Subsets[idx].Ports {
				endpointPort := &e.Subsets[idx].Ports[portIdx]
				// portSegment := buildPortSegmentString(endpointPort.Name, endpointPort.Protocol)
				if _, ok := routedPorts[endpointPort.Name]; ok {
					addr := e.Subsets[idx].Addresses[subIdx].IP
					s, err := newVulcandServer(addr, endpointPort.Port)
					if err != nil {
						log.WithFields(log.Fields{"address": addr, "port": endpointPort.Port, "error": err}).Warn("Unable to create vulcand server")
						continue
					}
					srv := *s
					delete(existingServersMap, serverKeyFromBackendKeyAndServer(backendKey, srv))
					err = rly.vulcandClient.UpsertServer(backendKey, srv, 0)
					if err != nil {
						log.WithFields(log.Fields{"server": srv, "error": err}).Warn("Unable to upsert vulcand server")
						continue
					}
				}
			}
		}
	}

	err = rly.removeServersForService(svc, existingServersMap)

	return nil
}

func (rly *relay) getServersFromService(s *kubeAPI.Service) (map[vulcand.ServerKey]vulcand.Server, error) {
	backendKey := vulcand.BackendKey{Id: vulcandID(s)}
	serverMap := make(map[vulcand.ServerKey]vulcand.Server)
	servers, err := rly.vulcandClient.GetServers(backendKey)
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		serverMap[serverKeyFromBackendKeyAndServer(backendKey, server)] = server
	}
	return serverMap, nil
}

func (rly *relay) getServiceFromEndpoints(e *kubeAPI.Endpoints) (*kubeAPI.Service, error) {
	return getServiceFromEndpoints(rly.serviceStore, e)
}

func (rly *relay) removeServersForService(service *kubeAPI.Service, servers map[vulcand.ServerKey]vulcand.Server) error {
	backendKey := backendKeyFromService(service)
	for _, s := range servers {
		if err := rly.vulcandClient.DeleteServer(serverKeyFromBackendKeyAndServer(backendKey, s)); err != nil {
			return err
		}
	}
	return nil
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

func backendKeyFromService(service *kubeAPI.Service) vulcand.BackendKey {
	return vulcand.BackendKey{Id: vulcandID(service)}
}

func serverKeyFromBackendKeyAndServer(backendKey vulcand.BackendKey, server vulcand.Server) vulcand.ServerKey {
	return vulcand.ServerKey{BackendKey: backendKey, Id: server.Id}
}

func vulcandBackendFromService(svc *kubeAPI.Service) (*vulcand.Backend, error) {
	return newVulcandBackend(vulcandID(svc))
}

func vulcandFrontendFromBackendAndService(be vulcand.Backend, svc *kubeAPI.Service) (*vulcand.Frontend, error) {
	route, err := routeExpressionFromService(svc)
	if err != nil {
		return nil, err
	}
	return newVulcandFrontend(vulcandID(svc), be.Id, route)
}

func getRoutedPortNamesFromService(e *kubeAPI.Service) (map[string]bool, error) {
	var routedPortNames []string
	var portNames map[string]bool
	var err error
	if routedPortNames, err = routedPortNamesFromService(e); err != nil {
		return nil, err
	}

	for _, name := range routedPortNames {
		portNames[name] = true
	}

	return portNames, nil
}

func routedPortNamesFromService(svc *kubeAPI.Service) ([]string, error) {
	routedPorts, ok := svc.Annotations[annotationsKeyRoutedPortNames]
	if !ok {
		return []string{}, fmt.Errorf("Could not find routed port names for service %v", vulcandID(svc))
	}
	return strings.Split(routedPorts, ","), nil
}

func routeExpressionFromService(svc *kubeAPI.Service) (string, error) {
	route, ok := svc.Annotations[annotationsKeyRouteExpression]
	if !ok {
		return "", fmt.Errorf("Could not find route expression for service %v", vulcandID(svc))
	}
	return route, nil
}

func vulcandID(s *kubeAPI.Service) string {
	return strings.Join([]string{s.Namespace, s.Name}, "-")
}
