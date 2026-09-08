package updateout

import "math"

// ReceiverTiming contains optional, overlapping server spans in milliseconds.
// These diagnostics do not participate in delivery acceptance.
type ReceiverTiming struct {
	BootstrapToHandler *float64 `json:"bootstrap_to_handler,omitempty"`
	HandlerTotal       *float64 `json:"handler_total,omitempty"`
	JsonDecode         *float64 `json:"json_decode,omitempty"`
	Validation         *float64 `json:"validation,omitempty"`
	TransactionTotal   *float64 `json:"transaction_total,omitempty"`
	TransactionEntry   *float64 `json:"transaction_entry,omitempty"`
	TransactionWork    *float64 `json:"transaction_work,omitempty"`
	Projection         *float64 `json:"projection,omitempty"`
	Persistence        *float64 `json:"persistence,omitempty"`
	DestinationDrain   *float64 `json:"destination_drain,omitempty"`
	EventEmit          *float64 `json:"event_emit,omitempty"`
	ReceiverTotal      *float64 `json:"receiver_total,omitempty"`
}

func (t *ReceiverTiming) valid() bool {
	for _, value := range []*float64{t.BootstrapToHandler, t.HandlerTotal, t.JsonDecode, t.Validation, t.TransactionTotal, t.TransactionEntry, t.TransactionWork, t.Projection, t.Persistence, t.DestinationDrain, t.EventEmit, t.ReceiverTotal} {
		if value != nil && (*value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return false
		}
	}
	return true
}
