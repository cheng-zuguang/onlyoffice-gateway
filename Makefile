VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-X github.com/zenmind/onlyoffice-gateway/internal/version.Version=$(VERSION) -X github.com/zenmind/onlyoffice-gateway/internal/version.BuildTime=$(BUILD_TIME) -X github.com/zenmind/onlyoffice-gateway/internal/version.Commit=$(COMMIT)"

.PHONY: build test run clean version

version:
	@echo $(VERSION)

build:
	go build $(LDFLAGS) -o bin/gateway ./cmd/gateway

test:
	go test ./... -count=1 -timeout 60s

run:
	go run -ldflags "-X github.com/zenmind/onlyoffice-gateway/internal/version.Version=$(VERSION)" ./cmd/gateway -config gateway.yaml

clean:
	rm -rf bin/ data/

sync-version:
	@V=$$(cat VERSION | sed 's/^v//'); \
	cd frontend-sdk && node -e "var p=require('./package.json');p.version='$$V';require('fs').writeFileSync('./package.json',JSON.stringify(p,null,2)+'\n')"
	@echo "Synced version $$(cat VERSION) to frontend-sdk/package.json"

sync-version:
	node frontend-sdk/sync-version.mjs

frontend-test:
	cd frontend-sdk && npm test
