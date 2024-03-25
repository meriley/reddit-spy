.PHONY: start build docker docker-build

ifeq ($(detected_OS),Windows)
    TIMESTAMP := $(shell powershell -Command "Get-Date -Format yyyy.MM.dd.HHmmss")
else
    TIMESTAMP := $(shell date +%Y.%m.%d.%H.%M.%S)
endif

start:
	go run .

build:
	go build -o ./dist/reddit-spy

docker: docker-build

docker-build:
	docker build --build-arg APP_VERSION=$(TIMESTAMP) -t reddit-spy:$(TIMESTAMP) .