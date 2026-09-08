package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

func freshAckSnapshot(t *testing.T, products []canonical.Product, sourceID string) *canonical.Envelope {
	t.Helper()
	rows := make([]map[string]interface{}, 0, len(products))
	for _, product := range products {
		rows = append(rows, map[string]interface{}{"Code": product.ProductCode, "name": product.Name, "ALLANBAR": 1})
	}
	cfg := canonical.DefaultConfig()
	cfg.SourceID = sourceID
	_, envelope, err := canonical.TransformContext(context.Background(), rows, "kala.db", cfg, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func verifiedSourceAcknowledgement(input *canonical.Envelope) updateout.DeliveryResult {
	return updateout.DeliveryResult{HTTPStatus: 200, Status: "accepted", EventID: input.EventID, Attempts: 1,
		Delivery: &updateout.DeliveryReceipt{Status: "complete", EventID: input.EventID, Source: input.Source, InputSource: input.Source}}
}

func TestFreshSelectionRequiresAcknowledgedGlobalSource(t *testing.T) {
	cfg := commandTestConfig(t)
	s := &Server{dbPath: "kala.db"}
	products := []canonical.Product{{ProductCode: "A", Name: "selected"}, {ProductCode: "B", Name: "other"}}
	initial := freshAckSnapshot(t, products, cfg.Canonical.SourceID)
	selected := s.selectFreshSourceDelivery(initial, "A", cfg, cfg.SendUpdates)
	if selected.EventType != "snapshot" || len(selected.Products) != 2 {
		t.Fatal("unproven baseline was sent as incomplete delta")
	}
	event := updateout.Event{Type: "update", Contract: initial}
	s.recordSourceDeliveryAcknowledgement(s.sourceDeliveryKey(cfg, cfg.SendUpdates), cfg.SendUpdates, event, verifiedSourceAcknowledgement(initial), nil)
	selected = s.selectFreshSourceDelivery(initial, "A", cfg, cfg.SendUpdates)
	if selected.EventType != "update" || len(selected.Products) != 1 || selected.Products[0].ProductCode != "A" {
		t.Fatal("unchanged acknowledged source did not select one row")
	}

	for _, test := range []struct {
		name string
		rows []canonical.Product
	}{
		{"unrelated edit", []canonical.Product{{ProductCode: "A", Name: "selected"}, {ProductCode: "B", Name: "changed"}}},
		{"unrelated removal", []canonical.Product{{ProductCode: "A", Name: "selected"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh := freshAckSnapshot(t, test.rows, cfg.Canonical.SourceID)
			if fresh.Source.Revision == initial.Source.Revision {
				t.Fatal("fixture did not change global revision")
			}
			delivery := s.selectFreshSourceDelivery(fresh, "A", cfg, cfg.SendUpdates)
			if delivery.EventType != "snapshot" || len(delivery.Products) != len(test.rows) || !delivery.Source.SameIdentity(fresh.Source) {
				t.Fatal("global source change was reduced to selected row")
			}
			encoded, err := json.Marshal(delivery)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = canonical.VerifySnapshotJSON(encoded); err != nil {
				t.Fatalf("fallback is not coherent: %v", err)
			}
		})
	}

	changedDestination := cfg
	changedDestination.SendUpdates.URL = "https://other.invalid/receiver"
	if got := s.selectFreshSourceDelivery(initial, "A", changedDestination, changedDestination.SendUpdates); got.EventType != "snapshot" {
		t.Fatal("acknowledgement crossed destination")
	}
	if got := s.selectFreshSourceDelivery(initial, "A", cfg, cfg.SendUpdates); got.EventType != "snapshot" {
		t.Fatal("config change did not invalidate marker")
	}
	s.recordSourceDeliveryAcknowledgement(s.sourceDeliveryKey(cfg, cfg.SendUpdates), cfg.SendUpdates, event, verifiedSourceAcknowledgement(initial), nil)
	s.recordSourceDeliveryAcknowledgement(s.sourceDeliveryKey(cfg, cfg.SendUpdates), cfg.SendUpdates, event, updateout.DeliveryResult{Attempts: 1}, errors.New("ambiguous"))
	if got := s.selectFreshSourceDelivery(initial, "A", cfg, cfg.SendUpdates); got.EventType != "snapshot" {
		t.Fatal("ambiguous delivery retained prior source acknowledgement")
	}
}
