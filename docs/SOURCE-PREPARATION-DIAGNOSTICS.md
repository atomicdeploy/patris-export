# Source preparation and dispatch diagnostics

Background delivery errors after an attempted request are reported as `delivery_outcome_unknown` when the transport fails, a successful response cannot be read, or the receiver returns HTTP 408 or 5xx. The receiver may already have written prices. The terminal diagnostic does not advertise a safe retry. This classification does not change the dispatcher's configured retry policy and does not establish the destination's final state; inspect destination receipts before deciding on another operation. A local receiver test confirms that a request actually received before timeout produces an unknown terminal outcome, releases the busy state, and does not expose a retryable hint. The code is deployed; deliberate timeout behavior has only been exercised locally.

## Source delta empty lists

Source 01a38e1 fixes empty excluded/quarantined code lists in change envelopes: copying onto a nil slice encoded `null`, which the PHP receiver correctly rejected with HTTP 422. The full live envelope passed the production read-only validator, while a delta built by the actual Go function failed on `quarantined_codes`; the corrected delta passed. The original rejected request body was not retained, so this is reproduction of a matching concrete defect rather than a byte-for-byte replay.

The installed binary (2.0.2-source-delta-fix, SHA256 9646C1D9EA0041FAFB63B8A091775E01346BFF28FAA6AB41454FB025769252FD) completed startup delivery in 15313ms: source preparation 6223ms, permit wait 0ms, dispatch 9089ms (individual millisecond rounding differs by 1ms from total). HTTP 200 accepted, one attempt, complete receipt, zero pending/deferred products. Apache, PHP-FPM and WordPress WebSocket services were active. This is startup delivery acceptance, not an all-product changed-CNY latency benchmark or proof of a subsequent natural delta delivery.

Background source/startup delivery includes the elapsed interval before dispatch scheduling in `pricing_operation.stage_ms.source_prepare`. Permit wait and dispatch remain separate; timing begins at preparation, but a queued operation cannot replace an active owner's diagnostic before acquiring the shared permit. Preparation includes record projection and pre-dispatch event construction; it is not solely database-read time.

Event-driven source delivery additionally records the first retained operating-system observation without changing the public webhook event's `timestamp` (which remains its materialization time):

| Field | Meaning |
| --- | --- |
| `source_observed_at` | Earliest source signal retained across debounce, transient preparation retry, and unacknowledged coalescing |
| `source_prepared_at` | Start of the canonical record/materialization attempt that produced the dispatched event |
| `source_queued_at` | Time the durable delivery was queued for the shared permit/outbox |
| `source_revision` | Exact canonical source revision selected for this dispatch |
| `event_id` | Exact canonical event selected for this dispatch |
| `stage_ms.observation_wait` | `source_observed_at` to `source_prepared_at`; it includes debounce/retry delay and is not receiver time |

The three timestamps are clamped to `source_observed_at <= source_prepared_at <= source_queued_at`. Source-observation metadata is deliberately process-local so a rollback binary can still open the v1 outbox; delivery resumed after a process restart falls back to preparation time. These fields are diagnostics for the latest operation, not a durable acceptance record; the authenticated receiver receipt remains authoritative. In-process coalesced pending snapshots keep the newest full snapshot and the earliest still-unacknowledged observation.

`GET /api/status` also reports `source_delivery_watch.required` and `.active`. Canonical/authenticated `send_updates` configurations require an active local watcher; CLI and embedded startup fail closed when such delivery is configured with watching disabled, and runtime API config replacement is rejected until a watcher is active.

The local viewer's `database.raw` preference does not change the authenticated
receiver contract. In raw viewer mode, source events still build a fresh
non-raw canonical full snapshot for durable delivery, while the WebSocket/viewer
baseline remains raw. Disabling canonical identity hashes is incompatible with
this durable lane and is rejected before startup/config commit; hiding hashes
from ordinary outputs remains supported. Enabling the lane, or changing its
destination/canonical transformation identity at runtime, queues a forced full
catch-up so the receiver is not left waiting for a later file mutation.

The durable receipt outbox is selected only for one authenticated JSON `POST`
destination whose URL is HTTPS (plain HTTP is accepted only on loopback), has
no user-info/query/fragment material, and has no parallel command transport.
Generic contract webhooks are not forced into this receiver-specific protocol.
An invalid authenticated lane is rejected before a watcher baseline can
advance. A new or post-restart destination begins with a full snapshot because
the process cannot otherwise prove the receiver's baseline.

External file reloads acquire the same runtime boundary as source callbacks
before committing the new effective config. RTL conversion and the character
map are also captured once per record projection. Consequently one snapshot
cannot mix old/new transformation policy even if the config file changes while
the source is being read.

`pricing_operation.last_pre_dispatch_failure` holds one timestamped historical failure when source preparation fails or permit waiting is cancelled. It contains operation, stage, a bounded code, start/end and elapsed time. It does not replace the active operation, imply receipt success, retain raw errors, or schedule retries. It is in-memory and lost on process restart; it is not a durable failure ledger. Consumers must distinguish this historical failure from the current operation and use its timestamps.

Breaking Go API change: `recordpipe.Build` and `BuildContext` return `(Result, error)`. Server and CLI callers propagate errors rather than consuming an empty successful result. All repository call sites were updated; no compatibility wrapper was introduced.

Production-first evidence: source a8c878acd6d699394575f4a67d3ba8ad4cf3f75d was installed as2.0.2-pre-dispatch-status, SHA256 FBCBF288F04A7EEB20FBA9E27868478A7F811109E3936E3DE1B92CE00E09E3AE. Startup completed with HTTP200/already_current, one attempt and zero pending/deferred:7623ms preparation,0ms permit wait,8374ms dispatch,total15997ms. The scheduled service/API and WordPress services remained available after startup. No deliberate production failure was injected.

Local checks cover actual failed startup preparation, cancelled queued delivery retaining an active owner, timing arithmetic, error propagation on cancellation, and record-building fixtures using the installed pxlib runtime. These do not prove timeout-with-unknown-delivery recovery across every interface, changed-price latency, or durable delivery of failure events. Those acceptance requirements remain in issue312.
