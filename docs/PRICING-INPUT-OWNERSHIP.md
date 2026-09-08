# Pricing input ownership

The coordinated candidate uses one explicit input mode per configured final engine:

| Final engine | Input mode | Responsibilities |
| --- | --- | --- |
| PHP | patris_inputs | Go reads Patris facts; PHP applies its current pricing and selected-warehouse policy. |
| Go | go_projection | Go obtains current owner inputs, calculates and submits the validated projection. |

The raw Patris revision must not change merely because website rates, shipping assignments or markup change. PHP output identity remains separate from input identity. Do not hide owner-dependent fields in a supposedly raw source hash, infer direct-sale permission from a price amount, or introduce a second PHP calculator.

This coordinated breaking change was deployed on2026-09-08. Existing senders without input_mode cannot be paired with the new receiver. Stop the current sender before installing both sides; its enabled initial full-source delivery establishes the new baseline without deleting receiver state. Mode switches require a complete validated source envelope. Consumer snapshot generation remains disabled and is separate from this full source delivery.

Deployment acceptance compared1022products before/after: zero differences in stored price, warehouse stock and destination identity. Initial full delivery completed in40584ms with no pending/deferred products. Fresh single execution subsequently took5242ms: source_read41, canonical_build118, owner_inputs1370, dispatch3577ms. Prior measured canonical build was4510ms and total9308ms. Services remained active. This is an already-current input test; fresh changed-product latency, owner-only revision invariance and live engine-switch acceptance remain open. The below1second goal is not met.

Do not remove stock policy to make raw-input transfer pass. Until explicit direct-sale policy is shared, PHP mode with that opt-in must fail explicitly instead of silently ignoring it. This limitation remains unfinished work.
