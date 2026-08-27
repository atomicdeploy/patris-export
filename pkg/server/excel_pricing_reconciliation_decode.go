package server

import (
	"encoding/json"
	"errors"

	"github.com/atomicdeploy/patris-export/pkg/canonical"
)

// UnmarshalJSON keeps the strict three-field source identity while accepting
// receiver audit metadata such as generated_at and last_event_id. Those fields
// describe delivery history and must not make an otherwise identical source
// unsafe for Excel.
func (value *excelPricingRemoteSnapshotReconciliation) UnmarshalJSON(data []byte) error {
	if value == nil {
		return errors.New("remote pricing snapshot reconciliation is nil")
	}
	var decoded struct {
		Status          string                                    `json:"status"`
		IntegrityStatus string                                    `json:"integrity_status"`
		Warnings        []interface{}                             `json:"warnings"`
		Source          json.RawMessage                           `json:"source"`
		Counts          excelPricingRemoteSnapshotReconcileCounts `json:"counts"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var source struct {
		ID       string `json:"id"`
		Dataset  string `json:"dataset"`
		Revision string `json:"revision"`
	}
	if len(decoded.Source) == 0 || json.Unmarshal(decoded.Source, &source) != nil {
		return errors.New("remote pricing snapshot reconciliation source is invalid")
	}
	*value = excelPricingRemoteSnapshotReconciliation{
		Status:          decoded.Status,
		IntegrityStatus: decoded.IntegrityStatus,
		Warnings:        decoded.Warnings,
		Source: canonical.Source{
			ID:       source.ID,
			Dataset:  source.Dataset,
			Revision: source.Revision,
		},
		Counts: decoded.Counts,
	}
	return nil
}
