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
)

type ServicesWatcher interface {
	AddService(obj interface{})
	DeleteService(obj interface{})
	UpdateService(oldObj interface{}, newObj interface{})
}

type EndpointsWatcher interface {
	AddEndpoint(obj interface{})
}

/**
Initialize a kubernetes API client
*/
func newKubeClient(apiserverURLString string) (*kubeClient.Client, error) {
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
	config.Host = apiserverURLString
	config.Version = "v1"

	log.WithFields(log.Fields{"host": config.Host, "apiVersion": config.Version}).Debug("Creating kubernetes API client")

	return kubeClient.New(config)
}

// func testKubeConnectivity(client *kubeClient.Client, labelSelector kubeLabels.Selector) error {
// 	// _, err := client.Services(kubeAPI.NamespaceDefault).List(labelSelector)
// 	_, err := client.ServerVersion()
// 	return err
// }

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
			AddFunc: watcher.AddEndpoint,
			UpdateFunc: func(oldObj, newObj interface{}) {
				// TODO: Avoid unwanted updates.
				watcher.AddEndpoint(newObj)
			},
		},
	)
}

func getServiceFromEndpoints(serviceStore kubeCache.Store, e *kubeAPI.Endpoints) (*kubeAPI.Service, error) {
	var (
		err error
		key string
		obj interface{}
		exists bool
		ok bool
		svc *kubeAPI.Service
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

// Returns a kubeCache.ListWatch that gets all changes to services.
func buildServiceLW(client *kubeClient.Client) *kubeCache.ListWatch {
	return buildLW(client, "services")
}

// Returns a kubeCache.ListWatch that gets all changes to endpoints.
func buildEndpointsLW(client *kubeClient.Client) *kubeCache.ListWatch {
	return buildLW(client, "endpoints")
}

func buildLW(client *kubeClient.Client, target string) *kubeCache.ListWatch {
	return kubeCache.NewListWatchFromClient(client, target, kubeAPI.NamespaceAll, kubeFields.Everything())
}
