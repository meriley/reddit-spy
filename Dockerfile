# Builder stage with specific Go version
FROM golang:1.20-alpine3.16 as builder
WORKDIR /app

# Leverage caching of modules
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG APP_VERSION=1.0.0
RUN CGO_ENABLED=0 go build -ldflags="-X main.version=$APP_VERSION" -o reddit-spy

# Runner stage
FROM alpine:latest
WORKDIR /app

# Add a non-root user and switch to it
RUN adduser -D user
USER user

COPY --from=builder /app/reddit-spy ./reddit-spy

ENTRYPOINT ["/app/reddit-spy"]
