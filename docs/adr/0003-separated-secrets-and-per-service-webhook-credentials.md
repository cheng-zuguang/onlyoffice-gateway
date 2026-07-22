# ADR 0003: Separate trust domains and use per-service webhook credentials

## Status

Accepted — 2026-07-16.

## Context

Gateway previously used one shared secret for unrelated responsibilities:
ONLYOFFICE editor configuration, administrator sessions, callback capabilities,
and business webhook signatures. Giving that value to a business service would
also expose privileges belonging only to Gateway and Document Server, and one
service could forge another service's webhook.

Service registration, upload validation, callback processing, and webhook
delivery are separate modules, but they share the persisted service registry at
runtime. The credential lifecycle therefore needs one owner and narrow read
interfaces for data-path callers.

## Decision

Use four required, distinct deployment secrets:

- `DOCUMENT_SERVER_JWT_SECRET` is shared only with Document Server and signs
  ONLYOFFICE editor configuration.
- `GATEWAY_ADMIN_SESSION_SECRET` signs administrator session JWTs.
- `GATEWAY_CALLBACK_CAPABILITY_SECRET` creates document-scoped callback tokens.
- `WEBHOOK_SECRET_ENCRYPTION_KEY` is a Base64 encoding of exactly 32 bytes and
  encrypts persisted service webhook credentials with AES-256-GCM.

The legacy `JWT_SECRET` does not configure Gateway and is not a compatibility
fallback. Compose maps `DOCUMENT_SERVER_JWT_SECRET` to Document Server's own
`JWT_SECRET` variable.

Each registered business service receives an independent 32-byte Base64url
webhook credential. `internal/admin` owns generation, encryption, persistence,
and the `active` -> `pending` -> `previous` rotation state. Create and rotate
responses return plaintext once with `Cache-Control: no-store`; read and update
responses expose only credential status. Activating a pending credential keeps
the former active credential available for a ten-minute rollback, but Gateway
signs new deliveries only with the active credential.

Business webhook delivery uses Webhook v1. Gateway signs
`v1\n<service_id>\n<timestamp>\n<delivery_id>\n<raw_body>` and sends the service
ID, timestamp, stable delivery ID, and `v1=` signature in request headers. The
delivery ID remains stable across retries; timestamp and signature are renewed
for every attempt. Network errors, 408, 429, and 5xx are retryable. Other 4xx
responses are permanent failures.

A document session that requests a business webhook must resolve an active
credential for its service or fail with 422. Sessions without `webhook_url` do
not require a credential and do not cause an unsigned delivery.

## Consequences

- Compromise of one service webhook credential does not grant administrator,
  Document Server, callback, or other service privileges.
- `services.json` contains encrypted credential envelopes; the configured
  encryption key is required to load and use them.
- Operators must capture create/rotate plaintext immediately and coordinate
  rotate, business-service configuration, activate, and optional rollback.
- Logs, audit events, list/update APIs, URLs, and browser storage must not expose
  plaintext credentials or encrypted credential internals.
- Gateway and each business webhook verifier must implement the same canonical
  Webhook v1 bytes and retry-safe idempotency expectations.
