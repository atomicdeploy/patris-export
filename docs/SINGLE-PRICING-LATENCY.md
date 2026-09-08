# Single pricing latency

`pricing-sync.cmd session --json` now authenticates through the existing config endpoint once and accepts one product code per input line; `quit` or EOF closes the connection. Requires Node.js 22+ with native WebSocket. Credentials remain in the existing environment variables; the transient session token stays in memory and is not printed or saved. Cold authentication is reported separately. There is no reconnect/retry of writes after an uncertain result.

Production candidate acceptance: authentication/connection 1528 ms, subsequent commands 113/114 ms, both already-current, zero writes and pending products. This improves established-session operation, not cold-start or changed-price acceptance.

The installed `pricing-sync.cmd single PRODUCT_CODE --json` exposes response header and complete-body timings, plus elapsed time outside the server-reported coordinator. The latter includes WordPress bootstrap and transport; it is not a network-only metric. Timing fields contain no credentials or response payloads.

Production execution on 2026-09-08: total 1902 ms, response headers 1897 ms, coordinator 146 ms, outside coordinator 1756 ms. The receipt was already-current with zero writes and pending products. Exit classification remained target_missed. This is not changed-price latency acceptance.

The existing WordPress persistent dispatcher can invoke the same coordinator. A production probe on one Windows WSS connection completed commands after a global CNY change and restoration in 418/564 ms without redundant writes. Those measurements exclude prior token issuance and follow completed bulk pricing. They do not establish the installed REST CLI's sub-second performance.

The existing WebSocket user token is issued through `/wp-json/digitalogic/v1/websocket/config` for an authorized panel user and has a one-hour configured lifetime. The current CLI has separate WooCommerce write credentials, not a persistent user session. Integrating a reusable authorized session is required before treating the WSS probe as a user-facing command. Do not copy a server-wide token into the CLI or broaden the read-only Patris event principal. Include initial authentication in cold-start timing and label established-session timing separately. Do not retry a timed-out price write automatically.
