name := kubernetes-vulcand-router
container_name := $(name)
release := 0.1.3-dev

.PHONY: all build/container tag/container release/container deploy undeploy

all:
	
build/$(name)-linux-amd64: *.go Makefile
	GOOS=linux GOARCH=amd64 go build -o "$@" .

build/container: build/$(name)-linux-amd64
	docker build -t $(container_name) .

tag/container: build/container
	docker tag -f $(container_name) nordstrom/$(container_name):$(release)

release/container: tag/container
	docker push nordstrom/$(container_name):$(release)
