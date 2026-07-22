# ONLYOFFICE Gateway context

## Purpose

ONLYOFFICE Gateway centralizes the protocol boundary between business services
and ONLYOFFICE Document Server. Business services authenticate with an RSA-signed
service JWT, upload or reference a document, render the supplied editor SDK, and
receive the edited result through a webhook.

Gateway is the single ONLYOFFICE integration boundary. Different business file
topologies are represented as document-ingress modes, not as separate
Gateway/Document Server architectures.

## Core terms

- **Gateway**: the Go HTTP service that owns the public document API, Document
  Server callback endpoint, temporary storage, and administrator API.
- **Business service**: a registered external consumer of Gateway. Its RSA public
  key and webhook-domain allowlist are stored in the service registry.
- **Document-ingress mode**: the way a business service creates an editing
  session. Gateway currently supports hosted multipart upload and direct
  `source_url`.
- **Hosted multipart attachment**: a temporary attachment whose original bytes
  are uploaded to Gateway. This is the default for desktop apps and local
  workspace services that do not have a public HTTPS document URL.
- **Direct-source attachment**: a temporary attachment created from `source_url`.
  Gateway stores only its metadata; its source and edited bytes remain outside
  Gateway storage. This is the default for online services that can expose a
  short-lived HTTPS document URL.
- **Administrator**: the single operator authenticated to `/admin/api` with the
  Gateway HMAC JWT. There are no administrator roles.
- **Temporary attachment**: a Gateway document record, consisting of metadata and
  optionally Gateway-hosted original and edited bytes. It expires according to
  its TTL.
- **Original / latest variant**: hosted attachment byte variants. `latest` is the
  edited result when present and otherwise the original.
- **Audit log**: a local, structured, daily-rotated record of Gateway access,
  asynchronous processing, and administrator activity. It is instance-local and
  must not contain tokens, request bodies, file contents, or full sensitive URLs.
- **Service registry**: the persisted list of business service identities,
  verification keys, webhook-domain allowlists, and encrypted webhook
  credential state.
- **Service webhook credential**: a per-business-service HMAC secret shared
  only by Gateway and that business service. It has `active`, `pending`, and
  short-lived `previous` slots for two-phase rotation and rollback.
- **Callback capability**: a document-scoped token in the Document Server
  callback URL. It authenticates Document Server callbacks to Gateway and is
  distinct from service webhook authentication.

## Module and caller map

- `cmd/gateway` is the composition root. It validates the four independent
  Gateway secrets, opens the service registry with its encryption key, and
  passes the same registry to the administrator and document runtimes.
- `internal/admin` owns service identity CRUD and the service webhook
  credential lifecycle. It generates credentials, encrypts their persisted
  form with AES-256-GCM, exposes only safe service views, and returns plaintext
  only from create and rotate responses.
- `internal/jwt` consumes the registry's identity resolver to verify a business
  service's RS256 request token and obtain its webhook-domain allowlist.
- `internal/gateway` wires the public document routes. It gives
  `internal/handler` only the identity and active-credential lookup capabilities
  needed by upload validation and webhook delivery.
- `internal/handler/upload.go` creates hosted multipart or direct-source
  temporary attachments. A session with a `webhook_url` requires an active
  credential; a session without one may be used for preview or temporary
  editing.
- `internal/handler/editor.go` signs ONLYOFFICE editor configuration with the
  Document Server JWT secret and creates document-scoped callback capabilities
  with the callback capability secret.
- `internal/handler/callback.go` verifies callback capabilities, saves hosted
  results, and signs business webhook deliveries with the attachment owner's
  active service credential.
- `admin-ui` calls only `/admin/api`. Its service page shows credential state
  and keeps create/rotate plaintext only in the one-time dialog's React state.

The main call chain is: business service -> public document API -> temporary
attachment -> editor config -> Document Server callback -> business webhook.
Administrator calls form a separate control plane that mutates the service
registry consumed by that data path.

## Boundaries

- Gateway may delete its own temporary attachment metadata and hosted bytes. It
  must never delete the business-owned object addressed by `source_url`.
- Gateway must not require business services to centralize their document
  storage. Each service remains responsible for its own final save semantics.
- Do not introduce a second integration architecture for desktop/local files
  versus online files. Add or refine document-ingress modes behind the same
  Gateway API instead.
- The management UI consumes only `/admin/api`; it does not access storage or
  container logs directly.
- Document Server logs are outside the management API scope.
- The Document Server JWT secret, administrator session secret, callback
  capability secret, and webhook encryption key are separate trust domains and
  must be present and distinct. The legacy `JWT_SECRET` is not a fallback.
- A business service receives only its own webhook credential. Service list and
  update APIs, logs, and audit records must not expose credential plaintext,
  nonce, or ciphertext.
- Business webhooks use Webhook v1: the service ID, timestamp, stable delivery
  ID, and raw body are signed with the active per-service credential. Network
  errors, 408, 429, and 5xx are retryable; other 4xx responses are permanent.
