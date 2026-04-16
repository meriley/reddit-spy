FROM golang:1.24-alpine3.21 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG APP_VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-X main.version=$APP_VERSION" -o reddit-spy

FROM alpine:3.21
WORKDIR /app

RUN adduser -D user
USER user

COPY --from=builder /app/reddit-spy ./reddit-spy

ENTRYPOINT ["/app/reddit-spy"]
