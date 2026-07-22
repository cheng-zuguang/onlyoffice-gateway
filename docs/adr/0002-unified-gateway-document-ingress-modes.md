# ADR 0002: Unified Gateway with document-ingress modes

## Status

Accepted — 2026-07-15.

## Context

Business services that need ONLYOFFICE editing have different file topologies:

- Desktop or local-workspace applications create files on the user's machine or
  in a local project directory. These files do not have a stable public HTTPS
  URL, so the service must actively push bytes to Gateway before editing.
- Online services already serve project files from a domain or object store.
  They can expose a short-lived HTTPS URL for Document Server to read.
- Multiple business services may use the same Gateway, but each service owns
  its own document storage, lifecycle, branch/workspace semantics, and save
  conflict policy.

Splitting these cases into separate ONLYOFFICE architectures would duplicate
JWT handling, callback security, editor config generation, observability,
administrator service registration, and Document Server integration.

## Decision

Use one ONLYOFFICE Gateway and model the file topology as a per-document
document-ingress mode:

- **Hosted multipart upload**: the business service sends the file bytes to
  `POST /api/v1/documents` as multipart form data. Gateway stores the original
  and latest bytes temporarily and returns `document_id`.
- **Direct `source_url`**: the business service sends a signed request with a
  short-lived HTTPS `source_url`. Gateway stores only session metadata and lets
  Document Server read the source URL directly.

The mode is selected when the editing session is created. It must not change
the external editor embedding model: clients still open `/edit` with a signed
token, Gateway still builds the ONLYOFFICE config, Document Server still calls
Gateway `/callback`, and Gateway still notifies the business `webhook_url`.

Gateway must not become the system of record for every business document. In
hosted multipart mode, Gateway is temporary editing storage. In direct-source
mode, Gateway is metadata and protocol orchestration only. Final persistence
belongs to the originating business service.

## Consequences

- Business services share one Gateway deployment, service registry, audit model,
  callback protection, and editor SDK integration.
- Desktop/local-file clients use multipart upload because their files are not
  generally reachable by Document Server.
- Online services prefer `source_url` when they can provide a valid HTTPS URL.
- `source_url` must be readable by Document Server for the editing session and
  must not point at browser-only, local, or credential-in-URL resources.
- Webhook handlers stay business-specific. A service may write edited bytes to
  a local workspace, a Git branch, S3, MinIO, or any other storage it owns.
- Future ingress variants should extend the same Gateway API and session model
  instead of creating a second Gateway/Document Server architecture.
