VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-X github.com/zenmind/onlyoffice-gateway/internal/version.Version=$(VERSION) -X github.com/zenmind/onlyoffice-gateway/internal/version.BuildTime=$(BUILD_TIME) -X github.com/zenmind/onlyoffice-gateway/internal/version.Commit=$(COMMIT)"

.PHONY: build test run clean version frontend-build frontend-dev dev

version:
	@echo $(VERSION)

build:
	go build $(LDFLAGS) -o bin/gateway ./cmd/gateway

test:
	go test ./... -count=1 -timeout 120s

run:
	@if [ ! -f .env ] && [ -f .env.example ]; then \
		echo "NOTE: .env not found — creating from .env.example"; \
		cp .env.example .env; \
		echo "  → Edit .env to set ADMIN_PASSWORD, JWT_SECRET, etc."; \
	fi
	go run -ldflags "-X github.com/zenmind/onlyoffice-gateway/internal/version.Version=$(VERSION)" ./cmd/gateway

clean:
	rm -rf bin/ data/

# ── Admin UI ──────────────────────────────────────────────────────────────────

frontend-install:
	cd admin-ui && npm install

frontend-build:
	cd admin-ui && npx vite build

frontend-dev:
	cd admin-ui && npx vite --host

# Quick start: build and run gateway + admin dev server
dev:
	@if [ ! -f .env ] && [ -f .env.example ]; then \
		echo "NOTE: .env not found — creating from .env.example"; \
		cp .env.example .env; \
		echo "  → Edit .env to set ADMIN_PASSWORD, JWT_SECRET, etc."; \
	fi
	@echo "Starting gateway on :18080..."
	@$(MAKE) run &
	@sleep 2
	@echo "Starting admin UI on :5173..."
	@$(MAKE) frontend-dev

# ── SDK ───────────────────────────────────────────────────────────────────────

sync-version:
	node frontend-sdk/sync-version.mjs

frontend-test:
	cd frontend-sdk && npm test
