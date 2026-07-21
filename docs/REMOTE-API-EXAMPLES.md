# Remote Update Delivery

Patris Export has one native outbound pipeline: transformed records become a
current `patris.product-sync` JSON envelope, and the HTTP sink sends that
envelope to a receiver. REST and WordPress REST use this native path directly.
JSON-RPC, legacy WordPress AJAX, and gRPC use a thin adapter; they do not read
Patris or transform product data again.

## Support matrix

| Destination | Support | Patris configuration | Wire body |
| --- | --- | --- | --- |
| REST/webhook | Native | `send_updates.url` | Direct canonical envelope |
| WordPress REST | Native and recommended | `send_updates.url` | Direct canonical envelope |
| JSON-RPC 2.0 | Loopback adapter | Patris sends to adapter | JSON-RPC wrapper whose `params` is the unchanged envelope |
| WordPress `admin-ajax.php` | Loopback adapter, legacy only | Patris sends to adapter | Form fields containing the unchanged envelope |
| gRPC | HTTP/JSON transcoding gateway | Patris sends to adapter, adapter sends to gateway | Exact canonical JSON carried in an `envelope_json` string |
| RPC-style local command | Native command sink | `send_updates.command` | Direct envelope on stdin |

Patris does **not** natively speak JSON-RPC framing, WordPress form AJAX, or
HTTP/2 gRPC. The included Node.js adapter makes those boundaries explicit and
keeps the canonical pipeline replaceable. It requires Node.js 18 or newer and
uses only built-in modules.

## Contract and sparse-field rules

Use a canonical profile and `require_contract: true` for every product-sync
integration. Delivery never activates pricing and never synthesizes shipping
data. Pricing/shipping fields are eligible only when an integration provider is
active and supplied the value. A standalone run without an active provider
must not gain `shipping_method_id`, `shipping_price_per_kg`,
`shipping_price_per_kg_currency`, markup, FX, or calculated-price keys merely
because delivery is enabled. When present, the two shipping-price fields are a
required pair and currency is exactly `CNY` or `IRR`. The upstream canonical
pipeline converts CNY freight through the CNY-to-IRT rate and converts IRR to
IRT by dividing by 10; adapters preserve the resulting envelope unchanged.

Sparse payloads have two distinct states:

- A key that was never supplied, or whose source value is empty, is absent.
- A key explicitly supplied as JSON `null` is present with a `null` value.

The adapters validate identity fields, but forward the original JSON text
instead of serializing parsed JavaScript numbers. This preserves exact int64
and decimal tokens, explicit nulls, and absent fields. Automated adapter tests
cover all three wrappers. Upgrade older binaries that still emit the
legacy always-present pricing fields to a build containing the sparse export
work tracked by [issue #171](https://github.com/atomicdeploy/patris-export/issues/171)
before using these recipes in production.

## Secrets and request identity

Do not put secrets in a repository, URL, config value, or command line. Config
contains only an environment-variable **name**. Patris reads the named variable
at request time and sends it as `X-Patris-Product-Sync-Secret`.

For every canonical HTTP request Patris sets:

- `X-Patris-Contract`
- `X-Patris-Event-ID`
- `X-Patris-Event`
- `X-Patris-Source`

`event_id` is deterministic for a given source revision and event. Receivers
must use it as their idempotency key. Patris encodes the envelope once and
reuses the same bytes and identity headers for all attempts. The adapter also
sets `Idempotency-Key` on downstream HTTP requests.

Remote secret-bearing destinations require HTTPS. Plain HTTP is accepted only
for a loopback adapter or mock receiver. Redirects are not followed when the
product-sync secret is active.

The native sink provides the dedicated header secret above; it does not claim
generic HMAC request signing. If a receiver requires HMAC, sign the exact
canonical bytes in a small gateway and keep the signing key in that gateway's
secret store. Do not add a second transform or product schema to do so.

## Native REST and WordPress REST

Copy [send-updates-rest.json](examples/send-updates-rest.json), replace the
endpoint, and set the referenced secret in the process environment:

```powershell
$env:PATRIS_PRODUCT_SYNC_SECRET = 'replace-with-a-real-secret'
patris-export -c .\docs\examples\send-updates-rest.json convert C:\Patris\data4\kala.db -f json -w
```

The equivalent CLI form is:

```powershell
$env:PATRIS_PRODUCT_SYNC_SECRET = 'replace-with-a-real-secret'
patris-export convert C:\Patris\data4\kala.db -f json -w `
  --send-url https://receiver.example/wp-json/receiver/patris/product-sync `
  --send-format json `
  --send-mode changes `
  --send-initial `
  --send-product-sync-secret-env PATRIS_PRODUCT_SYNC_SECRET `
  --send-retry-attempts 3 `
  --send-retry-backoff 2s
```

The WordPress REST route is the preferred WordPress integration. It receives
the same direct body as any REST receiver; no WordPress-specific transport code
runs in Patris.

An accepted strict receiver response is:

```json
{
  "success": true,
  "data": {
    "status": "accepted",
    "event_id": "sha256:replace-with-request-event-id",
    "retryable": false,
    "pending_products": 0,
    "deferred_products": 0
  }
}
```

A temporary receiver failure should use HTTP `503` and may return:

```json
{
  "success": false,
  "code": "product_sync_busy",
  "details": {"retryable": true}
}
```

Patris retries network failures, HTTP 408/425/429, selected 5xx responses, and
valid receiver states that explicitly report pending retryable work. It does
not retry permanent 4xx rejection. Configure retries only for an idempotent
receiver.

## Web UI/config API equivalent

The current visual Settings dialog does not expose outbound-delivery secrets.
This is intentional: it must never collect or persist a secret value. It does
persist the same full application config through `/api/config`, so an operator
can update the non-secret `send_updates` block from the browser console while
the secret is injected into the Patris process environment:

```javascript
const config = await fetch('/api/config').then((response) => response.json());
config.send_updates = {
  enabled: true,
  url: 'https://receiver.example/wp-json/receiver/patris/product-sync',
  method: 'POST',
  format: 'json',
  mode: 'changes',
  initial: true,
  allow_raw: false,
  require_contract: true,
  timeout: '10s',
  retry_attempts: 3,
  retry_backoff: '2s',
  product_sync_secret_env: 'PATRIS_PRODUCT_SYNC_SECRET'
};
const response = await fetch('/api/config', {
  method: 'PUT',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify(config)
});
if (!response.ok) throw new Error(await response.text());
console.log(await response.json());
```

Always fetch, modify, and PUT the complete config as above. Sending only a
partial object can reset unrelated settings. The response and subsequent
`GET /api/config` expose only the environment-variable name, never its value.

## Loopback adapter

For non-native transports, start the adapter and point Patris at its fixed
loopback endpoint using [send-updates-adapter.json](examples/send-updates-adapter.json).
The adapter validates the canonical body and identity headers, forwards once,
and returns a strict success or retryable failure to Patris. Patris remains the
only retry owner.

Set a loopback ingress secret in both processes:

```powershell
$env:PATRIS_ADAPTER_INGRESS_SECRET = 'replace-with-a-local-secret'
```

Then start one adapter recipe below in terminal 1 and run this in terminal 2:

```powershell
$env:PATRIS_ADAPTER_INGRESS_SECRET = 'replace-with-the-same-local-secret'
patris-export -c .\docs\examples\send-updates-adapter.json convert C:\Patris\data4\kala.db -f json -w
```

Do not expose port 18081 beyond loopback. If the adapter must run on another
host, terminate TLS and mutual authentication in front of it and change the
Patris URL to HTTPS.

### JSON-RPC 2.0

```powershell
$env:PATRIS_ADAPTER_INGRESS_SECRET = 'replace-with-a-local-secret'
$env:REMOTE_API_SECRET = 'replace-with-the-remote-bearer-secret'
node .\scripts\examples\patris-delivery-adapter.cjs `
  --transport json-rpc `
  --target https://api.example/rpc `
  --method patris.productSync `
  --ingress-secret-env PATRIS_ADAPTER_INGRESS_SECRET `
  --target-secret-env REMOTE_API_SECRET
```

The adapter sends:

```json
{
  "jsonrpc": "2.0",
  "id": "sha256:replace-with-event-id",
  "method": "patris.productSync",
  "params": {
    "schema": "patris.product-sync",
    "event_type": "update",
    "event_id": "sha256:replace-with-event-id",
    "source": {"id": "patris-office"},
    "products": []
  }
}
```

The RPC server must return the same `id`:

```json
{"jsonrpc":"2.0","id":"sha256:replace-with-event-id","result":{"status":"accepted","event_id":"sha256:replace-with-event-id","retryable":false,"pending_products":0,"deferred_products":0}}
```

For a temporary method error, set `error.data.retryable` to `true`. The adapter
maps it to HTTP 503 so Patris applies its configured backoff and retry limit.

### WordPress AJAX

Prefer the native WordPress REST recipe. Use AJAX only for a legacy plugin that
cannot expose a REST route:

```powershell
$env:PATRIS_ADAPTER_INGRESS_SECRET = 'replace-with-a-local-secret'
$env:WORDPRESS_AJAX_SECRET = 'replace-with-the-remote-secret'
node .\scripts\examples\patris-delivery-adapter.cjs `
  --transport wordpress-ajax `
  --target https://wordpress.example/wp-admin/admin-ajax.php `
  --action patris_product_sync `
  --ingress-secret-env PATRIS_ADAPTER_INGRESS_SECRET `
  --target-secret-env WORDPRESS_AJAX_SECRET
```

The adapter submits these form fields:

- `action=patris_product_sync`
- `event_id=<canonical event_id>`
- `payload=<unchanged canonical JSON envelope>`

It sends the remote secret in `X-Patris-Product-Sync-Secret`, not a form
field. The WordPress action must authenticate that header, deduplicate by
`event_id`, and return the complete strict success document shown above. A
browser nonce is not an appropriate machine-to-machine credential.

### gRPC through an HTTP/JSON gateway

Patris does not contain a second protobuf product model or a native gRPC
client. Deploy an HTTP/JSON transcoding gateway in front of the gRPC service,
using [patris-product-sync.proto](examples/patris-product-sync.proto) as the
minimal boundary. The adapter places the original canonical JSON text in
`ApplyRequest.envelope_json`; the gRPC service must parse that string with a
lossless JSON decoder. This avoids `google.protobuf.Struct`, whose double-based
number representation cannot preserve every supported Patris int64 or decimal.

Point the adapter at the gateway route:

```powershell
$env:PATRIS_ADAPTER_INGRESS_SECRET = 'replace-with-a-local-secret'
$env:GRPC_GATEWAY_SECRET = 'replace-with-the-gateway-bearer-secret'
node .\scripts\examples\patris-delivery-adapter.cjs `
  --transport grpc-gateway `
  --target https://grpc-gateway.example/v1/patris:apply `
  --ingress-secret-env PATRIS_ADAPTER_INGRESS_SECRET `
  --target-secret-env GRPC_GATEWAY_SECRET
```

The gateway must forward `Idempotency-Key` and the `X-Patris-*` identity headers
as gRPC metadata. Every successful JSON-RPC, AJAX, or gateway response must
include `status`, matching `event_id`, `retryable`, `pending_products`, and
`deferred_products`. The adapter validates and propagates that state, including
valid retry-pending work, and maps 408/425/429/5xx gateway failures to Patris
retryable responses. Native gRPC without a gateway is not claimed or
implemented by this example.

## Local receiver and smoke test

The adapter's `mock` transport is a credential-free local receiver. `--fail-first
1` deliberately exposes one retryable failure before accepting the same event:

```powershell
$env:PATRIS_ADAPTER_INGRESS_SECRET = 'local-smoke-only'
node .\scripts\examples\patris-delivery-adapter.cjs `
  --transport mock `
  --fail-first 1 `
  --ingress-secret-env PATRIS_ADAPTER_INGRESS_SECRET
```

Run Patris with the adapter config and the same environment value. A verified
adapter transcript looks like:

```text
[adapter] listening=http://127.0.0.1:18081/ingest transport=mock
[adapter] retryable event_id=sha256:... reason=mock receiver requested a retry
[adapter] status=accepted event_id=sha256:... products=292
```

For a visible terminal failure, start the mock with `--fail-first 10` while the
Patris config permits only three attempts. Patris exits/logs a sanitized error
containing the endpoint without query parameters, HTTP status, attempt count,
and `retryable=true`. It never includes response bodies, product Codes, headers,
or credentials.

Run the repeatable smoke tests directly:

```powershell
node --test .\scripts\examples\patris-delivery-adapter.test.cjs
go test ./pkg/updateout
```

The Node tests cover REST-adapter identity validation, JSON-RPC framing,
WordPress form framing, gRPC-gateway body shape, explicit-null preservation,
omission of unseen keys, and retryable-failure recovery. The Go tests cover the
native HTTP sink's byte-stable retry/backoff and strict receiver responses.

## Operations and dead-letter diagnostics

The native sink deliberately does not persist a second product queue. The
source database plus deterministic event ID is the replay source of truth. On
exhaustion, capture the sanitized Patris error and adapter log in the service
manager, alert on `event_id` and attempt count, repair the destination, then
replay from Patris. A downstream system may maintain its own dead-letter queue
keyed by `event_id`; it must encrypt product data at rest and must never store
the authentication headers.

Recommended production checks:

1. Reject a request whose identity headers do not match its body.
2. Make a duplicate `event_id` return an idempotent terminal success.
3. Bound request size and response size at the reverse proxy.
4. Return explicit retryable state only for temporary work.
5. Keep adapter logs to event ID, counts, status, and a non-sensitive reason.
6. Alert after Patris exhausts attempts; do not silently discard the event.

See [Canonical Product Sync](CANONICAL-PRODUCT-SYNC.md) for the envelope and
strict receiver state machine, and [Transform, Export, and Send](examples/export-transform-send.md)
for the shared record pipeline.
