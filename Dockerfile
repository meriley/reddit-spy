# Builder stage
FROM golang:1.20-alpine3.16 as builder
LABEL authors="meril"

WORKDIR /app
COPY . .
RUN go build

# Runner stage
FROM gcr.io/distroless/static-debian11:nonroot
WORKDIR /app
COPY --from=builder /app/reddit-spy ./reddit-spy

EXPOSE 8080
ENTRYPOINT ["/app/reddit-spy"]
