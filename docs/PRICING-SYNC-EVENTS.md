# Pricing snapshot completion and change events

This document freezes the no-polling contract used by spreadsheet clients.
The routes are general pricing-integration routes; they are not specific to
Microsoft Excel.

## Authentication and ownership

All routes are loopback-only and require the existing companion session:

1. `POST /api/pricing-sync/session` with JSON `{}` and
   `X-Patris-Excel-Client: digitalogic-price-calculator/v1`.
2. Keep the returned CSRF token only in process memory.
3. Send the client header and the token in the existing CSRF header on every
   snapshot, wait, payload, cancel, and event request.

The workbook never stores the WordPress machine credential. The companion
resolves that credential server-side from its configured environment variable.

## Initial import without polling

1. `POST /api/pricing-sync/snapshots` starts or joins an immutable build.
   A new build returns `202`; a bounded, explicitly reusable ready build may
   return `200`; a conflicting operation returns `429` immediately with
   `Retry-After: 1` and `retry_after_ms: 1000`.
2. Read `wait_url` from the response and issue exactly one asynchronous `GET`.
   The URL is `/api/pricing-sync/snapshots/{job_id}?wait=terminal`.
3. The wait response completes only with a terminal job document. It does not
   return an intermediate `running` document. Disconnecting the wait request
   cancels its still-running owned job.
4. When the job is `ready`, fetch `payload_url` once. Hash the raw response
   bytes, validate the strong `ETag`, source/state revisions, integrity counts
   and digests, then stage the complete result.
5. Commit Settings, Products, SyncData, formulas, and the generation marker in
   one bounded client-side transaction. On any error, cancellation, mismatch,
   duplicate key, unsafe warning, or stale event, retain the previous committed
   workbook generation byte-for-byte.

The start document exposes both event URLs:

- `events_url: /api/pricing-sync/events` is the lasting semantic stream.
- `job_events_url: /api/pricing-sync/snapshots/{job_id}` is optional,
  job-scoped progress. Do not use it for lasting automatic refresh.

## Durable semantic event stream

Request:

```http
GET /api/pricing-sync/events HTTP/1.1
Accept: text/event-stream
X-Patris-Excel-Client: digitalogic-price-calculator/v1
X-Patris-Excel-CSRF-Token: <in-memory session token>
Last-Event-ID: <optional unsigned decimal cursor>
```

Successful response:

```http
HTTP/1.1 200 OK
Content-Type: text/event-stream; charset=utf-8
Cache-Control: no-cache, no-store, must-revalidate
Connection: keep-alive
X-Accel-Buffering: no

retry: 1000
```

Every data frame has one unique, strictly increasing numeric `id`. The event
schema is `patris.pricing-state-event/v1`. Internal one-second job heartbeats
do not create data frames; idle connections receive only a comment keepalive
every 15 seconds.

`Last-Event-ID` must be a single unsigned decimal value. A malformed cursor
returns `400 invalid_event_cursor`. A cursor older than retained history or
ahead of the server produces a `replay_required` data event with reason
`cursor_expired` or `cursor_ahead`. With no cursor, the latest retained semantic
state is delivered; if none exists, `replay_required` has reason
`initial_state_unavailable`.

Semantic event kinds include:

- `snapshot_ready`
- `source_changed`
- `catalog_changed`
- `pricing_state_changed`
- `pricing_state_invalidated`
- `replay_required`

An event with `stale: true` invalidates any staged-but-uncommitted import. The
client must cancel that work and start a new snapshot. Current verified fields
and `previous_*`/`invalidated_identity` are distinct; historical validators
must never be treated as the current identity.

The durable stream is independent of snapshot job retention and disconnecting
it never cancels a job. It closes when its local companion session expires or
the server shuts down. The client then creates a fresh session and reconnects
with the last fully processed event ID; it does not issue recurring status
requests.

## Payload caching and terminal errors

The immutable payload returns `200` with a strong representation `ETag`.
`If-None-Match` may return `304` only while the exact validated in-memory body
is still available to the client. Expired or state-invalidated payloads return
`410`; a client must rebuild and must not import a cached body under a different
source/state identity.

`DELETE /api/pricing-sync/snapshots/{job_id}` begins cancellation and may return
`202 cancelling`; the terminal wait/event subsequently reports `cancelled`.

## Upstream WordPress delivery status

`pkg/server/excel_pricing_events_client.go` implements the authenticated
`digitalogic.pricing.v1` WordPress WebSocket consumer and conditional composite
revision validation. Its server lifecycle/opt-in must remain disabled until the
matching WordPress event endpoint is released. At the time this contract was
written, that endpoint existed only in a held local WordPress draft and was not
deployed; therefore WooCommerce-origin automatic refresh is not yet a live
end-to-end capability.
