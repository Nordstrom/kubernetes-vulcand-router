Run as Docker container as follows:

```
docker run -d amdatu-registrator app -pods "ws://[kubernetes-server]/api/v1beta3/namespaces/default/pods?watch=true" -etcd "[etcd-address]"

```

For example:

```
docker run -ti --rm amdatu-registrator app -pods "ws://rti-kubernetes.amdatu.com:8080/api/v1beta3/namespaces/default/pods?watch=true" -etcd "http://10.100.103.4:2379"
```