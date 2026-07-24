package recordmap

import (
	"context"
	"errors"
	"testing"
)

type cancellingStringer struct {
	cancel context.CancelFunc
}

func (value cancellingStringer) String() string {
	value.cancel()
	return "cancel-now"
}

func TestApplyContextStopsWhenMappingCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rows := []map[string]interface{}{{
		"Code":  "100",
		"Value": cancellingStringer{cancel: cancel},
	}}
	cfg := Config{
		Enabled: true,
		Values: map[string]ValueMap{
			"Value": {"cancel-now": "mapped"},
		},
	}

	mapped, err := ApplyContext(ctx, rows, cfg, "products")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyContext error=%v, want context.Canceled", err)
	}
	if mapped != nil {
		t.Fatalf("cancelled mapping returned partial rows: %#v", mapped)
	}
}

func TestKeyedAndCopyRowsContextRejectCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows := []map[string]interface{}{{"Code": "100"}}

	if keyed, err := KeyedContext(ctx, rows, "Code", true); !errors.Is(err, context.Canceled) || keyed != nil {
		t.Fatalf("KeyedContext result=%#v error=%v, want nil/context.Canceled", keyed, err)
	}
	if copied, err := CopyRowsContext(ctx, rows); !errors.Is(err, context.Canceled) || copied != nil {
		t.Fatalf("CopyRowsContext result=%#v error=%v, want nil/context.Canceled", copied, err)
	}
}
