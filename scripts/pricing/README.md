# Pricing sync command

Requires Node.js 18 or newer. Keep `pricing-sync.cmd` beside `pricing-sync.cjs`.

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
Without arguments it shows help. Single-product refresh is unsupported until the
server provides an actual product scope.

The sequence is GET `/api/status`, POST `/api/pricing-sync/session` with `{}`, then
authenticated POST `/api/refresh` with `{"delivery":"wait"}`, then GET `/api/status`.
The session token stays in memory and is never included in output. Mutation requests
are sent once. A timeout or malformed response is an unknown outcome, not proof that
prices were unchanged; inspect the existing server receipt before retrying manually.

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
`/api/status`. No live refresh was invoked during implementation or tests.
