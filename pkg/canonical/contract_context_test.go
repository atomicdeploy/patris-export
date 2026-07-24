package canonical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type cancelAfterChecksContext struct {
	context.Context
	limit int32
	calls atomic.Int32
	done  chan struct{}
	once  sync.Once
}

func newCancelAfterChecksContext(limit int32) *cancelAfterChecksContext {
	return &cancelAfterChecksContext{
		Context: context.Background(),
		limit:   limit,
		done:    make(chan struct{}),
	}
}

func (ctx *cancelAfterChecksContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *cancelAfterChecksContext) Err() error {
	if ctx.calls.Add(1) >= ctx.limit {
		ctx.once.Do(func() { close(ctx.done) })
		return context.Canceled
	}
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}

func TestSourceRevisionContextMatchesLegacyMaterial(t *testing.T) {
	products := []Product{
		{ProductCode: "200", RecordHash: "hash-2"},
		{ProductCode: "100", RecordHash: "hash-1"},
	}
	categories := []Category{
		{CategoryCode: "20", RecordHash: "category-2"},
		{CategoryCode: "10", RecordHash: "category-1"},
	}
	excluded := []string{"999", "111"}
	quarantined := []string{"700", "800"}

	material := []string{
		"200=hash-2",
		"100=hash-1",
		"category:20=category-2",
		"category:10=category-1",
		"excluded=999",
		"excluded=111",
	}
	sort.Strings(material)
	material = append(material, "quarantined=700", "quarantined=800")
	want := hashBytes([]byte(strings.Join(material, "\n")))

	got, err := sourceRevisionContext(context.Background(), products, categories, excluded, quarantined)
	if err != nil {
		t.Fatalf("sourceRevisionContext error: %v", err)
	}
	if got != want {
		t.Fatalf("source revision=%q, want %q", got, want)
	}
}

func TestSourceRevisionContextCancelsDuringMaterialization(t *testing.T) {
	products := make([]Product, 64)
	for index := range products {
		products[index] = Product{
			ProductCode: fmt.Sprintf("%04d", index),
			RecordHash:  fmt.Sprintf("hash-%04d", index),
		}
	}
	ctx := newCancelAfterChecksContext(12)
	revision, err := sourceRevisionContext(ctx, products, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sourceRevisionContext error=%v, want context.Canceled", err)
	}
	if revision != "" {
		t.Fatalf("cancelled source revision=%q, want empty", revision)
	}
}

func TestEventIDContextMatchesLegacyJSONIdentity(t *testing.T) {
	envelope := &Envelope{
		Schema:        `patris.<sync>`,
		EventType:     "snapshot",
		LocalCurrency: "IRT",
		FormulaID:     "landed&price",
		Source: Source{
			ID:       `source-"one"`,
			Dataset:  "kala.db",
			Revision: "sha256:revision",
		},
		GeneratedAt: "2026-07-24T00:00:00Z",
		Products: []Product{
			{ProductCode: "200", RecordHash: "hash-2"},
			{ProductCode: "100", RecordHash: "hash-1"},
		},
		Categories: []Category{
			{CategoryCode: "20", RecordHash: "category-2"},
			{CategoryCode: "10", RecordHash: "category-1"},
		},
		ExcludedCodes:    []string{"999", "111"},
		DeletedCodes:     []Tombstone{{ProductCode: "300", Deleted: true}, {ProductCode: "400", Deleted: false}},
		QuarantinedCodes: nil,
	}
	productHashes := []string{"200=hash-2", "100=hash-1"}
	categoryHashes := []string{"20=category-2", "10=category-1"}
	sort.Strings(productHashes)
	sort.Strings(categoryHashes)
	legacyIdentity := struct {
		Schema           string      `json:"schema"`
		EventType        string      `json:"event_type"`
		LocalCurrency    string      `json:"local_currency,omitempty"`
		FormulaID        string      `json:"formula_id,omitempty"`
		Source           Source      `json:"source"`
		GeneratedAt      string      `json:"generated_at"`
		Products         []string    `json:"products"`
		Categories       []string    `json:"categories"`
		ExcludedCodes    []string    `json:"excluded_codes"`
		DeletedCodes     []Tombstone `json:"deleted_codes,omitempty"`
		QuarantinedCodes []string    `json:"quarantined_codes"`
	}{
		Schema: envelope.Schema, EventType: envelope.EventType,
		LocalCurrency: envelope.LocalCurrency, FormulaID: envelope.FormulaID,
		Source: envelope.Source, GeneratedAt: envelope.GeneratedAt,
		Products: productHashes, Categories: categoryHashes,
		ExcludedCodes: envelope.ExcludedCodes, DeletedCodes: envelope.DeletedCodes,
		QuarantinedCodes: envelope.QuarantinedCodes,
	}
	material, err := json.Marshal(legacyIdentity)
	if err != nil {
		t.Fatalf("marshal legacy identity: %v", err)
	}
	want := hashBytes(material)

	got, err := eventIDContext(context.Background(), envelope)
	if err != nil {
		t.Fatalf("eventIDContext error: %v", err)
	}
	if got != want {
		t.Fatalf("event ID=%q, want %q; legacy=%s", got, want, material)
	}
}

func TestEventIDContextCancelsDuringIdentityStreaming(t *testing.T) {
	excluded := make([]string, 128)
	for index := range excluded {
		excluded[index] = fmt.Sprintf("excluded-%04d", index)
	}
	envelope := &Envelope{
		Schema:           ContractName,
		EventType:        "snapshot",
		Source:           Source{ID: "source", Dataset: "kala.db", Revision: "sha256:revision"},
		GeneratedAt:      "2026-07-24T00:00:00Z",
		Products:         []Product{},
		Categories:       []Category{},
		ExcludedCodes:    excluded,
		QuarantinedCodes: []string{},
	}
	// Empty product/category lists keep the pre-stream check count bounded; the
	// cancellation threshold is reached while excluded_codes is being emitted.
	ctx := newCancelAfterChecksContext(40)
	value, err := eventIDContext(ctx, envelope)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("eventIDContext error=%v, want context.Canceled", err)
	}
	if value != "" {
		t.Fatalf("cancelled event ID=%q, want empty", value)
	}
}
