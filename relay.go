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
	"errors"
	"fmt"
	"net/url"
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
	LabelServiceHTTPRouted     = "vulcand.io/http-routed"
	AnnotationsKeyServiceRoute = "vulcand.io/route"
	ServiceEndpointPortName    = "http"
)

var ErrorCouldNotFindRouteExpression = errors.New("Could not find route expression for service")

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
	apiserverURL         *url.URL
	vulcandURL           *url.URL
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

	if kClient, err = NewKubeClient(apiserverURLString); err != nil {
		// return nil, fmt.Errorf("Unable to create Kubernetes API client. Error: %v", err)
		return nil, err
	}
	if vClient, err = NewVulcandClient(vulcandAdminURLString); err != nil {
		// return nil, fmt.Errorf("Unable to create Vulcand API client. Error: %v", err)
		return nil, err
	}
	kURL, _ := url.Parse(apiserverURLString)
	vURL, _ := url.Parse(vulcandAdminURLString)

	rly := &relay{
		kubeClient:           kClient,
		vulcandClient:        vClient,
		vulcandUpdateTimeout: vulcandUpdateTimeout,
		apiserverURL:         kURL,
		vulcandURL:           vURL,
		stopC:                make(chan struct{}),
	}

	rly.serviceStore, rly.serviceController = buildServiceWatch(kClient, rly, LabelServiceHTTPRouted, resyncPeriod)
	rly.endpointsStore, rly.endpointsController = buildEndpointsWatch(kClient, rly, LabelServiceHTTPRouted, resyncPeriod)

	return rly, nil
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
	// 	labelSelector := kubeLabels.Everything().Add(LabelServiceHTTPRouted, kubeLabels.ExistsOperator, nil)
	// 	return testKubeConnectivity(kClient, labelSelector)
	// }
	// return fmt.Errorf("Unable to type assert kubeClient as kubeAPI.Client.")
	log.WithField("apiserverURL", rly.apiserverURL).Debug("Testing connectivity to Kubernetes apiserver")
	_, err := rly.kubeClient.ServerVersion()
	return err
}

func (rly *relay) testVulcandConnectivity() error {
	// if vClient, ok := rly.vulcandClient.(*vulcandAPI.Client); ok {
	// 	return testVulcandConnectivity(vClient)
	// }
	// return fmt.Errorf("Unable to type assert vulcandClient as vulcandAPI.Client.")
	log.WithField("vulcandURL", rly.vulcandURL).Debug("Testing connectivity with Vulcand Admin API")
	return rly.vulcandClient.GetStatus()
}

func (rly *relay) AddService(obj interface{}) {
	log.WithField("service", obj).Debug("Attempting to add service")

	if s, ok := obj.(*kubeAPI.Service); ok {
		serviceID := vulcandID(s)
		if s.Spec.Type != kubeAPI.ServiceTypeClusterIP || kubeAPI.IsServiceIPSet(s) {
			log.WithField("serviceID", serviceID).Info("Not adding service")
			return
		}

		rly.handleAddService(s)
	}
}

func (rly *relay) handleAddService(s *kubeAPI.Service) {
	rly.mlock.Lock()
	defer rly.mlock.Unlock()

	var (
		err       error
		backend   *vulcand.Backend
		frontend  *vulcand.Frontend
		endpoints *kubeAPI.Endpoints
	)
	serviceID := vulcandID(s)
	log.WithField("serviceID", serviceID).Info("Adding service")
	if backend, err = newVulcandBackend(serviceID); err != nil {
		log.WithFields(log.Fields{"serviceID": serviceID, "error": err}).Warn("Could not create vulcand backend")
		return
	}
	if frontend, err = vulcandFrontendFromBackendAndService(*backend, s); err != nil {
		log.WithFields(log.Fields{"serviceID": serviceID, "error": err}).Warn("Could not create vulcand frontend")
		return
	}
	if endpoints, err = rly.getEndpointsForService(s); err != nil || endpoints == nil {
		log.WithFields(log.Fields{"serviceID": serviceID, "error": err}).Warn("Could not get endpoints for service")
		return
	}
	updateVulcandOrDie(rly.vulcandUpdateTimeout, func() error {
		if err = rly.vulcandClient.UpsertBackend(*backend); err != nil {
			return err
		}
		if err = rly.vulcandClient.UpsertFrontend(*frontend, 0); err != nil {
			return err
		}
		return rly.syncServersForHeadlessService(endpoints, s)
	})
}

func (rly *relay) DeleteService(obj interface{}) {
	rly.mlock.Lock()
	defer rly.mlock.Unlock()
	log.WithField("service", obj).Debug("Attempting to delete service")

	if s, ok := obj.(*kubeAPI.Service); ok {
		remainingServersMap, err := rly.getServersForService(s)
		if err != nil {
			log.WithField("service", s).Info("Unable to retrieve Vulcand Servers for Kubernetes service")
		}
		updateVulcandOrDie(rly.vulcandUpdateTimeout, func() error {
			serviceID := vulcandID(s)
			if err := rly.removeServersForService(s, remainingServersMap); err != nil {
				log.WithFields(log.Fields{
					"serviceID":         serviceID,
					"serversForService": remainingServersMap,
					"error":             err,
				}).Warn("Unable to remove Vulcand Servers for Kubernetes service")
				return err
			}
			if err := rly.vulcandClient.DeleteFrontend(vulcand.FrontendKey{Id: serviceID}); err != nil {
				log.WithField("serviceID", serviceID).Warn("Unable to remove Vulcand Frontend for Kubernetes service")
				return err
			}
			if err := rly.vulcandClient.DeleteBackend(vulcand.BackendKey{Id: serviceID}); err != nil {
				log.WithField("serviceID", serviceID).Warn("Unable to remove Vulcand Backend for Kubernetes service")
				return err
			}
			log.WithField("serviceID", serviceID).Debug("Successfully removed Vulcand Servers, Frontend, and Backend for Kubernetes service")
			return nil
		})
	}
}

func (rly *relay) UpdateService(oldObj interface{}, newObj interface{}) {
	log.WithFields(log.Fields{"old": oldObj, "new": newObj}).Debug("Attempting to update service")

	rly.DeleteService(oldObj)
	rly.AddService(newObj)
}

func (rly *relay) AddEndpoints(obj interface{}) {
	log.WithField("endpoints", obj).Debug("Attempting to add endpoints")
	rly.SyncEndpoints(obj)
}

func (rly *relay) DeleteEndpoints(obj interface{}) {
	log.WithField("endpoints", obj).Debug("Attempting to remove endpoints")
	rly.SyncEndpoints(obj)
}

func (rly *relay) UpdateEndpoints(oldObj interface{}, newObj interface{}) {
	log.WithFields(log.Fields{"old": oldObj, "new": newObj}).Debug("Attempting to update endpoints")
	rly.SyncEndpoints(newObj)
}

func (rly *relay) SyncEndpoints(obj interface{}) {
	rly.mlock.Lock()
	defer rly.mlock.Unlock()

	if e, ok := obj.(*kubeAPI.Endpoints); ok {
		svc, err := rly.getServiceForEndpoints(e)
		if err != nil {
			return
		}
		if svc == nil || kubeAPI.IsServiceIPSet(svc) {
			svcID := ""
			if svc != nil {
				svcID = vulcandID(svc)
			}
			log.WithField("serviceID", svcID).Debug("No headless service found corresponding to Endpoints.")
			return
		}
		updateVulcandOrDie(rly.vulcandUpdateTimeout, func() error {
			return rly.syncServersForHeadlessService(e, svc)
		})
	}
}

func (rly *relay) syncServersForHeadlessService(e *kubeAPI.Endpoints, svc *kubeAPI.Service) error {
	backendKey := backendKeyFromService(svc)
	remainingServersMap, err := rly.getServersForService(svc)
	if err != nil {
		// return fmt.Errorf("Unable to retrieve Vulcand Servers for Kubernetes service: %v", vulcandID(svc))
		return err
	}

	for idx := range e.Subsets {
		for subIdx := range e.Subsets[idx].Addresses {
			for portIdx := range e.Subsets[idx].Ports {
				endpointPort := &e.Subsets[idx].Ports[portIdx]
				if endpointPort.Name == ServiceEndpointPortName {
					addr := e.Subsets[idx].Addresses[subIdx].IP
					s, err := newVulcandServer(addr, endpointPort.Port)
					if s == nil || err != nil {
						log.WithFields(log.Fields{"address": addr, "port": endpointPort.Port, "error": err}).Warn("Unable to create vulcand server")
						continue
					}
					srv := *s
					err = rly.vulcandClient.UpsertServer(backendKey, srv, 0)
					if err != nil {
						log.WithFields(log.Fields{"server": srv, "error": err}).Warn("Unable to upsert vulcand server")
						continue
					}
					delete(remainingServersMap, serverKeyFromBackendKeyAndServer(backendKey, srv))
				}
			}
		}
	}

	err = rly.removeServersForService(svc, remainingServersMap)

	return nil
}

func (rly *relay) getEndpointsForService(s *kubeAPI.Service) (*kubeAPI.Endpoints, error) {
	return getEndpointsForService(rly.endpointsStore, s)
}

func (rly *relay) getServiceForEndpoints(e *kubeAPI.Endpoints) (*kubeAPI.Service, error) {
	return getServiceForEndpoints(rly.serviceStore, e)
}

func (rly *relay) getServersForService(s *kubeAPI.Service) (map[vulcand.ServerKey]vulcand.Server, error) {
	return getServersForBackendKey(rly.vulcandClient, backendKeyFromService(s))
}

func (rly *relay) removeServersForService(svc *kubeAPI.Service, servers map[vulcand.ServerKey]vulcand.Server) error {
	return removeServersForService(rly.vulcandClient, vulcandID(svc), servers)
}

func backendKeyFromService(service *kubeAPI.Service) vulcand.BackendKey {
	return vulcand.BackendKey{Id: vulcandID(service)}
}

func serverKeyFromBackendKeyAndServer(backendKey vulcand.BackendKey, server vulcand.Server) vulcand.ServerKey {
	return vulcand.ServerKey{BackendKey: backendKey, Id: server.Id}
}

func vulcandFrontendFromBackendAndService(backend vulcand.Backend, svc *kubeAPI.Service) (*vulcand.Frontend, error) {
	route, err := routeExpressionFromService(svc)
	if err != nil {
		return nil, err
	}
	return newVulcandFrontend(vulcandID(svc), backend.Id, route)
}

func routeExpressionFromService(svc *kubeAPI.Service) (string, error) {
	route, ok := svc.Annotations[AnnotationsKeyServiceRoute]
	if !ok {
		return "", ErrorCouldNotFindRouteExpression
	}
	return route, nil
}

func vulcandID(s *kubeAPI.Service) string {
	return fmt.Sprintf("%v-%v", s.Namespace, s.Name)
}
