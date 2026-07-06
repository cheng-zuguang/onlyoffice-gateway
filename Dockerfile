FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
  -ldflags "-X github.com/zenmind/onlyoffice-gateway/internal/version.Version=${VERSION} -X github.com/zenmind/onlyoffice-gateway/internal/version.BuildTime=${BUILD_TIME} -X github.com/zenmind/onlyoffice-gateway/internal/version.Commit=${COMMIT}" \
  -o gateway ./cmd/gateway

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/gateway .
VOLUME /app/data
EXPOSE 18080
ENTRYPOINT ["./gateway", "-config", "/app/gateway.yaml"]
