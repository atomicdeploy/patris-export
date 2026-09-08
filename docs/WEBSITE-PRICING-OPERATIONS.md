# Website pricing operations

Current prototype: PHP is the selected calculator on digitalogic.ir. Go reads Patris inputs and delivers them to the website. Optional snapshots are disabled; do not use snapshot availability as the website pricing completion criterion.

| Operation | Command | Input freshness |
| --- | --- | --- |
| Full refresh from the office deployment | `scripts\pricing\pricing-sync.cmd bulk --json` | Reads the configured Patris source and waits for website delivery receipt |
| Recalculate stored website inputs | `wp digitalogic currency update --recalculate` | Uses committed Patris inputs; no fresh office database fetch |
| Recalculate one product | `wp digitalogic pricing recalculate --product-code=113001002` | Uses committed Patris inputs; PHP authority required |
| Read configured rates | `wp digitalogic currency get` | Reports configured values, not verified market freshness |

Run the first command from the installed office deployment directory. Run WordPress commands in the existing authorized server/WP-CLI context. Replace the example Product Code with the intended product. The REST equivalent for one product is POST `/wp-json/digitalogic/v1/pricing/products/recalculate` with JSON `{"product_code":"113001002"}` and the existing WordPress authentication/permissions.

Bulk JSON must report `delivered:true` with no pending/deferred products. A timeout or disconnected caller does not prove that the operation stopped: inspect `/api/status` and the current receipt before retrying. Snapshot endpoints intentionally report `snapshot_disabled`; PHP final-price consumer projection through Go is unavailable during this prototype.

Observed production results: full fixed-rate bulk14.058s; actual PHP CNY34000->34100 confirmation19.477s; single already-current in-process REST110ms; queued stored-input REST reconcile4s at server timestamp resolution. These are different scopes, not interchangeable performance claims. Single timing excludes WordPress startup/network and does not prove a changed-price write under1s.

Independent current readback classifies all1022 Patris source records:901 positive prices match,120 unavailable prices cleared,1 preserved. One public product's main price HTML changed814600->817000toman during the rate test; original34000rate restored. This is not all-page browser acceptance. Six unmapped product structures remain in digitalogic-wp#299.

Snapshot re-enablement requires redesign against current owner/authority and latency criteria, consumer-controlled configuration, and validation of both enabled and disabled modes. Current disablement is hard-coded. Deferred integration work and its justification are tracked in digitalogic-wp#296; overall plan is#288.
