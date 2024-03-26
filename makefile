.PHONY: start build lint docker docker-build docker-tag docker-push

VERSION := 2.0.3
REGISTRY := 192.168.50.124:5000

start:
	go run .

lint:
	golangci-lint run --verbose

build:
	go build -o ./dist/reddit-spy

docker: docker-build docker-tag docker-push

docker-build:
	docker build --build-arg APP_VERSION=$(VERSION) -t reddit-spy:$(VERSION) .

docker-tag:
	docker tag reddit-spy:$(VERSION) 192.168.50.124:5000/reddit-spy:$(VERSION)
	docker tag 192.168.50.124:5000/reddit-spy:$(VERSION) 192.168.50.124:5000/reddit-spy:latest

docker-push:
	docker push 192.168.50.124:5000/reddit-spy:$(VERSION)
	docker push 192.168.50.124:5000/reddit-spy:latest
