# Kubernetes Vulcand Router

## Introduction

This tool watches the Kubernetes API server for Pod start & stop events.
New Pods are registered as `Servers` to Vulcan by writing to `etcd`. 
A deleted Pod is removed from Vulcan as well by removing its key in `etcd`.

Pods will be registered using the following key pattern in `etcd`:

```
/vulcan/backends/[pod namespace]-[pod label 'name']/servers/[pod IP]
```

Make sure your Vulcan backend/frontend configuration is configured to use backend 
servers based on the pod name.

## Running

Run as Docker container as follows:

```
docker run -d amdatu/amdatu-vulcanized -pods "ws://kube-apiserver:8080/api/v1/pods?watch=true" -etcd "vulcand-etcd:2379"
```

## Credits

This is a fork of [amdatu-vulcanized](https://bitbucket.org/amdatulabs/amdatu-vulcanized).
