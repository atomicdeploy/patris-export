package canonical

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

type cancellingPricingProvider struct {
	cancel context.CancelFunc
	once   sync.Once
	calls  atomic.Int32
}

func (provider *cancellingPricingProvider) Resolve(ctx context.Context, _ string) pricingcatalog.Resolution {
	provider.calls.Add(1)
	provider.once.Do(provider.cancel)
	select {
	case <-ctx.Done():
	default:
	}
	return pricingcatalog.Resolution{}
}

func TestTransformContextCancelsWorkerDispatchAndJoinsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := &cancellingPricingProvider{cancel: cancel}
	cfg := DefaultConfig()
	cfg.Pricing.Mode = pricingcatalog.ModeDigitalogic
	cfg.Pricing.Digitalogic.BaseURL = "https://pricing.invalid"
	cfg.Pricing.Digitalogic.MaxConcurrency = 4
	rows := make([]map[string]interface{}, 128)
	for index := range rows {
		rows[index] = map[string]interface{}{
			"Code":   fmt.Sprintf("%09d", 100000000+index),
			"Name":   "Product",
			"Serial": fmt.Sprintf("SKU-%d", index),
		}
	}

	productRows, envelope, err := TransformContext(ctx, rows, "kala.db", cfg, provider, time.Unix(1, 0))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TransformContext error=%v, want context.Canceled", err)
	}
	if productRows != nil || envelope != nil {
		t.Fatalf("cancelled transform returned partial output: rows=%d envelope=%v", len(productRows), envelope != nil)
	}
	if calls := provider.calls.Load(); calls < 1 || calls > int32(cfg.Pricing.Digitalogic.MaxConcurrency) {
		t.Fatalf("provider calls=%d, want bounded active workers only", calls)
	}
}

func TestTransformContextRejectsPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, envelope, err := TransformContext(
		ctx,
		[]map[string]interface{}{{"Code": "100000001", "Name": "Product"}},
		"kala.db",
		DefaultConfig(),
		pricingcatalog.NewProvider(pricingcatalog.Config{Mode: pricingcatalog.ModeNone}),
		time.Unix(1, 0),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TransformContext error=%v, want context.Canceled", err)
	}
	if rows != nil || envelope != nil {
		t.Fatalf("pre-cancelled transform returned output: rows=%#v envelope=%#v", rows, envelope)
	}
}
