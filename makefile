.PHONY: start build lint test vuln docker docker-build docker-tag docker-push

VERSION := 2.1.0
REGISTRY := gitea.cmtriley.com/mriley
IMAGE := $(REGISTRY)/reddit-spy
export GOTOOLCHAIN := local

start:
	go run .

lint:
	golangci-lint run --verbose

test:
	go test ./... -v -race

vuln:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

build:
	go build -ldflags="-X main.version=$(VERSION)" -o ./dist/reddit-spy

docker: docker-build docker-tag docker-push

docker-build:
	docker build --build-arg APP_VERSION=$(VERSION) -t reddit-spy:$(VERSION) .

docker-tag:
	docker tag reddit-spy:$(VERSION) $(IMAGE):$(VERSION)
	docker tag reddit-spy:$(VERSION) $(IMAGE):latest

docker-push:
	docker push $(IMAGE):$(VERSION)
	docker push $(IMAGE):latest
