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
