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

	kubeAPI "k8s.io/kubernetes/pkg/api"
	kubeCache "k8s.io/kubernetes/pkg/client/cache"
	kubeClient "k8s.io/kubernetes/pkg/client/unversioned"
	kubeClientCmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	kubeFramework "k8s.io/kubernetes/pkg/controller/framework"
	kubeFields "k8s.io/kubernetes/pkg/fields"
	kubeLabels "k8s.io/kubernetes/pkg/labels"
	kubeRuntime "k8s.io/kubernetes/pkg/runtime"
	kubeWatch "k8s.io/kubernetes/pkg/watch"
)

type ServicesWatcher interface {
	AddService(obj interface{})
	DeleteService(obj interface{})
	UpdateService(oldObj interface{}, newObj interface{})
}

type EndpointsWatcher interface {
	AddEndpoints(obj interface{})
	DeleteEndpoints(obj interface{})
	UpdateEndpoints(oldObj interface{}, newObj interface{})
}

func NewKubeClient(apiserverURLString string) (*kubeClient.Client, error) {
	var u *url.URL
	var err error
	if u, err = url.Parse(os.ExpandEnv(apiserverURLString)); err != nil {
		return nil, fmt.Errorf("Could not parse Kubernetes apiserver URL: %v. Error: %v", apiserverURLString, err)
	}
	if u.Scheme == "" || u.Host == "" || u.Host == ":" || u.Path != "" {
		return nil, fmt.Errorf("Invalid URL provided for Kubernetes API server: %v.", u)
	}

	loadingRules := kubeClientCmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &kubeClientCmd.ConfigOverrides{}
	kubeConfig := kubeClientCmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	config.Host = u.String()
	config.Version = "v1"

	log.WithFields(log.Fields{"apiserverURL": config.Host, "apiVersion": config.Version}).Debug("Creating kubernetes API client")

	return kubeClient.New(config)
}

// func testKubeConnectivity(client *kubeClient.Client, labelSelector kubeLabels.Selector) error {
// 	// _, err := client.Services(kubeAPI.NamespaceDefault).List(labelSelector)
// 	_, err := client.ServerVersion()
// 	return err
// }

func getServiceForEndpoints(serviceStore kubeCache.Store, e *kubeAPI.Endpoints) (*kubeAPI.Service, error) {
	var (
		err    error
		key    string
		obj    interface{}
		exists bool
		ok     bool
		svc    *kubeAPI.Service
	)
	if key, err = kubeCache.MetaNamespaceKeyFunc(e); err != nil {
		return nil, err
	}
	if obj, exists, err = serviceStore.GetByKey(key); err != nil {
		return nil, fmt.Errorf("Error getting service object from services store - %v", err)
	}
	if !exists {
		log.WithFields(log.Fields{"name": e.Name, "namespace": e.Namespace}).Warn("Unable to find service for endpoint")
		return nil, nil
	}
	if svc, ok = obj.(*kubeAPI.Service); !ok {
		return nil, fmt.Errorf("got a non service object in services store %v", obj)
	}
	return svc, nil
}

func getEndpointsForService(endpointsStore kubeCache.Store, s *kubeAPI.Service) (*kubeAPI.Endpoints, error) {
	var (
		err    error
		key    string
		obj    interface{}
		exists bool
		ok     bool
		e      *kubeAPI.Endpoints
	)
	if key, err = kubeCache.MetaNamespaceKeyFunc(s); err != nil {
		return nil, err
	}
	if obj, exists, err = endpointsStore.GetByKey(key); err != nil {
		return nil, fmt.Errorf("Error getting endpoints object from endpoints store - %v", err)
	}
	if !exists {
		log.WithFields(log.Fields{"name": s.Name, "namespace": s.Namespace}).Warn("Unable to find service for endpoint")
		return nil, nil
	}
	if e, ok = obj.(*kubeAPI.Endpoints); !ok {
		return nil, fmt.Errorf("got a non endpoints object in endpoints store %v", obj)
	}
	return e, nil
}

func buildServiceWatch(client *kubeClient.Client, watcher ServicesWatcher, tagLabel string, resyncPeriod time.Duration) (kubeCache.Store, *kubeFramework.Controller) {
	return kubeFramework.NewInformer(
		buildServiceLW(client),
		&kubeAPI.Service{},
		resyncPeriod,
		kubeFramework.ResourceEventHandlerFuncs{
			AddFunc:    watcher.AddService,
			DeleteFunc: watcher.DeleteService,
			UpdateFunc: watcher.UpdateService,
		},
	)
}

func buildEndpointsWatch(client *kubeClient.Client, watcher EndpointsWatcher, tagLabel string, resyncPeriod time.Duration) (kubeCache.Store, *kubeFramework.Controller) {
	return kubeFramework.NewInformer(
		buildEndpointsLW(client),
		&kubeAPI.Endpoints{},
		resyncPeriod,
		kubeFramework.ResourceEventHandlerFuncs{
			AddFunc:    watcher.AddEndpoints,
			DeleteFunc: watcher.DeleteEndpoints,
			UpdateFunc: watcher.UpdateEndpoints,
		},
	)
}

// Returns a kubeCache.ListWatch that gets all changes to services.
func buildServiceLW(client *kubeClient.Client) *kubeCache.ListWatch {
	return buildLW(client, "services", nil)
}

// Returns a kubeCache.ListWatch that gets all changes to endpoints.
func buildEndpointsLW(client *kubeClient.Client) *kubeCache.ListWatch {
	return buildLW(client, "endpoints", kubeLabels.Everything().Add(LabelServiceHTTPRouted, kubeLabels.ExistsOperator, []string{}))
}

// Returns a kubeCache.ListWatch that gets all changes to endpoints.
func buildLW(client *kubeClient.Client, resource string, labelSelector kubeLabels.Selector) *kubeCache.ListWatch {
	return newListWatchFromClient(
		client,
		resource,
		kubeAPI.NamespaceAll,
		labelSelector,
		kubeFields.Everything())
}

// copied (with slight modifications) from: k8s.io/kubernetes/pkg/client/cache NewListWatchFromClient
func newListWatchFromClient(c kubeCache.Getter, resource string, namespace string, labelSelector kubeLabels.Selector, fieldSelector kubeFields.Selector) *kubeCache.ListWatch {
	listFunc := func() (kubeRuntime.Object, error) {
		return c.Get().
			Namespace(namespace).
			Resource(resource).
			LabelsSelectorParam(labelSelector).
			FieldsSelectorParam(fieldSelector).
			Do().
			Get()
	}
	watchFunc := func(resourceVersion string) (kubeWatch.Interface, error) {
		return c.Get().
			Prefix("watch").
			Namespace(namespace).
			Resource(resource).
			LabelsSelectorParam(labelSelector).
			FieldsSelectorParam(fieldSelector).
			Param("resourceVersion", resourceVersion).
			Watch()
	}
	return &kubeCache.ListWatch{ListFunc: listFunc, WatchFunc: watchFunc}
}
