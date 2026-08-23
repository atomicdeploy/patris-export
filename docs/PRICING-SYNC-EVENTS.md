# Pricing snapshot completion and change events

This document freezes the no-polling contract used by spreadsheet clients.
The routes are general pricing-integration routes; they are not specific to
Microsoft Excel.

## Authentication and ownership

All routes are loopback-only and require the existing companion session:

1. `POST /api/pricing-sync/session` with JSON `{}` and
   `X-Patris-Excel-Client: digitalogic-price-calculator`.
2. Keep the returned CSRF token only in process memory.
3. Send the client header and the token in the existing CSRF header on every
   snapshot, wait, payload, apply-status, cancel, and event request.

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

The companion obtains a snapshot from the protected remote bulk contract: one
revision read, one build start, one authenticated terminal-event wait when the
build is cold, and one immutable payload fetch. It does not assemble production
snapshots by paging the legacy remote `/state` route, and it does not poll a
remote build-status route. A terminal-event cursor is acknowledged only after
the generation-fenced local terminal hub retains the event for its waiter.

Remote revision and immutable-payload reads explicitly request the identity
representation. If an intermediary removes the payload `ETag` header entirely,
the companion sends exactly one conditional identity `GET` with the
authenticated terminal digest and accepts that exceptional path only for an
exact `304` with zero response bytes. A present empty, weak, malformed, or
mismatched validator fails closed; this verification is neither polling nor a
retry loop. All payload schema, source, revision, expiry, row, reconciliation,
and canonical digest checks still run before the local snapshot is committed.

`max_age_seconds: 0` without `expected_state_revision` deliberately requests a
new verification/build. Exact-revision reuse is allowed only when the requested
composite revision matches the cached snapshot and the authenticated upstream
event bridge still confirms that source, composite revision, and catalog
revision. Positive `max_age_seconds` remains the separate bounded-age policy.

The start document exposes both event URLs:

- `events_url: /api/pricing-sync/events` is the lasting semantic stream.
- `job_events_url: /api/pricing-sync/snapshots/{job_id}` is optional,
  job-scoped progress. Do not use it for lasting automatic refresh.

## Durable semantic event stream

Request:

```http
GET /api/pricing-sync/events HTTP/1.1
Accept: text/event-stream
X-Patris-Excel-Client: digitalogic-price-calculator
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
schema is `patris.pricing-state-event`. Internal one-second job heartbeats
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
- `pricing_apply_terminal`
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

## Apply completion without polling

`POST /api/pricing-sync/apply` is admission only. It may return an active
`202 patris.pricing-apply-job`, a replayed terminal `200`, or a terminal
scheduling failure `503`. The workbook records the request ID before the
single `POST`; an uncertain response is recovered by exactly one `GET` to
`/api/pricing-sync/jobs/{original_request_id}`. It never repeats the mutation.

The companion subscribes outbound to the authenticated WordPress
`digitalogic.pricing` WebSocket. The `pricing.apply.terminal` frame is accepted
only when its exact source, request/job identities, preview digest, terminal
status, result digest, and status path match the durable local admission. The
local ledger commits the frame before the remote cursor advances. Duplicate
frames are harmless by the stable terminal-event fingerprint.

A remote `completed` frame is not yet success for Excel. Patris first performs
the existing canonical WooCommerce delivery and exact pricing-state readback
under a stable delivery event identity. Only that terminal verified result
emits local `pricing_apply_terminal` with `verified: true` and the exact new
state revision. Excel then requests one fresh exact-revision snapshot. Failed,
cancelled, or `outcome_unknown` events never trigger a workbook refresh;
`outcome_unknown` retains the request and requires readback rather than another
mutation.

Exact `pricing.source.changed` and `pricing.source.removed` frames are also
synchronous lifecycle barriers. The companion validates the source schema,
projection, ID/dataset ownership, old/new revision relationship, audience,
revision route, and idempotency key; synchronously invalidates the old local
generation; and only then accepts the frame cursor. It immediately
rematerializes the canonical source and starts a fresh outbound subscriber at
that cursor. The replacement connection must pass the authenticated composite
revision validation before later event cursors can advance. A removed remote
source therefore remains fail-closed until that same validation succeeds or a
later local source transition starts another generation.

WordPress may return `pricing.stream.reset` with reason `invalid_event` when a
retained event predates the Living stream contract. After validating the reset
schema, retained window, revision route, and supplied cursor, the companion
performs one authenticated conditional revision validation, replaces its
process-memory cursor, and reconnects. It neither treats the retired frame as a
successful state change nor replays it indefinitely within that process.

The bridge's outbound WordPress `Last-Event-ID` cursor is process-memory state,
not a disk-durable checkpoint. After a full companion restart it begins at zero
and the subscriber sends that zero explicitly; omitting the header would mean a
new tail-only subscription and could skip retained terminal events. Retained source
transitions are therefore validated from their intrinsic old/new relationship,
accepted idempotently, rematerialized, and followed by authenticated conditional
revision validation. A retained `invalid_event` reset can repeat once after a
process restart, but it again advances the in-process cursor before reconnect.
Apply terminal effects use a separate disk-backed event ledger: replaying an
already completed terminal event from cursor zero returns duplicate acceptance
and does not restart finalization or canonical delivery.

There is no timer-driven apply-status loop. A single status reconciliation is
allowed after a lost admission response and on each authenticated stream
connect/reconnect. Cancellation may perform one reconciliation before one
`DELETE`. Normal operation is event-driven.

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
`digitalogic.pricing` WordPress WebSocket consumer, conditional composite
revision validation, and the `pricing.snapshot.build.terminal` callback used by
the remote bulk waiter. It also validates `pricing.apply.terminal` and advances
the cursor only after the terminal event is durably accepted by the local
apply ledger. The server wiring is active only for a valid protected
remote configuration. This source contract does not prove that the matching
WordPress terminal-event publisher is deployed or enabled; that external
activation must be independently verified before claiming cold remote snapshot
completion or WooCommerce-origin automatic refresh end to end.
