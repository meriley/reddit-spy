# Builder stage
FROM golang:1.20-alpine3.16 as builder
LABEL authors="meril"

WORKDIR /app
COPY . .
RUN go build

# Runner stage
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/reddit-spy ./reddit-spy

ENTRYPOINT ["/app/reddit-spy"]