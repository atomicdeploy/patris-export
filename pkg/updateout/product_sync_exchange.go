package updateout

import "context"

func safeExchangeFailureCode(code string) string {
	if len(code) == 0 || len(code) > 96 {
		return "persistent_exchange_failed"
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "persistent_exchange_failed"
		}
	}
	return code
}

type productSyncExchangeKey struct{}

// ProductSyncExchange carries the exact encoded source envelope once. A write
// with an uncertain outcome must never be retried by the exchange.
type ProductSyncExchange func(context.Context, []byte) ([]byte, error)

// WithProductSyncExchange selects a caller-authenticated persistent transport.
// Existing envelope encoding and receiver receipt validation still apply.
func WithProductSyncExchange(ctx context.Context, exchange ProductSyncExchange) context.Context {
	return context.WithValue(ctx, productSyncExchangeKey{}, exchange)
}
