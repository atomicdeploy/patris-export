# Owner currency writeback — implementation checkpoint

Currency-only Go queue jobs now observe the caller RequestID, submit only after an exact owner request-not-found response, and use the owner currency endpoint. They forward only explicit currency fields and the original expected revision. Owner confirmation is independently checked against canonical settings; returned settings include the owner-selected currency date. No currency preview/apply/ACK transaction is created.

Uncertain outcomes use `observation_required`, not `failed`: an uncertain delivery does not authorize a client to restore an old value. Writes are not automatically retried. A new local queue can observe an already-admitted stable caller request without POST.

Verified: TLS transport tests; queue confirmation without ACK, uncertain POST exactly once, and existing-owner recovery without POST; existing writeback checks retained for noncurrency/mixed behavior. The new Go transport also read actual production generation66 and current currency successfully. This is not yet installed-service writeback acceptance.

Before promotion:
- Update consumers to handle observation_required without restoring values or forgetting the stable pending intent.
- Apply owner-confirmed currency dates for single edits as well as batches; preserve newer local edits.
- Verify dedicated write credentials are available to the service process, not merely the invoking shell.
- Resolve mixed currency/noncurrency edits through shared owner semantics; their existing preview/apply path is still partial work, not the intended final architecture.
- Verify real installed-service admission, readback and restart/recovery before PR/merge. No claim of durable local journaling or native Excel acceptance is made.

The existing production Go service remains unchanged and ready. Product snapshots, rollback features and broad compatibility paths are not prerequisites for this operational identity mechanism.

Consumer/recovery checkpoint:
- Explicit POST /api/pricing-sync/writebacks/{job_id}/observe requeues owner observation only; even request-not-found during this recovery cannot submit currency.
- VBA observation_required preserves active value/request identity and pauses without RestoreWritebackValueFromTerminal or CompletePricingWriteback. SyncPricingSettingsNow resumes that local job through /observe instead of enqueueing another mutation.
- Single and batch confirmations consume owner currency dates, preserving newer date proposals. Single-confirmation errors propagate instead of silently becoming successful completion.
- Queue checks now cover uncertain submission followed by explicit recovery to confirmed with only one currency POST. Existing writeback and relevant VBA source checks pass.
- Native VBA execution, recovery after workbook/service restart, local session expiry, full mixed-setting unification and service credential inheritance remain open. Paused intent here is retained in the active session; this is not a claim of a complete disk-backed journal.

Live owner readback correction:
The first real execution failed because currency confirmation still called the old pricing state projection, whose schema differed from production. Currency REST now exposes canonical settings alongside the same state_revision; Go verifies those owner settings directly through its currency client. This removes the old state-contract/product-source credential dependency from currency confirmation.
The explicit read-only live queue probe then passed against production generation66: CNY34500,date2026-09-08, confirmation/readback2640ms (whole probe4.02s). No admission or price mutation was permitted. Transport and writeback tests passed. Installed-service writeback and native Excel remain unverified; the production Go binary is still unchanged.

Actual Go queue admission checkpoint (2026-09-09):
The explicit live probe passed through enqueue -> next -> processRemote -> finish -> get, then independently observed the owner request. It submitted the unchanged current CNY34500 using request go-owner-unchanged-20260909-01. Owner job4eb7068aaeded8a6de57546af443e490,generation67 confirmed; all canonical settings exactly matched before/after; no transaction ID or ACK deadline was created. Queue plus independent owner observation took5458ms (whole probe6.83s). This is actual authenticated admission from current Go code, not installed-service or changed-price bulk latency. No retry was needed or issued.

Session recovery checkpoint:
The existing SyncPricingSettingsNow action now renews the local session before /observe and retains the original request/job identity on renewal failure. It cannot fall through to currency enqueue after receiving the new session token. Explicit HTTP-route checks passed: expired token ->403, currency fields in observation body ->400, new token + empty observation ->202 -> confirmed, with total owner POST count still1. Relevant VBA source checks passed; no native workbook execution or installation is claimed.

Durable intent prototype checkpoint:
Currency-only queue admission now writes an immutable intent beside the configuration in `.currency-intents` before queue visibility or remote submission. Records contain original request/job IDs, sequence, owner origin, settings and revision, never authentication credentials. Restart restores observation-only recovery; an owner 404 cannot trigger a new POST. Unresolved currency records no longer expire at the queue's 30-minute TTL. Validated terminal records alone are eligible for retention cleanup; corrupt storage blocks currency admission with HTTP503 while other settings retain their existing path.
Focused transport, queue and journal checks passed, including restart identity, zero-POST recovery, malformed storage and 256-record capacity. This is source-level prototype evidence, not installed-service durability acceptance. Capacity recovery and terminal-record lifecycle still require operational review before deployment. Workbook restart identity, mixed-setting unification and service credential inheritance remain open. This small intent record is not a product snapshot or the deferred revision/rollback subsystem.
