.PHONY: start build lint test vuln docker docker-build docker-tag docker-push

VERSION := 2.0.8
REGISTRY := merileyjr
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
	docker tag reddit-spy:$(VERSION) $(REGISTRY)/reddit-spy:$(VERSION)
	docker tag reddit-spy:$(VERSION) $(REGISTRY)/reddit-spy:latest

docker-push:
	docker push $(REGISTRY)/reddit-spy:$(VERSION)
	docker push $(REGISTRY)/reddit-spy:latest
