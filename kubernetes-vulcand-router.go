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
	"flag"
	"time"

	log "github.com/Sirupsen/logrus"
)

var argAPIServerHostPort string
var argVulcandURLString string
var argResyncPeriod time.Duration
var argVulcandTimeout time.Duration

func init() {
	flag.StringVar(&argAPIServerHostPort, "apiserver", "", "Kubernetes apiserver host:port")
	flag.StringVar(&argVulcandURLString, "vulcand", "", "Vulcand Admin URL")
	flag.DurationVar(&argResyncPeriod, "resync", 30 * time.Second, "Resync period (in seconds)")
	flag.DurationVar(&argVulcandTimeout, "vulcan-timeout", 10 * time.Second, "Vulcand update timeout (in seconds)")

	flag.Parse()

	if argAPIServerHostPort == "" || argVulcandURLString == "" {
		log.Fatal("Missing required properties. Usage: kubernetes-vulcand-router -apiserver '[kubernetes-server]' -vulcand '[vulcand-address]'")
	}

	// Only log the warning severity or above.
	log.SetLevel(log.DebugLevel)
}

func main() {
	relay, err := NewRelay(argAPIServerHostPort, argVulcandURLString, argResyncPeriod, argVulcandTimeout)
	if err != nil {
		log.WithField("error", err).Fatal("Unable to create Kubernetes Vulcand Router relay")
	}

	if err = relay.Start(); err != nil {
		log.WithField("error", err).Fatal("Unable to start Kubernetes Vulcand Router relay")
	}
	defer relay.Stop()

	// select {}
	time.Sleep(10 * time.Second)
	defer relay.Stop()
	time.Sleep(10 * time.Second)

}
