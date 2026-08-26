package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

var errExcelPricingRemoteBridgeStale = errors.New("pricing event subscriber generation is stale")

type excelPricingRemoteBridgeConfig struct {
	config updateout.Config
	key    [sha256.Size]byte
	valid  bool
}

type excelPricingRemoteBridgeDependencies struct {
	config      func() appconfig.Config
	resolve     func(appconfig.Config) excelPricingRemoteBridgeConfig
	fenceConfig func()
	materialize func(context.Context) (canonical.Source, error)
	run         func(
		context.Context,
		updateout.Config,
		canonical.Source,
		uint64,
		func(uint64),
		func(excelPricingRemoteRevision) error,
		func(excelPricingRemoteSourceLifecycle) error,
		func(excelPricingRemoteSnapshotTerminalEvent) error,
	) error
	apply     func(excelPricingRemoteRevision) error
	lifecycle func(excelPricingRemoteSourceLifecycle) error
	logf      func(string, ...interface{})
}

// excelPricingRemoteEventsBridge owns the production lifecycle around the
// compile-isolated WordPress event client. All restarts pass through one
// generation fence, so a callback from a cancelled subscription cannot append
// a stale invalidation or advance its cursor.
type excelPricingRemoteEventsBridge struct {
	dependencies excelPricingRemoteBridgeDependencies
	wake         chan struct{}
	terminals    *excelPricingRemoteSnapshotTerminalHub
	startOnce    sync.Once
	lifecycleMu  sync.Mutex
	lifecycleCtx context.Context
	lifecycleWG  *sync.WaitGroup
	managerLive  bool

	mu                  sync.Mutex
	epoch               uint64
	configInitialized   bool
	desired             excelPricingRemoteBridgeConfig
	cursor              uint64
	acknowledged        bool
	streamSource        canonical.Source
	streamPresent       bool
	acceptedSources     map[string]struct{}
	acceptedSourceOrder []string
	lifecycleSeen       map[string]string
	lifecycleOrder      []string
	verifiedRevision    atomic.Pointer[excelPricingRemoteRevision]
}

func newExcelPricingRemoteEventsBridge(server *Server) *excelPricingRemoteEventsBridge {
	if server == nil {
		return nil
	}
	return newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config:  server.Config,
		resolve: resolveExcelPricingRemoteBridgeConfig,
		materialize: func(ctx context.Context) (canonical.Source, error) {
			cfg := server.Config()
			materializeCtx, cancel := context.WithTimeout(ctx, canonicalRequestTimeout(cfg))
			defer cancel()
			result, err := server.canonicalRecordResultContext(materializeCtx)
			if err != nil || result.Contract == nil ||
				!validExcelPricingRemoteSource(result.Contract.Source) {
				return canonical.Source{}, errExcelPricingRemoteConfiguration
			}
			return result.Contract.Source, nil
		},
		run: func(
			ctx context.Context,
			cfg updateout.Config,
			source canonical.Source,
			initialCursor uint64,
			onCursor func(uint64),
			onRevision func(excelPricingRemoteRevision) error,
			onSourceLifecycle func(excelPricingRemoteSourceLifecycle) error,
			onTerminal func(excelPricingRemoteSnapshotTerminalEvent) error,
		) error {
			return runExcelPricingRemoteEvents(
				ctx,
				cfg,
				source,
				initialCursor,
				onCursor,
				onRevision,
				onSourceLifecycle,
				onTerminal,
			)
		},
		fenceConfig: server.fenceExcelPricingRemoteConfiguration,
		apply:       server.notifyExcelPricingRemoteRevisionChanged,
		lifecycle:   server.notifyExcelPricingRemoteSourceLifecycle,
		logf:        log.Printf,
	})
}

func newExcelPricingRemoteEventsBridgeWithDependencies(
	dependencies excelPricingRemoteBridgeDependencies,
) *excelPricingRemoteEventsBridge {
	if dependencies.logf == nil {
		dependencies.logf = func(string, ...interface{}) {}
	}
	return &excelPricingRemoteEventsBridge{
		dependencies:    dependencies,
		wake:            make(chan struct{}, 1),
		terminals:       newExcelPricingRemoteSnapshotTerminalHub(),
		acceptedSources: make(map[string]struct{}),
		lifecycleSeen:   make(map[string]string),
	}
}

func resolveExcelPricingRemoteBridgeConfig(cfg appconfig.Config) excelPricingRemoteBridgeConfig {
	remote, secret, _, err := resolveExcelPricingRemote(cfg.SendUpdates, "state")
	if err != nil {
		return excelPricingRemoteBridgeConfig{}
	}
	material, err := json.Marshal([]interface{}{
		remote,
		cfg.Canonical,
		cfg.Transform,
		cfg.Database.RTLConversion,
	})
	if err != nil {
		return excelPricingRemoteBridgeConfig{}
	}
	hash := sha256.New()
	_, _ = hash.Write(material)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(secret))
	var key [sha256.Size]byte
	copy(key[:], hash.Sum(nil))
	return excelPricingRemoteBridgeConfig{config: remote, key: key, valid: true}
}

func (bridge *excelPricingRemoteEventsBridge) start(
	ctx context.Context,
	waitGroup *sync.WaitGroup,
) {
	if bridge == nil || ctx == nil || waitGroup == nil {
		return
	}
	bridge.startOnce.Do(func() {
		bridge.lifecycleMu.Lock()
		bridge.lifecycleCtx = ctx
		bridge.lifecycleWG = waitGroup
		bridge.lifecycleMu.Unlock()
		bridge.sourceChanged()
	})
}

func (bridge *excelPricingRemoteEventsBridge) configChanged(cfg appconfig.Config) {
	if bridge == nil {
		return
	}
	bridge.reconcile(cfg, false, true)
}

func (bridge *excelPricingRemoteEventsBridge) sourceChanged() {
	if bridge == nil || bridge.dependencies.config == nil {
		return
	}
	bridge.reconcile(bridge.dependencies.config(), true, true)
}

// fenceSourceChange rejects old-generation callbacks synchronously. The
// caller may then replace the source and commit the wake only after the new
// source is visible to materialization.
func (bridge *excelPricingRemoteEventsBridge) fenceSourceChange() uint64 {
	if bridge == nil || bridge.dependencies.config == nil {
		return 0
	}
	return bridge.reconcile(bridge.dependencies.config(), true, false)
}

func (bridge *excelPricingRemoteEventsBridge) commitSourceChange(epoch uint64) {
	if bridge == nil || epoch == 0 {
		return
	}
	bridge.mu.Lock()
	current := bridge.epoch == epoch
	bridge.mu.Unlock()
	if current {
		bridge.signal()
	}
}

func (bridge *excelPricingRemoteEventsBridge) reconcile(
	cfg appconfig.Config,
	sourceChanged bool,
	wake bool,
) uint64 {
	if bridge == nil || bridge.dependencies.resolve == nil {
		return 0
	}
	desired := bridge.dependencies.resolve(cfg)
	bridge.mu.Lock()
	initialized := bridge.configInitialized
	configChanged := !bridge.configInitialized ||
		bridge.desired.valid != desired.valid ||
		bridge.desired.key != desired.key
	if !sourceChanged && !configChanged {
		epoch := bridge.epoch
		bridge.mu.Unlock()
		return epoch
	}
	if initialized && configChanged && !sourceChanged && bridge.dependencies.fenceConfig != nil {
		bridge.dependencies.fenceConfig()
	}
	bridge.configInitialized = true
	bridge.desired = desired
	bridge.epoch++
	epoch := bridge.epoch
	bridge.cursor = 0
	bridge.acknowledged = false
	bridge.streamSource = canonical.Source{}
	bridge.streamPresent = false
	bridge.acceptedSources = make(map[string]struct{})
	bridge.acceptedSourceOrder = nil
	bridge.lifecycleSeen = make(map[string]string)
	bridge.lifecycleOrder = nil
	bridge.verifiedRevision.Store(nil)
	if bridge.terminals != nil {
		bridge.terminals.resetAuthenticatedEpoch()
	}
	bridge.mu.Unlock()
	if wake {
		bridge.signal()
	}
	return epoch
}

func (bridge *excelPricingRemoteEventsBridge) signal() {
	select {
	case bridge.wake <- struct{}{}:
	default:
	}
	bridge.ensureManager()
}

func (bridge *excelPricingRemoteEventsBridge) ensureManager() {
	bridge.lifecycleMu.Lock()
	defer bridge.lifecycleMu.Unlock()
	ctx, waitGroup := bridge.lifecycleCtx, bridge.lifecycleWG
	if bridge.managerLive || ctx == nil || waitGroup == nil || ctx.Err() != nil {
		return
	}
	bridge.launchManagerLocked(ctx, waitGroup)
}

func (bridge *excelPricingRemoteEventsBridge) launchManagerLocked(
	ctx context.Context,
	waitGroup *sync.WaitGroup,
) {
	bridge.managerLive = true
	waitGroup.Add(1)
	go func() {
		bridge.run(ctx)

		bridge.lifecycleMu.Lock()
		bridge.managerLive = false
		if ctx.Err() == nil && len(bridge.wake) != 0 {
			bridge.launchManagerLocked(ctx, waitGroup)
		}
		bridge.lifecycleMu.Unlock()
		waitGroup.Done()
	}()
}

func (bridge *excelPricingRemoteEventsBridge) run(ctx context.Context) {
	var (
		workerCancel  context.CancelFunc
		workerDone    <-chan struct{}
		observedEpoch uint64
	)
	stopWorker := func() {
		if workerCancel != nil {
			workerCancel()
		}
		if workerDone != nil {
			<-workerDone
		}
		workerCancel = nil
		workerDone = nil
	}
	defer stopWorker()

	for {
		select {
		case <-ctx.Done():
			return
		case <-bridge.wake:
			epoch, _ := bridge.desiredGeneration()
			if epoch == observedEpoch {
				continue
			}
			stopWorker()
			epoch, desired := bridge.desiredGeneration()
			observedEpoch = epoch
			if !desired.valid {
				return
			}
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			workerCancel = cancel
			workerDone = done
			go func() {
				defer close(done)
				bridge.runGeneration(workerCtx, epoch, desired.config)
			}()
		case <-workerDone:
			return
		}
	}
}

func (bridge *excelPricingRemoteEventsBridge) desiredGeneration() (
	uint64,
	excelPricingRemoteBridgeConfig,
) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.epoch, bridge.desired
}

func (bridge *excelPricingRemoteEventsBridge) runGeneration(
	ctx context.Context,
	epoch uint64,
	cfg updateout.Config,
) {
	defer bridge.clearVerifiedRevision(epoch)
	if bridge.dependencies.materialize == nil || bridge.dependencies.run == nil ||
		bridge.dependencies.apply == nil || bridge.dependencies.lifecycle == nil {
		bridge.dependencies.logf("Pricing event subscriber is inactive: lifecycle dependencies are unavailable")
		return
	}
	source, err := bridge.dependencies.materialize(ctx)
	if err != nil || !validExcelPricingRemoteSource(source) ||
		!bridge.generationCurrent(epoch) {
		if ctx.Err() == nil && bridge.generationCurrent(epoch) {
			bridge.dependencies.logf("Pricing event subscriber is inactive: canonical source is unavailable")
		}
		return
	}
	if !bridge.initializeStreamSource(epoch, source) {
		return
	}
	initialCursor := bridge.cursorForGeneration(epoch)
	err = bridge.dependencies.run(
		ctx,
		cfg,
		source,
		initialCursor,
		func(cursor uint64) { bridge.persistCursor(epoch, cursor) },
		func(revision excelPricingRemoteRevision) error {
			return bridge.acceptRevision(epoch, source, revision)
		},
		func(lifecycle excelPricingRemoteSourceLifecycle) error {
			return bridge.acceptSourceLifecycle(epoch, source, lifecycle)
		},
		func(event excelPricingRemoteSnapshotTerminalEvent) error {
			return bridge.acceptSnapshotTerminal(epoch, source, event)
		},
	)
	if err != nil && ctx.Err() == nil && bridge.generationCurrent(epoch) {
		// Never include an endpoint, environment-variable name, transport error,
		// response, or credential in production diagnostics.
		bridge.dependencies.logf("Pricing event subscriber stopped before cancellation")
	}
}

func (bridge *excelPricingRemoteEventsBridge) generationCurrent(epoch uint64) bool {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return epoch != 0 && bridge.epoch == epoch
}

func (bridge *excelPricingRemoteEventsBridge) cursorForGeneration(epoch uint64) uint64 {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.epoch != epoch || !bridge.acknowledged {
		return 0
	}
	return bridge.cursor
}

func (bridge *excelPricingRemoteEventsBridge) initializeStreamSource(
	epoch uint64,
	source canonical.Source,
) bool {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if epoch == 0 || bridge.epoch != epoch || !validExcelPricingRemoteSource(source) {
		return false
	}
	bridge.streamSource = source
	bridge.streamPresent = true
	bridge.rememberAcceptedSourceLocked(source)
	return true
}

func (bridge *excelPricingRemoteEventsBridge) acceptRevision(
	epoch uint64,
	source canonical.Source,
	revision excelPricingRemoteRevision,
) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if epoch == 0 || bridge.epoch != epoch ||
		!sameExcelPricingRemoteSourceScope(revision.Source, source) ||
		!bridge.streamPresent || revision.Source != bridge.streamSource {
		return errExcelPricingRemoteBridgeStale
	}
	// A reconnect validates the authoritative composite before replaying queued
	// frames. Treat an identical authenticated composite as an acknowledgement,
	// not a state change: reapplying it would advance the local generation and
	// cancel a snapshot that already fetched the same revision.
	if sameExcelPricingRemoteCompositeRevision(bridge.verifiedRevision.Load(), revision) {
		bridge.acknowledged = true
		bridge.rememberAcceptedSourceLocked(revision.Source)
		return nil
	}
	bridge.verifiedRevision.Store(nil)
	if err := bridge.dependencies.apply(revision); err != nil {
		return err
	}
	bridge.acknowledged = true
	bridge.rememberAcceptedSourceLocked(revision.Source)
	verified := revision
	bridge.verifiedRevision.Store(&verified)
	return nil
}

func (bridge *excelPricingRemoteEventsBridge) acceptSourceLifecycle(
	epoch uint64,
	fixedScope canonical.Source,
	lifecycle excelPricingRemoteSourceLifecycle,
) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if epoch == 0 || bridge.epoch != epoch ||
		!validExcelPricingRemoteSource(lifecycle.Source) ||
		!sameExcelPricingRemoteSourceScope(lifecycle.Source, fixedScope) {
		return errExcelPricingRemoteBridgeStale
	}
	key, fingerprint := excelPricingRemoteBridgeLifecycleIdentity(lifecycle)
	if key != "" {
		if stored, seen := bridge.lifecycleSeen[key]; seen {
			if stored != fingerprint {
				return errExcelPricingRemoteBridgeStale
			}
			bridge.acknowledged = true
			return nil
		}
	}
	if !bridge.validSourceLifecycleLocked(lifecycle) {
		return errExcelPricingRemoteBridgeStale
	}
	if bridge.dependencies.lifecycle == nil {
		return errExcelPricingRemoteConfiguration
	}
	// Fence the previously verified composite before any lifecycle side effect.
	// A failing callback therefore leaves readers fail-closed and the durable
	// cursor/state transition remains available for replay.
	bridge.verifiedRevision.Store(nil)
	if err := bridge.dependencies.lifecycle(lifecycle); err != nil {
		return err
	}

	bridge.acknowledged = true
	switch lifecycle.Mode {
	case "ordered":
		bridge.streamSource = lifecycle.Source
		bridge.streamPresent = lifecycle.Change != "removed"
		bridge.rememberAcceptedSourceLocked(lifecycle.Source)
		bridge.rememberLifecycleLocked(key, fingerprint)
	case "reconcile_present":
		bridge.streamSource = lifecycle.Source
		bridge.streamPresent = true
		bridge.rememberAcceptedSourceLocked(lifecycle.Source)
	case "reconcile_absent":
		bridge.streamSource = lifecycle.Source
		bridge.streamPresent = false
	case "validation_gap":
		bridge.verifiedRevision.Store(nil)
		return nil
	default:
		return errExcelPricingRemoteBridgeStale
	}
	if lifecycle.Revision == nil || !bridge.streamPresent {
		bridge.verifiedRevision.Store(nil)
		return nil
	}
	verified := *lifecycle.Revision
	bridge.verifiedRevision.Store(&verified)
	return nil
}

func (bridge *excelPricingRemoteEventsBridge) validSourceLifecycleLocked(
	lifecycle excelPricingRemoteSourceLifecycle,
) bool {
	if !validExcelPricingRemoteSource(lifecycle.Source) ||
		!validExcelPricingRemoteLifecycleValidation(lifecycle) {
		return false
	}
	if lifecycle.Revision != nil &&
		(lifecycle.Revision.Source != lifecycle.Source ||
			!validExcelPricingRemoteRevisionParts(
				lifecycle.Revision.StateRevision,
				lifecycle.Revision.CatalogRevision,
				lifecycle.Revision.PricingStateRevision,
				lifecycle.Revision.PricingPolicyRevision,
			) || !isStrongExcelPricingRevisionETag(
			lifecycle.Revision.ETag,
			lifecycle.Revision.StateRevision,
		)) {
		return false
	}
	switch lifecycle.Mode {
	case "ordered":
		if lifecycle.EventID == 0 || !isSHA256Revision(lifecycle.IdempotencyKey) ||
			lifecycle.ValidationOrigin != "source_event" ||
			!validExcelPricingRemoteSourceTransition(
				lifecycle.Name,
				lifecycle.Change,
				lifecycle.Source,
				lifecycle.PreviousSourceRevision,
				bridge.streamSource,
				bridge.streamPresent,
			) {
			return false
		}
		if lifecycle.Change == "removed" {
			return lifecycle.Revision == nil
		}
		return (lifecycle.ValidationOutcome == excelPricingRemoteSourceCurrent) ==
			(lifecycle.Revision != nil)
	case "validation_gap":
		return lifecycle.Revision == nil && lifecycle.IdempotencyKey == "" &&
			lifecycle.Source == bridge.streamSource &&
			(lifecycle.ValidationOutcome == excelPricingRemoteSourceSuperseded ||
				lifecycle.ValidationOutcome == excelPricingRemoteSourceAbsent ||
				(lifecycle.ValidationOutcome == excelPricingRemoteSourceCurrent &&
					!bridge.streamPresent))
	case "reconcile_present":
		return lifecycle.Name == "pricing.source.changed" && lifecycle.Change == "reconciled" &&
			lifecycle.IdempotencyKey == "" && lifecycle.Revision != nil &&
			lifecycle.ValidationOutcome == excelPricingRemoteSourceCurrent
	case "reconcile_absent":
		return lifecycle.Name == "pricing.source.removed" && lifecycle.Change == "removed" &&
			lifecycle.IdempotencyKey == "" && lifecycle.Revision == nil &&
			lifecycle.Source == bridge.streamSource && bridge.streamPresent &&
			lifecycle.ValidationOutcome == excelPricingRemoteSourceAbsent
	default:
		return false
	}
}

func validExcelPricingRemoteLifecycleValidation(lifecycle excelPricingRemoteSourceLifecycle) bool {
	switch lifecycle.ValidationOutcome {
	case excelPricingRemoteSourceCurrent:
		return lifecycle.CurrentSourceRevision == ""
	case excelPricingRemoteSourceSuperseded:
		return lifecycle.Revision == nil &&
			isSHA256Revision(lifecycle.CurrentSourceRevision) &&
			lifecycle.CurrentSourceRevision != lifecycle.Source.Revision
	case excelPricingRemoteSourceAbsent:
		return lifecycle.Revision == nil && lifecycle.CurrentSourceRevision == ""
	default:
		return false
	}
}

func excelPricingRemoteBridgeLifecycleIdentity(
	lifecycle excelPricingRemoteSourceLifecycle,
) (string, string) {
	if lifecycle.Mode != "ordered" {
		return "", ""
	}
	return excelPricingRemoteSourceEventDedupeKey(lifecycle.Name, lifecycle.IdempotencyKey),
		strings.Join([]string{
			lifecycle.Name,
			lifecycle.Change,
			lifecycle.Source.ID,
			lifecycle.Source.Dataset,
			lifecycle.Source.Revision,
			lifecycle.PreviousSourceRevision,
		}, "\x00")
}

func (bridge *excelPricingRemoteEventsBridge) rememberAcceptedSourceLocked(source canonical.Source) {
	if bridge.acceptedSources == nil {
		bridge.acceptedSources = make(map[string]struct{})
	}
	key := source.Revision
	if _, exists := bridge.acceptedSources[key]; exists {
		for index, accepted := range bridge.acceptedSourceOrder {
			if accepted != key {
				continue
			}
			copy(bridge.acceptedSourceOrder[index:], bridge.acceptedSourceOrder[index+1:])
			bridge.acceptedSourceOrder = bridge.acceptedSourceOrder[:len(bridge.acceptedSourceOrder)-1]
			break
		}
	} else {
		bridge.acceptedSources[key] = struct{}{}
	}
	bridge.acceptedSourceOrder = append(bridge.acceptedSourceOrder, key)
	if len(bridge.acceptedSourceOrder) > excelPricingSnapshotEventHistory {
		delete(bridge.acceptedSources, bridge.acceptedSourceOrder[0])
		bridge.acceptedSourceOrder = bridge.acceptedSourceOrder[1:]
	}
}

func (bridge *excelPricingRemoteEventsBridge) rememberLifecycleLocked(key, fingerprint string) {
	if key == "" {
		return
	}
	if bridge.lifecycleSeen == nil {
		bridge.lifecycleSeen = make(map[string]string)
	}
	if _, exists := bridge.lifecycleSeen[key]; exists {
		return
	}
	bridge.lifecycleSeen[key] = fingerprint
	bridge.lifecycleOrder = append(bridge.lifecycleOrder, key)
	if len(bridge.lifecycleOrder) > excelPricingSnapshotEventHistory {
		delete(bridge.lifecycleSeen, bridge.lifecycleOrder[0])
		bridge.lifecycleOrder = bridge.lifecycleOrder[1:]
	}
}

func (bridge *excelPricingRemoteEventsBridge) sourceAcceptedLocked(source canonical.Source) bool {
	if !sameExcelPricingRemoteSourceScope(source, bridge.streamSource) {
		return false
	}
	_, accepted := bridge.acceptedSources[source.Revision]
	return accepted
}

func (bridge *excelPricingRemoteEventsBridge) sourceAbsent() bool {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return !bridge.streamPresent
}

func sameExcelPricingRemoteCompositeRevision(
	previous *excelPricingRemoteRevision,
	current excelPricingRemoteRevision,
) bool {
	return previous != nil &&
		previous.Source == current.Source &&
		previous.StateRevision == current.StateRevision &&
		previous.CatalogRevision == current.CatalogRevision &&
		previous.PricingStateRevision == current.PricingStateRevision &&
		previous.PricingPolicyRevision == current.PricingPolicyRevision &&
		previous.ETag == current.ETag
}

func (bridge *excelPricingRemoteEventsBridge) acceptSnapshotTerminal(
	epoch uint64,
	source canonical.Source,
	event excelPricingRemoteSnapshotTerminalEvent,
) error {
	if bridge == nil || bridge.terminals == nil {
		return errExcelPricingRemoteBridgeStale
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if epoch == 0 || bridge.epoch != epoch ||
		!sameExcelPricingRemoteSourceScope(event.Source, source) ||
		!bridge.sourceAcceptedLocked(event.Source) {
		return errExcelPricingRemoteBridgeStale
	}
	return bridge.terminals.publishAuthenticated(event)
}

func (bridge *excelPricingRemoteEventsBridge) clearVerifiedRevision(epoch uint64) {
	if bridge == nil || epoch == 0 {
		return
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.epoch == epoch {
		bridge.verifiedRevision.Store(nil)
	}
}

func (bridge *excelPricingRemoteEventsBridge) snapshotTerminals() excelPricingRemoteSnapshotTerminalSource {
	if bridge == nil || bridge.terminals == nil {
		return nil
	}
	return bridge.terminals.authenticatedLease()
}

func (bridge *excelPricingRemoteEventsBridge) revisionCurrent(
	source canonical.Source,
	stateRevision string,
	catalogRevision string,
) bool {
	if bridge == nil || !validExcelPricingRemoteSource(source) ||
		!validExcelPricingRemoteRevisionParts(stateRevision, catalogRevision) {
		return false
	}
	verified := bridge.verifiedRevision.Load()
	return verified != nil && verified.Source == source &&
		verified.StateRevision == stateRevision &&
		verified.CatalogRevision == catalogRevision
}

func (bridge *excelPricingRemoteEventsBridge) persistCursor(epoch, cursor uint64) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if epoch == 0 || bridge.epoch != epoch || !bridge.acknowledged {
		return
	}
	bridge.cursor = cursor
}
