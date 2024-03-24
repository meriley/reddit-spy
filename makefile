start:
	go run .

build:
	go build -o ./dist/reddit-spy

docker: docker-upgrade docker-compose docker-prune

docker-upgrade:
	docker pull mongo:latest

docker-compose:
	docker-compose up --force-recreate --build -d

docker-prune:
	docker image prune -f