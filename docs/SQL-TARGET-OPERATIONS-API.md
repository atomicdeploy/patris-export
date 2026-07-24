# SQL target operations API

The SQL target operations API exposes bounded, explicit operator actions while
keeping the MySQL/MariaDB DSN, custom-CA path, TLS server name, server version,
and raw driver errors inside the Patris process. It reuses the same
`recordpipe` canonical projection and `recordsink` transaction path as CLI and
watch exports.

The API never accepts connection details from the browser. Configure those
values through protected server configuration or environment variables as
described in [Export, transform, and send](examples/export-transform-send.md).

## Authorization

An operator first creates a short-lived in-memory session:

```http
POST /api/sql-target/session
Origin: http://127.0.0.1:18080
```

When the request peer and exact origin hostname are loopback (`localhost`,
`127.0.0.1`, or `::1`), the application can bootstrap without another
credential only if `PATRIS_EXPORT_SQL_OPERATOR_TOKEN` is unset and the request
contains no recognized proxy-forwarding marker. If the operator token is
configured, direct-loopback callers must send it too. The tokenless exception
never applies merely because a reverse proxy connects to Patris over loopback.

For every non-loopback origin:

- HTTPS is mandatory.
- Set a dedicated, random value of 32 to 512 characters in
  `PATRIS_EXPORT_SQL_OPERATOR_TOKEN`.
- Send that value once in `X-Patris-SQL-Operator-Token` when creating the
  session.

The edge-upload token, delivery credentials, and other Patris credentials
cannot authorize SQL operations. Never put the SQL operator token in a URL,
application config, browser storage, repository, or log.

Patris itself serves HTTP. When HTTPS terminates at a reverse proxy, preserve
the public `Host` or send singular `X-Forwarded-Host` and
`X-Forwarded-Proto: https` headers. Patris accepts these forwarding headers only
from a loopback proxy peer; an internet client cannot use them to impersonate a
secure origin. The proxy must preserve at least one recognized forwarding
marker and must require the dedicated operator token. Do not expose this API
through a proxy that strips all of `Forwarded`, `Via`, `X-Forwarded-*`, and
`X-Real-IP`: such a request is indistinguishable from a direct local client.

A successful session response sets an `HttpOnly`, `SameSite=Strict` cookie
scoped to `/api/sql-target` and returns:

```json
{
  "authenticated": true,
  "csrf_token": "opaque-short-lived-value",
  "expires_at": "2026-07-24T12:10:00Z"
}
```

The session expires after ten minutes and is bound to the exact scheme, host,
and effective port used during bootstrap. Every following request must carry
that cookie and exactly one `X-Patris-CSRF-Token` header. Session creation and
all protected POST operations also require exactly one matching `Origin`
header. Same-origin browsers do not consistently emit `Origin` on GET, so a
protected GET may instead supply the browser-generated
`Sec-Fetch-Site: same-origin` plus a `Referer` whose scheme, host, and effective
port match the session origin. Cross-site, missing, duplicated, or mismatched
evidence is rejected. Authentication failures use one generic response and do
not reveal which credential check failed.

Revoke the session when the operator locks or leaves the SQL controls:

```http
DELETE /api/sql-target/session
Origin: http://127.0.0.1:18080
X-Patris-CSRF-Token: opaque-short-lived-value
```

This deletes the hashed in-memory session and expires the browser cookie. A
successful response is `{"authenticated":false}`.

## Operations

All responses use `Cache-Control: no-store`. Connection tests, previews, and
manual syncs share one server-side operation permit so they cannot overlap.
`busy` remains readable while an operation is running. For bounded API calls,
the MySQL connector defaults or caps its connect, read, and write deadlines to
the remaining request deadline. This also bounds driver-level transaction
commit/rollback I/O that does not otherwise observe a Go context; unbounded
CLI/watch callers retain their configured DSN behavior.

### Status

```http
GET /api/sql-target/status
X-Patris-CSRF-Token: opaque-short-lived-value
```

```json
{
  "configured": true,
  "driver": "mysql",
  "table_configured": true,
  "batch_size": 250,
  "reconciliation": "upsert_only",
  "connect_timeout_ms": 10000,
  "verified_tls_configured": true,
  "busy": false,
  "last_result_available": false
}
```

The booleans report safe configuration readiness only. They do not return the
table, host, database, CA path, or certificate server name.
`table_configured=true` requires an explicit `convert.table` or
`export.mysql_table`; the SQLite table and inferred source names do not count.
The explicit table must already be in its normalized SQL form: 1 to 64 ASCII
characters, starting with a letter, ending with a letter or number, and
containing only letters, numbers, and underscores. An invalid name is never
silently rewritten and preview/sync returns `422 target_table_invalid` before
source or sink work.

### Non-mutating connection test

```http
POST /api/sql-target/test
X-Patris-CSRF-Token: opaque-short-lived-value
```

The bounded probe performs a ping, a server-vendor query, and a best-effort TLS
state query. It does not create a table or change data.

### Dry-run preview

```http
POST /api/sql-target/preview
X-Patris-CSRF-Token: opaque-short-lived-value
```

The preview reads the current source through `Server.RecordResultContext`,
preserving the active transform, canonical contract, field mapping, key, and
protected quarantine Codes. The same operation permit covers source preparation
and the sink, preventing an overlapping probe or sync from observing a stale
snapshot. Built-in downloads, temporary copies, JSON reads, projection, and
native record iteration cooperate with the request deadline. Temporary refreshes
are staged and atomically published so cancellation cannot replace a stable copy
with partial data. A single pxlib native call remains the smallest
non-interruptible unit. The preview calls the shared SQL sink with
`dry_run=true`; no schema or row mutation is committed.

Every successful, apply-eligible preview returns a random one-time authorization
inside the direct diagnostic:

```json
{
  "preview_grant": "43-character-unpadded-base64url-value",
  "preview_grant_expires_at": "2026-07-24T12:02:00Z"
}
```

The grant expires after at most two minutes and never extends the operator
session. It is held only as a hash in process memory and is bound to the exact
operator session, protected target configuration (including DSN, TLS, table,
mode, and batch), source configuration, and typed source projection. A new
preview replaces the previous grant. Every apply attempt consumes it before
validation, source, or sink outcomes, so retries, config/source changes, expiry,
and replay all require a fresh preview. Values with different SQL material
types, such as integer versus float or bytes versus text, receive different
source fingerprints.

For `soft_delete_missing`, a successful, apply-allowed preview adds bounded
reconciliation evidence:

```json
{
  "source_rows": 1002,
  "protected_rows": 3,
  "target_rows": 1005,
  "missing_rows": 3,
  "would_soft_delete": 3,
  "already_soft_deleted": 0,
  "would_restore": 0,
  "partial_source_risk": true,
  "confirmation_required": true,
  "apply_allowed": true,
  "confirmation_token": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
```

The token is an aggregate exact-plan digest. It never contains source values,
destination keys, or connection material. Keep it only in application memory,
discard it on any config or status change and after every apply attempt, and
request a new preview before retrying. An empty or safety-blocked source does
not receive a token.

### Explicit manual sync

```http
POST /api/sql-target/sync
Content-Type: application/json
X-Patris-CSRF-Token: opaque-short-lived-value

{
  "confirm": "manual_sync",
  "preview_grant": "43-character-unpadded-base64url-value"
}
```

For `soft_delete_missing`, include the exact token issued by the immediately
preceding preview:

```json
{
  "confirm": "manual_sync",
  "preview_grant": "43-character-unpadded-base64url-value",
  "reconciliation_token": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
```

The body is limited to 4 KiB and is parsed before taking the global SQL
operation permit, so a slow or partial request body cannot block probes or
previews. Unknown fields, additional JSON values, a missing content type, any
other confirmation string, malformed grants, whitespace-padded tokens,
uppercase hex, and malformed or oversized tokens fail before the sink runs.
Missing, stale, mismatched, expired, or replayed grants return a generic
`409 reconciliation_blocked` diagnostic with reason `preview_required`. The
shared sink then re-computes the soft-delete exact plan as an independent
second defense; a target-key or tombstone-state change returns
`409 reconciliation_blocked` with reason `preview_mismatch`.

`upsert_only` requires the one-time preview grant but does not accept a
reconciliation token. Browser-triggered `delete_missing` apply returns
`422 hard_delete_unavailable`: the existing hard-delete mode has no exact-plan
token contract and remains available only through an explicitly protected
server-side operator workflow. Its non-mutating preview remains available.

### Last in-process result

```http
GET /api/sql-target/last-result
X-Patris-CSRF-Token: opaque-short-lived-value
```

Before any operation it returns `{"available":false}`. Otherwise it returns the
last diagnostic held in memory. A process restart clears it. Preview
grants, grant expiry, and reconciliation confirmation tokens are returned only
by the direct preview response; cached diagnostics strip them, set
`apply_allowed=false`, and use `guard_code=preview_required`, so an apply always
requires a fresh preview.

Successful diagnostics contain the operation name, timestamps, optional source
record count, and either a probe or the shared SQL result:

```json
{
  "success": true,
  "diagnostic": {
    "operation": "preview",
    "status": "succeeded",
    "started_at": "2026-07-24T12:00:00Z",
    "finished_at": "2026-07-24T12:00:01Z",
    "record_count": 1002,
    "result": {
      "inserted": 3,
      "updated": 8,
      "unchanged": 991,
      "deleted": 0,
      "failed": 0,
      "elapsed_ms": 734,
      "dry_run": true,
      "reconciliation": "upsert_only"
    }
  }
}
```

Failures return a stable code, stage, retryable flag, generic message, and an
allowlisted reconciliation reason when applicable. Reconciliation evidence is
copied and normalized at the HTTP boundary; unknown guard codes and malformed
tokens are never forwarded. Failed or unconfirmed transactions report no
inserted, updated, unchanged, or deleted successes.
