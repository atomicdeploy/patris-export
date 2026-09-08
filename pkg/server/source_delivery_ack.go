package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recorddiff"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

// Keep one verified source identity in memory, not a local record baseline.
// Watcher/projection caches advance before delivery and cannot prove acceptance.
type sourceDeliveryAcknowledgement struct {
	mu     sync.Mutex
	key    string
	source canonical.Source
}

func (s *Server) sourceDeliveryKey(cfg appconfig.Config, delivery updateout.Config) string {
	secret, err := updateout.ResolveProductSyncSecret(updateout.Normalize(delivery))
	if err != nil {
		return ""
	}
	material, _ := json.Marshal([]any{delivery.URL, delivery.ProductSyncSecretEnv, delivery.Headers, secret,
		cfg.Canonical, s.pricingCommandFingerprint(cfg), canonical.SourceIdentity(s.currentDBPath(), cfg.Canonical.SourceID, "")})
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:])
}

func (s *Server) recordSourceDeliveryAcknowledgement(key string, delivery updateout.Config, event updateout.Event, result updateout.DeliveryResult, err error) {
	input := event.Contract
	if (event.Type == "initial" || updateout.Normalize(delivery).Mode == "full") && event.SnapshotContract != nil {
		input = event.SnapshotContract
	}
	ack := &s.sourceDeliveryAck
	ack.mu.Lock()
	defer ack.mu.Unlock()
	// Uncertain or incomplete delivery can have changed the receiver baseline.
	ack.key = ""
	ack.source = canonical.Source{}
	if backgroundDeliveryOutcome(result, err, input) != "receipt_received" {
		return
	}
	if key == "" {
		return
	}
	ack.key = key
	ack.source = canonical.Source{ID: input.Source.ID, Dataset: input.Source.Dataset, Revision: input.Source.Revision}
}

func (s *Server) selectFreshSourceDelivery(snapshot *canonical.Envelope, code string, cfg appconfig.Config, delivery updateout.Config) *canonical.Envelope {
	key := s.sourceDeliveryKey(cfg, delivery)
	ack := &s.sourceDeliveryAck
	ack.mu.Lock()
	unchanged := key != "" && key == ack.key && snapshot.Source.SameIdentity(ack.source)
	if key != ack.key {
		ack.key = ""
		ack.source = canonical.Source{}
	}
	ack.mu.Unlock()
	if !unchanged {
		// A selected row cannot describe unrelated source edits or removals.
		// Deliver the fresh complete snapshot once, preserving its global hash.
		return canonical.ChangeEnvelope(snapshot, nil)
	}
	return canonical.ChangeEnvelope(snapshot, &recorddiff.ChangeSet{KeyField: "product_code", Modified: []recorddiff.RecordChange{{Code: code}}})
}
