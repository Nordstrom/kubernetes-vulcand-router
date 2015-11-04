name := kubernetes-vulcand-router
container_name := $(name)
release := 0.0.2-dev

.PHONY: all build/container release/container deploy undeploy

all:
	
build/$(name)-linux-amd64: *.go Makefile
	GOOS=linux GOARCH=amd64 go build -o "$@" .

build/container: build/$(name)-linux-amd64
	docker build -t $(container_name) .

release/container: build/container
	docker tag -f $(container_name) nordstrom/$(container_name):$(release)
	docker push nordstrom/$(container_name):$(release)

deploy: deploy/service deploy/rc

deploy/service:
	kubectl create --validate -f svc.yaml

deploy/rc:
	kubectl create --validate -f rc.yaml

undeploy: undeploy/service undeploy/rc

undeploy/service:
	kubectl delete --validate -f svc.yaml

undeploy/rc:
	kubectl delete --validate -f rc.yaml
