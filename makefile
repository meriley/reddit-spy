.PHONY: start build lint docker docker-build docker-tag docker-push

VERSION := 2.0.8
REGISTRY := merileyjr

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
	docker tag reddit-spy:$(VERSION) $(REGISTRY)/reddit-spy:$(VERSION)
	docker tag reddit-spy:$(VERSION) $(REGISTRY)/reddit-spy:latest

docker-push:
	docker push $(REGISTRY)/reddit-spy:$(VERSION)
	docker push $(REGISTRY)/reddit-spy:latest
