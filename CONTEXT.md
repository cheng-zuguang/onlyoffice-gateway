# ONLYOFFICE Gateway context

## Purpose

ONLYOFFICE Gateway centralizes the protocol boundary between business services
and ONLYOFFICE Document Server. Business services authenticate with an RSA-signed
service JWT, upload or reference a document, render the supplied editor SDK, and
receive the edited result through a webhook.

## Core terms

- **Gateway**: the Go HTTP service that owns the public document API, Document
  Server callback endpoint, temporary storage, and administrator API.
- **Business service**: a registered external consumer of Gateway. Its RSA public
  key and webhook-domain allowlist are stored in the service registry.
- **Administrator**: the single operator authenticated to `/admin/api` with the
  Gateway HMAC JWT. There are no administrator roles.
- **Temporary attachment**: a Gateway document record, consisting of metadata and
  optionally Gateway-hosted original and edited bytes. It expires according to
  its TTL.
- **Direct-source attachment**: a temporary attachment created from `source_url`.
  Gateway stores only its metadata; its source and edited bytes remain outside
  Gateway storage.
- **Original / latest variant**: hosted attachment byte variants. `latest` is the
  edited result when present and otherwise the original.
- **Audit log**: a local, structured, daily-rotated record of Gateway access,
  asynchronous processing, and administrator activity. It is instance-local and
  must not contain tokens, request bodies, file contents, or full sensitive URLs.
- **Service registry**: the persisted list of business service identities,
  verification keys, and webhook-domain allowlists.

## Boundaries

- Gateway may delete its own temporary attachment metadata and hosted bytes. It
  must never delete the business-owned object addressed by `source_url`.
- The management UI consumes only `/admin/api`; it does not access storage or
  container logs directly.
- Document Server logs are outside the management API scope.
