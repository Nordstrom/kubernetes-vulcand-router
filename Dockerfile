FROM nordstrom/baseimage-ubuntu:14.04.3
MAINTAINER Edison Platform Team "invcldtm@nordstrom.com"

COPY build/kubernetes-vulcand-router-linux-amd64 /kubernetes-vulcand-router

ENTRYPOINT ["/kubernetes-vulcand-router"]
