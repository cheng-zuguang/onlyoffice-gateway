.PHONY: build test run clean frontend-build frontend-dev dev init-secrets test-init-secrets

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./... -count=1 -timeout 120s

init-secrets:
	@./scripts/init-secrets.sh

test-init-secrets:
	@sh ./scripts/init-secrets-test.sh

run:
	@if [ ! -f .env ] && [ -f .env.example ]; then \
		echo "NOTE: .env not found — creating from .env.example"; \
		cp .env.example .env; \
		echo "  → Run 'make init-secrets', then set ADMIN_PASSWORD."; \
	fi
	go run ./cmd/gateway

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
		echo "  → Run 'make init-secrets', then set ADMIN_PASSWORD."; \
	fi
	@set -e; \
		echo "Starting gateway on :18080..."; \
		$(MAKE) run & gateway_pid=$$!; \
		trap 'kill $$gateway_pid 2>/dev/null || true' EXIT INT TERM; \
		sleep 2; \
		if ! kill -0 $$gateway_pid 2>/dev/null; then \
			wait $$gateway_pid || true; \
			echo "Gateway failed to start; check that :18080 is available and .env is valid." >&2; \
			exit 1; \
		fi; \
		echo "Starting admin UI on :5173..."; \
		$(MAKE) frontend-dev

# ── SDK ───────────────────────────────────────────────────────────────────────

sync-version:
	node frontend-sdk/sync-version.mjs

frontend-test:
	cd frontend-sdk && npm test
