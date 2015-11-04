name := kubernetes-vulcand-router
container_name := $(name)
release := 0.0.1

.PHONY: all build/container release/container

all:
	
build/$(name)-linux-amd64: *.go Makefile
	GOOS=linux GOARCH=amd64 go build -o "$@" github.com/Nordstrom/kubernetes-vulcand-router

build/container: build/$(name)-linux-amd64
	docker build -t $(container_name) .

release/container:
	docker tag -f $(container_name) nordstrom/$(container_name):$(release)
	docker push nordstrom/$(container_name):$(release)
