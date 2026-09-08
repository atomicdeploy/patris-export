# Website pricing operations

Current prototype: PHP is the selected calculator on digitalogic.ir. Go reads Patris inputs and delivers them to the website. Optional snapshots are disabled; do not use snapshot availability as the website pricing completion criterion.

| Operation | Command | Input freshness |
| --- | --- | --- |
| Full refresh from the office deployment | `scripts\pricing\pricing-sync.cmd bulk --json` | Reads the configured Patris source and waits for website delivery receipt |
| Recalculate stored website inputs | `wp digitalogic currency update --recalculate` | Uses committed Patris inputs; no fresh office database fetch |
| Recalculate one product | `wp digitalogic pricing recalculate --product-code=113001002` | Uses committed Patris inputs; PHP authority required |
| Recalculate one product from the office CLI | `scripts\pricing\pricing-sync.cmd single 113001002 --json` | Direct authenticated HTTPS to WordPress; committed Patris inputs, PHP authority required |
| Read configured rates | `wp digitalogic currency get` | Reports configured values, not verified market freshness |

Run the first command from the installed office deployment directory. Run WordPress commands in the existing authorized server/WP-CLI context. Replace the example Product Code with the intended product. The REST equivalent for one product is POST `/wp-json/digitalogic/v1/pricing/products/recalculate` with JSON `{"product_code":"113001002"}` and the existing WordPress authentication/permissions.

The office `single` command requires a separately provisioned WooCommerce write key
in `DIGITALOGIC_PRICING_WRITE_KEY` and `DIGITALOGIC_PRICING_WRITE_SECRET` environment
variables. Never pass credentials as command arguments. After setting Windows User
environment variables, open a new terminal so the command inherits them. The CLI
does not reuse the Patris input-read bearer or provider credentials. The default
site is `https://digitalogic.ir`; `--site-url` accepts an HTTPS origin and redirects
are refused. This is a WordPress operation, not single-product Go source refresh.

Single receipts distinguish `already_current` from `reconciled_with_updates` and
report client `elapsed_ms` separately from `server_elapsed_ms`. Exit 2 means the
strict client target of less than 1000 ms was missed, even if `delivered:true`.
Exit 1 means no verified terminal receipt. Neither result triggers an automatic
retry. A fast unchanged receipt does not establish changed-price latency; the CLI
does not claim downstream rendered-price or independent notification acceptance.

Bulk JSON must report `delivered:true` with no pending/deferred products. A timeout or disconnected caller does not prove that the operation stopped: inspect `/api/status` and the current receipt before retrying. Snapshot endpoints intentionally report `snapshot_disabled`; PHP final-price consumer projection through Go is unavailable during this prototype.

Observed production results: full fixed-rate bulk14.058s; actual PHP CNY34000->34100 confirmation19.477s; single already-current in-process REST110ms; queued stored-input REST reconcile4s at server timestamp resolution. These are different scopes, not interchangeable performance claims. Single timing excludes WordPress startup/network and does not prove a changed-price write under1s.

Independent current readback classifies all1022 Patris source records:901 positive prices match,120 unavailable prices cleared,1 preserved. One public product's main price HTML changed814600->817000toman during the rate test; original34000rate restored. This is not all-page browser acceptance. Six unmapped product structures remain in digitalogic-wp#299.

Snapshot re-enablement requires redesign against current owner/authority and latency criteria, consumer-controlled configuration, and validation of both enabled and disabled modes. Current disablement is hard-coded. Deferred integration work and its justification are tracked in digitalogic-wp#296; overall plan is#288.

## Read operation timing and delivery outcome

Read the existing local `GET /api/status` response and its `pricing_operation`:

| Field | Meaning |
| --- | --- |
| `operation` | `manual_refresh`, `startup_delivery`, or `source_delivery` |
| `busy` | Shared pricing permit is held; false alone does not prove delivery |
| `active` | The recorded operation has not reached its terminal diagnostic |
| `elapsed_ms` | Recorded operation time; terminal values remain available |
| `stage_ms` | Accumulated `owner_inputs`, `canonical_input`, and `dispatch` durations as applicable |
| `dispatch_diagnostic.delivery` | Actual receiver receipt, never synthesized from requested input |
| `code` | Terminal classification; inspect this together with the receipt and pending/deferred counts |

For manual refresh, `owner_inputs` includes projection invalidation and owner
selection; `canonical_input` groups source reading, transformation and assignment
resolution. `dispatch` includes sending and waiting for the receiver response.
These are stage measurements, not individual database-query timings. Millisecond
rounding and control overhead can leave a small difference from total elapsed time.

Startup/source diagnostics start after acquiring the shared permit. They exclude
startup preparation and queued waiting. `receipt_received` means a matching complete
receiver receipt was observed, not that startup independently validated the current
pricing owner. `delivery_failed`, `delivery_receipt_unresolved`, `delivery_pending`
and `delivery_deferred` must not be displayed as successful price delivery.
The latest operation replaces the prior diagnostic; it is not a durable history.

Live measured manual example: 13.626 seconds at the CLI, 13.527 seconds on the
server: owner inputs 1.385 seconds, canonical input 4.559 seconds, dispatch 7.581
seconds. Receipt was already-current with no pending/deferred; subsequent readback
verified 901 positive leaf prices. This does not establish changed-rate latency or
all rendered product pages. Startup preparation errors and controlled timeout
acceptance remain tracked in issue #312.
