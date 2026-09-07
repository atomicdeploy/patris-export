package updateout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
)

func TestReceiverReplayRetainsActualCurrentDeliveryIdentity(t *testing.T) {
	input := canonical.NewEnvelope(nil, "kala.db", "owner", time.Now())
	currentSource := input.Source
	currentSource.Revision = "sha256:" + strings.Repeat("b", 64)
	current := &DeliveryReceipt{Status: "complete", EventID: "sha256:" + strings.Repeat("c", 64), Source: currentSource, InputSource: currentSource, OwnerCatalogRevision: "sha256:" + strings.Repeat("d", 64)}
	body, err := json.Marshal(map[string]interface{}{"success": true, "data": map[string]interface{}{"status": "replayed", "event_id": input.EventID, "retryable": false, "pending_products": 0, "deferred_products": 0, "delivery": current}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: 200, Attempts: 1}, body, input, true)
	if err != nil || result.EventID != input.EventID || result.Delivery == nil || result.Delivery.EventID != current.EventID || !result.Delivery.InputSource.SameIdentity(currentSource) || result.Delivery.OwnerCatalogRevision != current.OwnerCatalogRevision {
		t.Fatalf("receipt identity lost: %+v %v", result, err)
	}
}

func TestMalformedNestedDeliveryIsNotAccepted(t *testing.T) {
	input := canonical.NewEnvelope(nil, "kala.db", "owner", time.Now())
	body, _ := json.Marshal(map[string]interface{}{"success": true, "data": map[string]interface{}{"status": "accepted", "event_id": input.EventID, "retryable": false, "pending_products": 0, "deferred_products": 0, "delivery": map[string]interface{}{"status": "complete", "event_id": input.EventID}}})
	if _, err := classifyHTTPResponse(DeliveryResult{HTTPStatus: 200, Attempts: 1}, body, input, true); err == nil {
		t.Fatal("incomplete actual delivery proof accepted")
	}
}
