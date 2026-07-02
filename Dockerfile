FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o gateway ./cmd/gateway

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/gateway .
VOLUME /app/data
EXPOSE 18080
ENTRYPOINT ["./gateway", "-config", "/app/gateway.yaml"]
