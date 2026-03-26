FROM golang:1.22-alpine3.19 AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG APP_VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-X main.version=$APP_VERSION" -o reddit-spy

FROM alpine:3.19
WORKDIR /app

RUN adduser -D user
USER user

COPY --from=builder /app/reddit-spy ./reddit-spy

HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD pgrep reddit-spy || exit 1

ENTRYPOINT ["/app/reddit-spy"]
