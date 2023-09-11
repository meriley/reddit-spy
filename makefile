start: go run .

build: go build -o ./dist/reddit-spy

docker: docker-compose docker-prune

docker-compose: docker-compose up --force-recreate --build -d

docker-prune: docker image prune -f