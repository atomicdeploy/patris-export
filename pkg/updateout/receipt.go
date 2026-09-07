package updateout

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
)

// DeliveryReceipt is the receiver's actual durable state. It may identify a
// newer event than the request being replayed; callers must bind it explicitly.
type DeliveryReceipt struct {
	EventID              string           `json:"event_id"`
	Status               string           `json:"status"`
	Source               canonical.Source `json:"source"`
	InputSource          canonical.Source `json:"input_source"`
	OwnerCatalogRevision string           `json:"owner_catalog_revision,omitempty"`
	PendingProducts      int              `json:"pending_products"`
	DeferredProducts     int              `json:"deferred_products"`
	DeferredMissing      int              `json:"deferred_missing"`
	DeferredAmbiguous    int              `json:"deferred_ambiguous"`
}

func (r *DeliveryReceipt) UnmarshalJSON(data []byte) error {
	type wireReceipt DeliveryReceipt
	var wire wireReceipt
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &wire) != nil || json.Unmarshal(data, &fields) != nil {
		return errReceiverStateInvalid
	}
	for _, key := range []string{"event_id", "status", "source", "input_source", "pending_products", "deferred_products", "deferred_missing", "deferred_ambiguous"} {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return errReceiverStateInvalid
		}
	}
	validHash := func(value string) bool {
		if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
			return false
		}
		_, err := hex.DecodeString(value[7:])
		return err == nil
	}
	validSource := func(source canonical.Source) bool {
		return strings.TrimSpace(source.ID) != "" && strings.TrimSpace(source.Dataset) != "" && validHash(source.Revision)
	}
	if !validHash(wire.EventID) || !validSource(wire.Source) || !validSource(wire.InputSource) || wire.PendingProducts < 0 || wire.DeferredProducts < 0 || wire.DeferredMissing < 0 || wire.DeferredAmbiguous < 0 {
		return errReceiverStateInvalid
	}
	switch wire.Status {
	case "complete", "pending", "deferred":
	default:
		return errReceiverStateInvalid
	}
	*r = DeliveryReceipt(wire)
	return nil
}
