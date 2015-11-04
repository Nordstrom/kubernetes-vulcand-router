# Kubernetes Vulcand Router

## Introduction

This tool watches the Kubernetes API server for Service (de)registration. 
New Services are registered to Vulcan by calls to the admin API. 
Deleted Services are deleted from Vulcan as well by calls to the admin API.
Services will be registered when they contain the label `vulcand.io/routed=true`
The route expression that Vulcand will use to direct traffic to the Service
will be read from the Service's annotations, specifically from the annotation
with the key `vulcand.io/route-expression`.

## Running

Run as Docker container as follows:

```
docker run -d nordstrom/kubernetes-vulcand-router:0.1.0 -apiserver "http://kube-apiserver:8080" -vulcand "http://127.0.0.1:8182"
```

## Credits

This began as a fork of [amdatu-vulcanized](https://bitbucket.org/amdatulabs/amdatu-vulcanized).
It has since been rewritten, with significant guidance and inspiration from 
[kube2sky](https://github.com/kubernetes/kubernetes/tree/master/cluster/addons/dns/kube2sky).
Also, @kelseyhightower provided notable help along the way.
