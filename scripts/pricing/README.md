# Pricing sync command

Bulk and REST single require Node.js 18 or newer; persistent `session` requires
Node.js 22 or newer. Keep `pricing-sync.cmd`, `pricing-sync.cjs` and
`pricing-session.cjs` and `currency-owner.cjs` together.

The Windows ZIP and assisted installer include these files under `scripts/pricing`.
For a default per-user installation, run:

```powershell
& "$env:LOCALAPPDATA\Programs\Patris Export\scripts\pricing\pricing-sync.cmd" bulk
```

For an all-users or custom installation, use `scripts\pricing\pricing-sync.cmd`
under that installation directory. Node.js must be installed separately and on PATH.

```powershell
.\pricing-sync.cmd --help
.\pricing-sync.cmd bulk
.\pricing-sync.cmd bulk --base-url http://127.0.0.1:18080 --json
node --test pricing-sync.test.cjs
```

`bulk` invokes the existing Go service and changes downstream prices. The wrapper
does not start or deploy the service and has no separate queue or calculation engine.
Without arguments it shows help.

## Currency updates through the owner

Use the separate WooCommerce write credentials described below. Send only the
fields you intend to change; WordPress applies its currency-date policy and
dispatches the configured calculator. This command adds no calculation engine.

```cmd
pricing-sync.cmd currency --request-id office-cny-20260909-01 --cny 34500 --json
pricing-sync.cmd currency-status --request-id office-cny-20260909-01 --json
```

Choose a unique request ID for each intended operation. Optional `--usd`,
`--cny-date` and `--usd-date` set explicit fields. Omitted dates follow owner
policy; an unchanged rate preserves its existing date. The default observation
timeout is 180 seconds (`--timeout-ms` can change it); this is not a performance
acceptance threshold. After an uncertain result, use `currency-status` with the
same ID. The command never retries a write automatically.

Exit 0 requires the same owner job to be confirmed and a current currency
readback to match its desired values. `readiness_after` means this owner readback
succeeded, not that every downstream application has been independently tested.
This CLI does not change the workbook writeback implementation.

While observing, the command prints owner phases, operation identity and elapsed
time to stderr, with at most one unchanged-state heartbeat every five seconds.
With `--json`, stderr contains JSON events and stdout retains the final result.
Use `--quiet-progress` to disable these events. No additional owner requests are
made for display. Fixed owner phase percentages are deliberately not presented
as measured product completion. A confirmed job first shows
`verifying_owner_readback`; delivery is reported only after that read succeeds.
Product/destination counts and other interfaces remain in progress backlog #325
in digitalogic-wp.

Production acceptance on 9 September 2026: an unchanged CNY 34500 submission
confirmed as generation 64 in about four seconds; subsequent status-only recovery
matched the owner values. This verifies authenticated admission and recovery,
not changed-rate bulk latency.

## Single-product pricing

To read fresh Patris data and deliver one exact product through the configured
PHP or Go authority, use the existing local Go service:

```cmd
pricing-sync.cmd fresh 113001002 --json
```

This mode uses the local service session, not the WooCommerce credentials below.
It requires the updated Go endpoint and validates an exact scoped receipt. Missing,
quarantined or ambiguous products must not silently become bulk refreshes.
The current implementation reads the complete source to preserve source identity,
then delivers the selected row. If other unsent rows changed, the receiver can
reject the aggregate revision; inspect the result and run an explicit bulk refresh.
No automatic fallback to bulk or write retry occurs.

Production fresh-input acceptance on 2026-09-08 with the persistent service transport
took2507ms with an already-current receipt, including owner inputs21ms and dispatch2005ms.
Subsequent measured runs took2022–2431ms. This still fails the single-product speed
target and does not prove changed-Patris-row acceptance. Matched
server stage timings appear in `server_stage_ms` when the status event matches the
receipt; missing timings are not zero. The following modes instead recalculate
the already committed input:

An administrator can select the existing WordPress command service with
`canonical.pricing.digitalogic.command_websocket_url` in the managed Go config,
for example `wss://digitalogic.ir/wordpress-ws`. An empty value selects HTTP.
This does not change the configured final pricing engine. Both the existing
source-write secret and owner-read bearer token are required; endpoints must
have the same origin. Catalog reads and source delivery reuse the connection,
while assignment reads still use their existing HTTP endpoint. No automatic
HTTP fallback is made after a persistent write failure.

`fresh` and `bulk` use this service setting; the interactive `session` command
below is a separate user command for committed inputs. The service setting is
managed-file configuration, not editable in the current browser settings UI.

Environment overrides apply after the config file. In particular,
`PATRIS_EXPORT_SEND_INITIAL=true` overrides `send_updates.initial=false`.
The Windows scheduled launcher imports the User environment value. Check the
effective `/api/config` value before expecting a restart without source delivery.

Configure the dedicated WooCommerce write credentials in
`DIGITALOGIC_PRICING_WRITE_KEY` and `DIGITALOGIC_PRICING_WRITE_SECRET` in the
calling process environment. Never place secrets in command arguments. On Windows,
an already-open shell may need to reload newly configured user environment values.

```cmd
pricing-sync.cmd single 113001002 --json
pricing-sync.cmd session --json
```

`single` makes one REST request. `session` authenticates once using the existing
WordPress endpoint, opens the existing WebSocket service, and prints `session_ready`
with authentication/connection time. Enter one exact Patris product code per line;
enter `quit` or end input to close. Each line returns the same validated pricing
receipt. A failed or uncertain receipt closes the session without retrying the write.
Tokens stay in memory and are not printed or stored by this command.

Both modes recalculate committed Patris inputs through the shared PHP coordinator;
they do not fetch a fresh Patris row or implement Go-authority single pricing.
Each product operation targets less than 1000 ms; a successful but slower receipt
returns exit code 2. Session startup time is separate and is not hidden inside a
claim of sub-second cold-start performance. An already-current receipt does not
prove changed-price latency or visible-page propagation.

Live installed session acceptance on 2026-09-08: startup 1893 ms, product command
165 ms, zero writes/pending products. The REST path previously measured 1902 ms,
including 146 ms in the server coordinator. Timing outside the coordinator includes
transport and WordPress setup; it is not a network-only measurement.

## Bulk delivery

The sequence is GET `/api/status`, POST `/api/pricing-sync/session` with `{}`, then
authenticated POST `/api/refresh` with `{"delivery":"wait"}`, then GET `/api/status`.
The session token stays in memory and is never included in output. Mutation requests
are sent once. A timeout or malformed response is an unknown outcome, not proof that
prices were unchanged; inspect the existing server receipt before retrying manually.
The exact HTTP 429 `pricing_busy` refresh rejection is `not_started`: that request
did not read source data or dispatch a delivery. It is never retried automatically.
Wait for the active operation and inspect its receipt before an explicit retry.
GET `/api/status` exposes `pricing_operation.busy` for the shared pricing permit;
`pricing.phase` describes the owner worker and may remain `idle` during startup
delivery. A free permit alone does not prove that downstream delivery completed.

The JSON result includes elapsed wall time through the last HTTP readiness check,
phase timestamps, source revision, event identity, delivery status and deferred counts.
More than 60 seconds returns exit code 2 even if delivery succeeded. Exit 0 means
verified Go receiver acknowledgement with no deferrals and final HTTP readiness;
exit 1 means failure/unknown outcome; exit 3 means missing-product deferrals remain.
The default overall budget is 60 seconds, including readiness, session and final
readback. Each request receives only the remaining budget. If refresh exhausts it,
delivery remains unknown and the final readiness check is explicitly unverified;
the command does not exceed the budget to claim a successful readback.
An explicit `--timeout-ms 120000` can allow a longer overall wait. At 60 seconds it
emits a real critical-threshold event to stderr while still waiting, including in
`--json` mode. The eventual result still reports the performance failure.

HTTP readiness proves the status API responds. It is not a database-health or price
readback claim. The current Go response verifies receiver acknowledgement but does
not expose an independent destination readback or n8n notification receipt. Those
remain required before claiming the owner's complete downstream acceptance.

Source contract inspected: `pkg/server/excel_pricing.go` session/auth and
`excelPricingDeliveryComplete`; `pkg/server/server.go` refresh-wait response and
`/api/status`. See deployment acceptance evidence separately from automated checks.
