# Source delivery connection reuse

Authenticated source deliveries share one lazily initialized HTTP client and transport instead of allocating a new pool for every delivery. Per-attempt context deadlines, header-only credentials, disabled ambient proxy and redirect rejection remain unchanged. Go partitions pooled connections by destination. This does not guarantee reuse when the server closes a connection or the idle timeout expires.

The updateout checks pass; a focused HTTP receiver check proves two separately obtained clients use the same TCP peer connection. Production source26f0d64 was deployed before publication as2.0.2-pooled-delivery, SHA2568F0946AC60CC9B5490D73D623B8081BCBADD20AA6F798D6EB478BF98A774EE34. Startup completed HTTP200/already_current, one attempt, complete receipt, zero pending/deferred:6367ms preparation plus8885ms dispatch, total15252ms. All three website services remained active.

Startup uses a cold connection, so this is deployment acceptance and not evidence of a production speedup from reuse. Changed-price latency and repeated production connection reuse remain unverified. This transport change does not remove WordPress initialization costs or complete the single-product integration.
