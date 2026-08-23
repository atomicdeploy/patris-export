package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log"
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
	materialize func(context.Context) (canonical.Source, error)
	run         func(
		context.Context,
		updateout.Config,
		canonical.Source,
		uint64,
		func(uint64),
		func(excelPricingRemoteRevision) error,
		func(excelPricingRemoteSnapshotTerminalEvent) error,
	) error
	apply func(excelPricingRemoteRevision) error
	logf  func(string, ...interface{})
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

	mu                sync.Mutex
	epoch             uint64
	configInitialized bool
	desired           excelPricingRemoteBridgeConfig
	cursor            uint64
	acknowledged      bool
	verifiedRevision  atomic.Pointer[excelPricingRemoteRevision]
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
			onTerminal func(excelPricingRemoteSnapshotTerminalEvent) error,
		) error {
			return runExcelPricingRemoteEvents(
				ctx,
				cfg,
				source,
				initialCursor,
				onCursor,
				onRevision,
				onTerminal,
			)
		},
		apply: server.notifyExcelPricingRemoteRevisionChanged,
		logf:  log.Printf,
	})
}

func newExcelPricingRemoteEventsBridgeWithDependencies(
	dependencies excelPricingRemoteBridgeDependencies,
) *excelPricingRemoteEventsBridge {
	if dependencies.logf == nil {
		dependencies.logf = func(string, ...interface{}) {}
	}
	return &excelPricingRemoteEventsBridge{
		dependencies: dependencies,
		wake:         make(chan struct{}, 1),
		terminals:    newExcelPricingRemoteSnapshotTerminalHub(),
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
	configChanged := !bridge.configInitialized ||
		bridge.desired.valid != desired.valid ||
		bridge.desired.key != desired.key
	if !sourceChanged && !configChanged {
		epoch := bridge.epoch
		bridge.mu.Unlock()
		return epoch
	}
	bridge.configInitialized = true
	bridge.desired = desired
	bridge.epoch++
	epoch := bridge.epoch
	bridge.cursor = 0
	bridge.acknowledged = false
	bridge.verifiedRevision.Store(nil)
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
		bridge.dependencies.apply == nil {
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

func (bridge *excelPricingRemoteEventsBridge) acceptRevision(
	epoch uint64,
	source canonical.Source,
	revision excelPricingRemoteRevision,
) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if epoch == 0 || bridge.epoch != epoch || revision.Source != source {
		return errExcelPricingRemoteBridgeStale
	}
	bridge.verifiedRevision.Store(nil)
	if err := bridge.dependencies.apply(revision); err != nil {
		return err
	}
	bridge.acknowledged = true
	verified := revision
	bridge.verifiedRevision.Store(&verified)
	return nil
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
	if epoch == 0 || bridge.epoch != epoch || event.Source != source {
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
	if bridge == nil {
		return nil
	}
	return bridge.terminals
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
