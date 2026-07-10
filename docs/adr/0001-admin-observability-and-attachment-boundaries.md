# ADR 0001: Admin observability and attachment boundaries

## Status

Accepted — 2026-07-10.

## Decision

The Gateway management API serves only Gateway-local, structured audit logs. It
does not expose Document Server container logs or depend on container stdout.
Audit logs rotate daily, retain 14 days by default, and are queryable only by the
single authenticated administrator.

Temporary attachment administration applies to both local and Gateway-owned S3
storage. Direct-source attachments expose metadata only: neither their original
nor their latest byte variant is downloaded or proxied through the management
API. TTL expiry runs for both supported storage backends and deletes only
Gateway-owned records and objects.

Destructive attachment administration requires an explicit confirmation. If an
audit record cannot be durably written, attachment deletion, TTL extension, and
manual cleanup are rejected; the business editing path remains available.

## Consequences

- Observability is instance-local; multi-instance log aggregation remains a
  separate deployment concern.
- The storage abstraction must support attachment enumeration in addition to
  existing per-document access.
- The Administrator API must use a stable, redacted metadata representation so
  it never exposes full `source_url` or `webhook_url` values.
