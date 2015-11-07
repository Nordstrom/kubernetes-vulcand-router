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
	"time"

	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"
	etcdclient "github.com/coreos/etcd/client"
)

type EtcdClient interface {
	Get(ctx context.Context, key string, opts *etcdclient.GetOptions) (*etcdclient.Response, error)
	Set(ctx context.Context, key, value string, opts *etcdclient.SetOptions) (*etcdclient.Response, error)
	Delete(ctx context.Context, key string, opts *etcdclient.DeleteOptions) (*etcdclient.Response, error)
}

/**
Initialize an etcd API client
*/
func newEtcdClient(etcdAddresses []string) (EtcdClient, error) {
	var (
		etcdClient etcdclient.Client
		err error
	)

	if etcdClient, err = newRawEtcdClient(etcdAddresses); err == nil {
		return etcdclient.NewKeysAPI(etcdClient), nil
	}

	return nil, err
}

/**
Initialize an etcd API client
*/
func newRawEtcdClient(etcdAddresses []string) (etcdclient.Client, error) {
	cfg := etcdclient.Config{
		Endpoints: etcdAddresses,
		Transport: etcdclient.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}

	return etcdclient.New(cfg)
}

