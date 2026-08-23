package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const excelPricingRemoteBridgeTestTimeout = 2 * time.Second

type excelPricingRemoteBridgeTestRun struct {
	config        updateout.Config
	source        canonical.Source
	initialCursor uint64
	onCursor      func(uint64)
	onRevision    func(excelPricingRemoteRevision) error
	onSource      func(excelPricingRemoteSourceTransition) error
	onTerminal    func(excelPricingRemoteSnapshotTerminalEvent) error
	onApply       func(excelPricingRemoteApplyTerminalEvent) error
	onConnected   func(context.Context) error
	stopped       chan struct{}
}

func excelPricingRemoteBridgeTestConfig(key string) appconfig.Config {
	cfg := appconfig.Default()
	cfg.SendUpdates = updateout.Config{
		Enabled: true,
		URL:     key,
	}
	return cfg
}

func excelPricingRemoteBridgeTestResolve(cfg appconfig.Config) excelPricingRemoteBridgeConfig {
	key := strings.TrimSpace(cfg.SendUpdates.URL)
	if !cfg.SendUpdates.Enabled || key == "" {
		return excelPricingRemoteBridgeConfig{}
	}
	return excelPricingRemoteBridgeConfig{
		config: cfg.SendUpdates,
		key:    sha256.Sum256([]byte(key)),
		valid:  true,
	}
}

func excelPricingRemoteBridgeTestRunner(
	runs chan<- *excelPricingRemoteBridgeTestRun,
) func(
	context.Context,
	updateout.Config,
	canonical.Source,
	uint64,
	func(uint64),
	func(excelPricingRemoteRevision) error,
	func(excelPricingRemoteSourceTransition) error,
	func(excelPricingRemoteSnapshotTerminalEvent) error,
	func(excelPricingRemoteApplyTerminalEvent) error,
	func(context.Context) error,
) error {
	return func(
		ctx context.Context,
		cfg updateout.Config,
		source canonical.Source,
		initialCursor uint64,
		onCursor func(uint64),
		onRevision func(excelPricingRemoteRevision) error,
		onSource func(excelPricingRemoteSourceTransition) error,
		onTerminal func(excelPricingRemoteSnapshotTerminalEvent) error,
		onApply func(excelPricingRemoteApplyTerminalEvent) error,
		onConnected func(context.Context) error,
	) error {
		call := &excelPricingRemoteBridgeTestRun{
			config:        cfg,
			source:        source,
			initialCursor: initialCursor,
			onCursor:      onCursor,
			onRevision:    onRevision,
			onSource:      onSource,
			onTerminal:    onTerminal,
			onApply:       onApply,
			onConnected:   onConnected,
			stopped:       make(chan struct{}),
		}
		select {
		case runs <- call:
		case <-ctx.Done():
			close(call.stopped)
			return ctx.Err()
		}
		<-ctx.Done()
		close(call.stopped)
		return ctx.Err()
	}
}

func TestExcelPricingRemoteEventsBridgeServerCloseCancelsAndJoins(t *testing.T) {
	cfg := excelPricingRemoteBridgeTestConfig("generation-a")
	source := excelPricingRemoteTestSource()
	runs := make(chan *excelPricingRemoteBridgeTestRun, 1)
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config:  func() appconfig.Config { return cfg },
		resolve: excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) {
			return source, nil
		},
		run:   excelPricingRemoteBridgeTestRunner(runs),
		apply: func(excelPricingRemoteRevision) error { return nil },
	})
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	server := &Server{
		backgroundCtx:      backgroundCtx,
		backgroundCancel:   backgroundCancel,
		excelPricingRemote: bridge,
	}
	bridge.start(server.backgroundCtx, &server.backgroundWG)
	call := nextExcelPricingRemoteBridgeTestRun(t, runs)

	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close server: %v", err)
		}
	case <-time.After(excelPricingRemoteBridgeTestTimeout):
		t.Fatal("server Close did not join pricing event subscriber")
	}
	waitExcelPricingRemoteBridgeTestSignal(t, call.stopped, "server-close generation cancellation")
}

func TestExcelPricingRemoteEventsBridgeCoalescesRestartToNewestGeneration(t *testing.T) {
	var cfgMu sync.RWMutex
	cfg := excelPricingRemoteBridgeTestConfig("generation-a")
	source := excelPricingRemoteTestSource()
	runs := make(chan *excelPricingRemoteBridgeTestRun, 4)
	firstCancellation := make(chan struct{})
	releaseFirst := make(chan struct{})
	var runMu sync.Mutex
	runCount := 0
	runner := func(
		ctx context.Context,
		cfg updateout.Config,
		source canonical.Source,
		initialCursor uint64,
		onCursor func(uint64),
		onRevision func(excelPricingRemoteRevision) error,
		onSource func(excelPricingRemoteSourceTransition) error,
		onTerminal func(excelPricingRemoteSnapshotTerminalEvent) error,
		onApply func(excelPricingRemoteApplyTerminalEvent) error,
		onConnected func(context.Context) error,
	) error {
		call := &excelPricingRemoteBridgeTestRun{
			config:        cfg,
			source:        source,
			initialCursor: initialCursor,
			onCursor:      onCursor,
			onRevision:    onRevision,
			onSource:      onSource,
			onTerminal:    onTerminal,
			onApply:       onApply,
			onConnected:   onConnected,
			stopped:       make(chan struct{}),
		}
		runMu.Lock()
		runCount++
		currentRun := runCount
		runMu.Unlock()
		runs <- call
		<-ctx.Done()
		if currentRun == 1 {
			close(firstCancellation)
			<-releaseFirst
		}
		close(call.stopped)
		return ctx.Err()
	}
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config: func() appconfig.Config {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return cfg
		},
		resolve: excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) {
			return source, nil
		},
		run:   runner,
		apply: func(excelPricingRemoteRevision) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	bridge.start(ctx, &waitGroup)
	callOne := nextExcelPricingRemoteBridgeTestRun(t, runs)
	cfgMu.Lock()
	cfg = excelPricingRemoteBridgeTestConfig("generation-b")
	configB := cfg
	cfgMu.Unlock()
	bridge.configChanged(configB)
	waitExcelPricingRemoteBridgeTestSignal(t, firstCancellation, "first restart cancellation")

	cfgMu.Lock()
	cfg = excelPricingRemoteBridgeTestConfig("generation-c")
	configC := cfg
	cfgMu.Unlock()
	bridge.configChanged(configC)
	close(releaseFirst)
	callTwo := nextExcelPricingRemoteBridgeTestRun(t, runs)
	if callTwo.config.URL != "generation-c" {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("restart did not coalesce to newest config: %q", callTwo.config.URL)
	}
	waitExcelPricingRemoteBridgeTestSignal(t, callOne.stopped, "coalesced old generation cancellation")

	cancel()
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
	waitExcelPricingRemoteBridgeTestSignal(t, callTwo.stopped, "coalesced replacement cancellation")
}

func nextExcelPricingRemoteBridgeTestRun(
	t *testing.T,
	runs <-chan *excelPricingRemoteBridgeTestRun,
) *excelPricingRemoteBridgeTestRun {
	t.Helper()
	select {
	case call := <-runs:
		return call
	case <-time.After(excelPricingRemoteBridgeTestTimeout):
		t.Fatal("timed out waiting for pricing event subscriber generation")
		return nil
	}
}

func waitExcelPricingRemoteBridgeTestSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(excelPricingRemoteBridgeTestTimeout):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitExcelPricingRemoteBridgeTestGroup(t *testing.T, waitGroup *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	waitExcelPricingRemoteBridgeTestSignal(t, done, "pricing event subscriber shutdown")
}

func excelPricingRemoteBridgeState(
	bridge *excelPricingRemoteEventsBridge,
) (epoch uint64, cursor uint64, acknowledged bool) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.epoch, bridge.cursor, bridge.acknowledged
}

func TestExcelPricingRemoteEventsBridgeStartsOnlyAfterConfigAndSourceResolve(t *testing.T) {
	var stateMu sync.RWMutex
	cfg := appconfig.Default()
	source := canonical.Source{}
	materializeErr := errors.New("source unavailable")
	materialized := make(chan struct{}, 4)
	runs := make(chan *excelPricingRemoteBridgeTestRun, 4)

	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config: func() appconfig.Config {
			stateMu.RLock()
			defer stateMu.RUnlock()
			return cfg
		},
		resolve: excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) {
			stateMu.RLock()
			currentSource, currentErr := source, materializeErr
			stateMu.RUnlock()
			materialized <- struct{}{}
			return currentSource, currentErr
		},
		run: excelPricingRemoteBridgeTestRunner(runs),
		apply: func(excelPricingRemoteRevision) error {
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	bridge.start(ctx, &waitGroup)
	epoch, _, _ := excelPricingRemoteBridgeState(bridge)
	_, desired := bridge.desiredGeneration()
	if epoch == 0 || desired.valid {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("initial disabled config was not fenced: epoch=%d valid=%v", epoch, desired.valid)
	}

	stateMu.Lock()
	cfg = excelPricingRemoteBridgeTestConfig("generation-a")
	stateMu.Unlock()
	bridge.configChanged(cfg)
	waitExcelPricingRemoteBridgeTestSignal(t, materialized, "canonical materialization attempt")
	select {
	case call := <-runs:
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("subscriber started with an unavailable source: %+v", call.source)
	default:
	}
	// A failed readiness attempt must leave backgroundWG quiescent. Existing
	// startup callers use this wait to join bounded canonical work before Close.
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)

	stateMu.Lock()
	source = excelPricingRemoteTestSource()
	materializeErr = nil
	stateMu.Unlock()
	bridge.sourceChanged()
	call := nextExcelPricingRemoteBridgeTestRun(t, runs)
	if call.source != source || call.initialCursor != 0 {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("unexpected first generation: source=%+v cursor=%d", call.source, call.initialCursor)
	}

	// A second start call must not create another manager or generation.
	bridge.start(ctx, &waitGroup)
	cancel()
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
	waitExcelPricingRemoteBridgeTestSignal(t, call.stopped, "first generation cancellation")
}

func TestExcelPricingRemoteEventsBridgeConfigRestartAndCursorOrdering(t *testing.T) {
	var cfgMu sync.RWMutex
	cfg := excelPricingRemoteBridgeTestConfig("generation-a")
	source := excelPricingRemoteTestSource()
	runs := make(chan *excelPricingRemoteBridgeTestRun, 4)
	var bridge *excelPricingRemoteEventsBridge
	var applyMu sync.Mutex
	applyCalls := 0
	bridge = newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config: func() appconfig.Config {
			cfgMu.RLock()
			defer cfgMu.RUnlock()
			return cfg
		},
		resolve: excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) {
			return source, nil
		},
		run: excelPricingRemoteBridgeTestRunner(runs),
		apply: func(excelPricingRemoteRevision) error {
			applyMu.Lock()
			applyCalls++
			applyMu.Unlock()
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	bridge.start(ctx, &waitGroup)
	callOne := nextExcelPricingRemoteBridgeTestRun(t, runs)
	callOne.onCursor(7)
	_, cursor, acknowledged := excelPricingRemoteBridgeState(bridge)
	if cursor != 0 || acknowledged {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("unacknowledged cursor persisted: cursor=%d acknowledged=%v", cursor, acknowledged)
	}

	revision := excelPricingRemoteRevision{Source: source}
	if err := callOne.onRevision(revision); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("accept revision: %v", err)
	}
	callOne.onCursor(7)
	epoch, cursor, acknowledged := excelPricingRemoteBridgeState(bridge)
	if cursor != 7 || !acknowledged {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("acknowledged cursor was not persisted: cursor=%d acknowledged=%v", cursor, acknowledged)
	}

	bridge.configChanged(cfg)
	sameEpoch, sameCursor, _ := excelPricingRemoteBridgeState(bridge)
	if sameEpoch != epoch || sameCursor != 7 {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("equivalent config restarted generation: before=%d after=%d cursor=%d", epoch, sameEpoch, sameCursor)
	}
	select {
	case <-callOne.stopped:
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatal("equivalent config cancelled the active subscriber")
	default:
	}

	cfgMu.Lock()
	cfg = excelPricingRemoteBridgeTestConfig("generation-b")
	changedConfig := cfg
	cfgMu.Unlock()
	bridge.configChanged(changedConfig)
	callTwo := nextExcelPricingRemoteBridgeTestRun(t, runs)
	waitExcelPricingRemoteBridgeTestSignal(t, callOne.stopped, "old config generation cancellation")
	if callTwo.initialCursor != 0 {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("config restart retained an old cursor: %d", callTwo.initialCursor)
	}
	if err := callOne.onRevision(revision); !errors.Is(err, errExcelPricingRemoteBridgeStale) {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("old config callback was not fenced: %v", err)
	}
	applyMu.Lock()
	gotApplyCalls := applyCalls
	applyMu.Unlock()
	if gotApplyCalls != 1 {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("stale callback reached local invalidation: calls=%d", gotApplyCalls)
	}

	cancel()
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
	waitExcelPricingRemoteBridgeTestSignal(t, callTwo.stopped, "replacement generation cancellation")
}

func TestExcelPricingRemoteEventsBridgeRetainsSnapshotTerminalBeforeCursorAck(t *testing.T) {
	cfg := excelPricingRemoteBridgeTestConfig("generation-terminal")
	source := excelPricingRemoteTestSource()
	runs := make(chan *excelPricingRemoteBridgeTestRun, 1)
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config:      func() appconfig.Config { return cfg },
		resolve:     excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) { return source, nil },
		run:         excelPricingRemoteBridgeTestRunner(runs),
		apply:       func(excelPricingRemoteRevision) error { return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	bridge.start(ctx, &waitGroup)
	call := nextExcelPricingRemoteBridgeTestRun(t, runs)
	stateRevision := excelPricingRemoteTestRevision("b")
	catalogRevision := excelPricingRemoteTestRevision("c")
	revision := excelPricingRemoteRevision{
		Source:                source,
		StateRevision:         stateRevision,
		CatalogRevision:       catalogRevision,
		PricingStateRevision:  excelPricingRemoteTestRevision("d"),
		PricingPolicyRevision: excelPricingRemoteTestRevision("e"),
		ETag:                  `"` + stateRevision + `"`,
	}
	if err := call.onRevision(revision); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("accept revision: %v", err)
	}
	const requestID = "snapshot-terminal-request-0001"
	subscription, err := bridge.snapshotTerminals().Subscribe(requestID, source, stateRevision)
	if err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("subscribe: %v", err)
	}
	defer subscription.Close()
	snapshotRevision := excelPricingRemoteTestRevision("f")
	event := excelPricingRemoteSnapshotTerminalEvent{
		Schema:               excelPricingRemoteSnapshotEventSchema,
		BuildID:              "snapshot-terminal-build-0001",
		RequestID:            requestID,
		Status:               "ready",
		Source:               source,
		StateRevision:        stateRevision,
		PricingStateRevision: revision.PricingStateRevision,
		CatalogRevision:      catalogRevision,
		SnapshotToken:        "snapshot-terminal-token-0001",
		SnapshotRevision:     snapshotRevision,
		Digest:               snapshotRevision,
		SnapshotPath:         "/wp-json/digitalogic/pricing/sync/snapshots/snapshot-terminal-token-0001",
		IdempotencyKey:       excelPricingRemoteTestRevision("0"),
		EventID:              19,
	}
	if err := call.onTerminal(event); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("retain terminal: %v", err)
	}
	call.onCursor(event.EventID)
	received, err := subscription.Wait(t.Context())
	if err != nil || received != event {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("terminal received=%+v err=%v", received, err)
	}
	_, cursor, acknowledged := excelPricingRemoteBridgeState(bridge)
	if cursor != event.EventID || !acknowledged ||
		!bridge.revisionCurrent(source, stateRevision, catalogRevision) {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("terminal cursor=%d acknowledged=%v revision_current=%v",
			cursor, acknowledged, bridge.revisionCurrent(source, stateRevision, catalogRevision))
	}
	cancel()
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
}

func TestExcelPricingRemoteEventsBridgeSourceFenceRemovalAndReset(t *testing.T) {
	var stateMu sync.RWMutex
	cfg := excelPricingRemoteBridgeTestConfig("generation-a")
	source := excelPricingRemoteTestSource()
	materializeErr := error(nil)
	materialized := make(chan error, 8)
	runs := make(chan *excelPricingRemoteBridgeTestRun, 8)
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config: func() appconfig.Config {
			return cfg
		},
		resolve: excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) {
			stateMu.RLock()
			currentSource, currentErr := source, materializeErr
			stateMu.RUnlock()
			materialized <- currentErr
			return currentSource, currentErr
		},
		run: excelPricingRemoteBridgeTestRunner(runs),
		apply: func(excelPricingRemoteRevision) error {
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	bridge.start(ctx, &waitGroup)
	callOne := nextExcelPricingRemoteBridgeTestRun(t, runs)
	<-materialized
	if err := callOne.onRevision(excelPricingRemoteRevision{Source: source}); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatal(err)
	}
	callOne.onCursor(11)

	oldEpoch, _, _ := excelPricingRemoteBridgeState(bridge)
	newEpoch := bridge.fenceSourceChange()
	if newEpoch <= oldEpoch {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("source fence did not advance generation: old=%d new=%d", oldEpoch, newEpoch)
	}
	if err := callOne.onRevision(excelPricingRemoteRevision{Source: source}); !errors.Is(err, errExcelPricingRemoteBridgeStale) {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("source fence did not synchronously reject old callback: %v", err)
	}
	select {
	case <-callOne.stopped:
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatal("source fence restarted before the new source was committed")
	default:
	}

	newSource := canonical.Source{
		ID:       source.ID,
		Dataset:  source.Dataset,
		Revision: excelPricingRemoteTestRevision("b"),
	}
	stateMu.Lock()
	source = newSource
	stateMu.Unlock()
	bridge.commitSourceChange(newEpoch)
	callTwo := nextExcelPricingRemoteBridgeTestRun(t, runs)
	<-materialized
	waitExcelPricingRemoteBridgeTestSignal(t, callOne.stopped, "old source generation cancellation")
	if callTwo.source != newSource || callTwo.initialCursor != 0 {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("unexpected replacement source generation: source=%+v cursor=%d", callTwo.source, callTwo.initialCursor)
	}

	removalEpoch := bridge.fenceSourceChange()
	stateMu.Lock()
	materializeErr = errors.New("source removed")
	stateMu.Unlock()
	bridge.commitSourceChange(removalEpoch)
	select {
	case gotErr := <-materialized:
		if gotErr == nil {
			cancel()
			waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
			t.Fatal("source removal materialized without an error")
		}
	case <-time.After(excelPricingRemoteBridgeTestTimeout):
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatal("timed out waiting for source-removal materialization")
	}
	waitExcelPricingRemoteBridgeTestSignal(t, callTwo.stopped, "removed source generation cancellation")
	if err := callTwo.onRevision(excelPricingRemoteRevision{Source: newSource}); !errors.Is(err, errExcelPricingRemoteBridgeStale) {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("removed source callback was not rejected: %v", err)
	}

	restoredSource := canonical.Source{
		ID:       source.ID,
		Dataset:  source.Dataset,
		Revision: excelPricingRemoteTestRevision("c"),
	}
	stateMu.Lock()
	source = restoredSource
	materializeErr = nil
	stateMu.Unlock()
	bridge.sourceChanged()
	callThree := nextExcelPricingRemoteBridgeTestRun(t, runs)
	<-materialized
	if callThree.source != restoredSource || callThree.initialCursor != 0 {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("unexpected restored source generation: source=%+v cursor=%d", callThree.source, callThree.initialCursor)
	}

	cancel()
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
	waitExcelPricingRemoteBridgeTestSignal(t, callThree.stopped, "restored source cancellation")
}

func TestExcelPricingRemoteEventsBridgeRemoteSourceTransitionsRematerializeAndRestart(t *testing.T) {
	var sourceMu sync.RWMutex
	cfg := excelPricingRemoteBridgeTestConfig("remote-source-transition")
	current := excelPricingRemoteTestSource()
	previousRevision := current.Revision
	runs := make(chan *excelPricingRemoteBridgeTestRun, 4)
	applied := make(chan excelPricingRemoteSourceTransition, 2)
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		config:  func() appconfig.Config { return cfg },
		resolve: excelPricingRemoteBridgeTestResolve,
		materialize: func(context.Context) (canonical.Source, error) {
			sourceMu.RLock()
			defer sourceMu.RUnlock()
			return current, nil
		},
		run: excelPricingRemoteBridgeTestRunner(runs),
		apply: func(excelPricingRemoteRevision) error {
			return nil
		},
		applySource: func(transition excelPricingRemoteSourceTransition) error {
			applied <- transition
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var waitGroup sync.WaitGroup
	bridge.start(ctx, &waitGroup)
	callOne := nextExcelPricingRemoteBridgeTestRun(t, runs)
	initialState := excelPricingRemoteTestRevision("1")
	if err := callOne.onRevision(excelPricingRemoteRevision{
		Source:                current,
		StateRevision:         initialState,
		CatalogRevision:       excelPricingRemoteTestRevision("2"),
		PricingStateRevision:  excelPricingRemoteTestRevision("3"),
		PricingPolicyRevision: excelPricingRemoteTestRevision("4"),
		ETag:                  `"` + initialState + `"`,
	}); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatal(err)
	}

	replacement := current
	replacement.Revision = excelPricingRemoteTestRevision("b")
	sourceMu.Lock()
	current = replacement
	sourceMu.Unlock()
	changed := excelPricingRemoteTestSourceTransition(
		"pricing.source.changed", "changed", replacement,
		&previousRevision,
		12,
	)
	if err := callOne.onSource(changed); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("accept changed source: %v", err)
	}
	if accepted := <-applied; accepted.EventID != changed.EventID || accepted.Source != replacement {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("changed transition = %+v", accepted)
	}
	callTwo := nextExcelPricingRemoteBridgeTestRun(t, runs)
	waitExcelPricingRemoteBridgeTestSignal(t, callOne.stopped, "changed-source subscriber cancellation")
	if callTwo.source != replacement || callTwo.initialCursor != changed.EventID {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("replacement source=%+v cursor=%d", callTwo.source, callTwo.initialCursor)
	}
	replacementState := excelPricingRemoteTestRevision("5")
	replacementCatalog := excelPricingRemoteTestRevision("6")
	if err := callTwo.onRevision(excelPricingRemoteRevision{
		Source:                replacement,
		StateRevision:         replacementState,
		CatalogRevision:       replacementCatalog,
		PricingStateRevision:  excelPricingRemoteTestRevision("7"),
		PricingPolicyRevision: excelPricingRemoteTestRevision("8"),
		ETag:                  `"` + replacementState + `"`,
	}); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("accept replacement authoritative revision: %v", err)
	}
	if !bridge.revisionCurrent(replacement, replacementState, replacementCatalog) {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatal("replacement source was not authoritative after reconnect validation")
	}

	removed := excelPricingRemoteTestSourceTransition(
		"pricing.source.removed", "removed", replacement, &replacement.Revision, 13,
	)
	if err := callTwo.onSource(removed); err != nil {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("accept removed source: %v", err)
	}
	if accepted := <-applied; accepted.EventID != removed.EventID || accepted.Change != "removed" {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("removed transition = %+v", accepted)
	}
	callThree := nextExcelPricingRemoteBridgeTestRun(t, runs)
	waitExcelPricingRemoteBridgeTestSignal(t, callTwo.stopped, "removed-source subscriber cancellation")
	if callThree.source != replacement || callThree.initialCursor != removed.EventID {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("removed-source rematerialization=%+v cursor=%d", callThree.source, callThree.initialCursor)
	}
	_, cursor, acknowledged := excelPricingRemoteBridgeState(bridge)
	if cursor != removed.EventID || !acknowledged ||
		bridge.revisionCurrent(replacement, replacementState, replacementCatalog) {
		cancel()
		waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
		t.Fatalf("removed cursor=%d acknowledged=%v old_revision_current=%v",
			cursor, acknowledged,
			bridge.revisionCurrent(replacement, replacementState, replacementCatalog))
	}

	cancel()
	waitExcelPricingRemoteBridgeTestGroup(t, &waitGroup)
	waitExcelPricingRemoteBridgeTestSignal(t, callThree.stopped, "removed-source reconnect cancellation")
}

func TestExcelPricingRemoteEventsBridgeCursorRequiresSuccessfulApplyAndAllowsReset(t *testing.T) {
	source := excelPricingRemoteTestSource()
	applyErr := errors.New("journal append failed")
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		apply: func(excelPricingRemoteRevision) error {
			return applyErr
		},
	})
	bridge.mu.Lock()
	bridge.epoch = 1
	bridge.mu.Unlock()

	revision := excelPricingRemoteRevision{Source: source}
	if err := bridge.acceptRevision(1, source, revision); !errors.Is(err, applyErr) {
		t.Fatalf("apply failure was not returned: %v", err)
	}
	bridge.persistCursor(1, 9)
	_, cursor, acknowledged := excelPricingRemoteBridgeState(bridge)
	if cursor != 0 || acknowledged {
		t.Fatalf("cursor survived failed local journal append: cursor=%d acknowledged=%v", cursor, acknowledged)
	}

	bridge.dependencies.apply = func(excelPricingRemoteRevision) error { return nil }
	if err := bridge.acceptRevision(1, source, revision); err != nil {
		t.Fatalf("successful apply was rejected: %v", err)
	}
	bridge.persistCursor(1, 9)
	bridge.persistCursor(1, 0)
	_, cursor, acknowledged = excelPricingRemoteBridgeState(bridge)
	if cursor != 0 || !acknowledged {
		t.Fatalf("validated replay reset was not persisted: cursor=%d acknowledged=%v", cursor, acknowledged)
	}
}

func TestExcelPricingRemoteEventsBridgeIdenticalCompositeDoesNotCancelActiveSnapshot(t *testing.T) {
	server := &Server{excelPricing: newExcelPricingState()}
	store := server.excelPricing.snapshots
	source := excelPricingRemoteTestSource()
	revision := excelPricingRemoteRevision{
		Source:                source,
		StateRevision:         excelPricingRemoteTestRevision("1"),
		CatalogRevision:       excelPricingRemoteTestRevision("2"),
		PricingStateRevision:  excelPricingRemoteTestRevision("3"),
		PricingPolicyRevision: excelPricingRemoteTestRevision("4"),
		ETag:                  `"` + excelPricingRemoteTestRevision("1") + `"`,
		Cause:                 "initial_validation",
		IdempotencyKey:        excelPricingRemoteTestRevision("5"),
		EventID:               1,
		ValidationOrigin:      "connected",
	}
	applyCalls := 0
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		apply: func(candidate excelPricingRemoteRevision) error {
			applyCalls++
			return server.notifyExcelPricingRemoteRevisionChanged(candidate)
		},
	})
	bridge.mu.Lock()
	bridge.epoch = 1
	bridge.mu.Unlock()

	store.mu.Lock()
	initialGeneration := store.generation
	store.mu.Unlock()
	if err := bridge.acceptRevision(1, source, revision); err != nil {
		t.Fatalf("accept initial revision: %v", err)
	}
	store.mu.Lock()
	establishedGeneration := store.generation
	establishedEvents := len(store.events)
	store.mu.Unlock()
	if applyCalls != 1 || establishedGeneration != initialGeneration+1 {
		t.Fatalf("initial composite apply_calls=%d generation=%d want=%d",
			applyCalls, establishedGeneration, initialGeneration+1)
	}

	jobContext, cancelJob := context.WithCancel(context.Background())
	defer cancelJob()
	store.mu.Lock()
	store.jobs["active-revision-replay"] = &excelPricingSnapshotJob{
		id:              "active-revision-replay",
		status:          "running",
		startGeneration: store.generation,
		cancel:          cancelJob,
	}
	store.activeJobID = "active-revision-replay"
	store.mu.Unlock()

	replay := revision
	replay.Cause = "reconnect_validation"
	replay.IdempotencyKey = excelPricingRemoteTestRevision("6")
	replay.EventID = 9
	replay.ValidationOrigin = "reconnect"
	if err := bridge.acceptRevision(1, source, replay); err != nil {
		t.Fatalf("accept identical replay: %v", err)
	}
	bridge.persistCursor(1, replay.EventID)
	select {
	case <-jobContext.Done():
		t.Fatal("identical composite replay cancelled the active snapshot")
	default:
	}
	store.mu.Lock()
	replayGeneration := store.generation
	replayEvents := len(store.events)
	replayJobStatus := store.jobs["active-revision-replay"].status
	store.mu.Unlock()
	_, cursor, acknowledged := excelPricingRemoteBridgeState(bridge)
	if applyCalls != 1 || replayGeneration != establishedGeneration ||
		replayEvents != establishedEvents || replayJobStatus != "running" ||
		cursor != replay.EventID || !acknowledged {
		t.Fatalf("identical replay apply_calls=%d generation=%d events=%d job=%s cursor=%d acknowledged=%v",
			applyCalls, replayGeneration, replayEvents, replayJobStatus, cursor, acknowledged)
	}

	changed := replay
	changed.StateRevision = excelPricingRemoteTestRevision("7")
	changed.PricingStateRevision = excelPricingRemoteTestRevision("8")
	changed.ETag = `"` + changed.StateRevision + `"`
	changed.EventID++
	if err := bridge.acceptRevision(1, source, changed); err != nil {
		t.Fatalf("accept changed composite: %v", err)
	}
	select {
	case <-jobContext.Done():
	case <-time.After(excelPricingRemoteBridgeTestTimeout):
		t.Fatal("changed composite did not cancel the active snapshot")
	}
	store.mu.Lock()
	changedGeneration := store.generation
	changedEvents := len(store.events)
	changedJobStatus := store.jobs["active-revision-replay"].status
	store.mu.Unlock()
	if applyCalls != 2 || changedGeneration != establishedGeneration+1 ||
		changedEvents != establishedEvents+2 || changedJobStatus != "cancelling" {
		t.Fatalf("changed composite apply_calls=%d generation=%d events=%d job=%s",
			applyCalls, changedGeneration, changedEvents, changedJobStatus)
	}

	changedReplay := changed
	changedReplay.Cause = "replayed_changed_composite"
	changedReplay.EventID++
	if err := bridge.acceptRevision(1, source, changedReplay); err != nil {
		t.Fatalf("accept changed-composite replay: %v", err)
	}
	store.mu.Lock()
	finalGeneration := store.generation
	finalEvents := len(store.events)
	store.mu.Unlock()
	if applyCalls != 2 || finalGeneration != changedGeneration || finalEvents != changedEvents {
		t.Fatalf("changed replay reapplied: calls=%d generation=%d events=%d",
			applyCalls, finalGeneration, finalEvents)
	}
}

func TestResolveExcelPricingRemoteBridgeConfigTracksOnlySubscriberInputs(t *testing.T) {
	cfg := appconfig.Default()
	cfg.SendUpdates = excelPricingRemoteTestConfig(t, "https://digitalogic.example")
	first := resolveExcelPricingRemoteBridgeConfig(cfg)
	if !first.valid {
		t.Fatal("valid outbound configuration did not resolve")
	}

	unrelated := cfg
	unrelated.Server.Port++
	if got := resolveExcelPricingRemoteBridgeConfig(unrelated); !got.valid || got.key != first.key {
		t.Fatal("unrelated server configuration changed subscriber identity")
	}

	changedProjection := cfg
	changedProjection.Database.RTLConversion = !changedProjection.Database.RTLConversion
	if got := resolveExcelPricingRemoteBridgeConfig(changedProjection); !got.valid || got.key == first.key {
		t.Fatal("canonical projection change did not change subscriber identity")
	}

	t.Setenv("PATRIS_PRICING_EVENTS_TEST_SECRET", "replacement-companion-test-secret")
	if got := resolveExcelPricingRemoteBridgeConfig(cfg); !got.valid || got.key == first.key {
		t.Fatal("credential rotation did not change subscriber identity")
	}

	disabled := cfg
	disabled.SendUpdates.Enabled = false
	if got := resolveExcelPricingRemoteBridgeConfig(disabled); got.valid {
		t.Fatal("disabled outbound configuration resolved as active")
	}
}

func TestExcelPricingRemoteEventsBridgeLogsAreSecretSafe(t *testing.T) {
	const privateEndpoint = "wss://private.example/wordpress-ws"
	const privateSecret = "private-secret-material"
	source := excelPricingRemoteTestSource()
	var logMu sync.Mutex
	var logs []string
	bridge := newExcelPricingRemoteEventsBridgeWithDependencies(excelPricingRemoteBridgeDependencies{
		materialize: func(context.Context) (canonical.Source, error) {
			return source, nil
		},
		run: func(
			context.Context,
			updateout.Config,
			canonical.Source,
			uint64,
			func(uint64),
			func(excelPricingRemoteRevision) error,
			func(excelPricingRemoteSourceTransition) error,
			func(excelPricingRemoteSnapshotTerminalEvent) error,
			func(excelPricingRemoteApplyTerminalEvent) error,
			func(context.Context) error,
		) error {
			return fmt.Errorf("transport rejected %s with %s", privateEndpoint, privateSecret)
		},
		apply: func(excelPricingRemoteRevision) error { return nil },
		logf: func(format string, args ...interface{}) {
			logMu.Lock()
			logs = append(logs, fmt.Sprintf(format, args...))
			logMu.Unlock()
		},
	})
	bridge.mu.Lock()
	bridge.epoch = 1
	bridge.mu.Unlock()
	bridge.runGeneration(context.Background(), 1, updateout.Config{})

	logMu.Lock()
	joined := strings.Join(logs, "\n")
	logMu.Unlock()
	if !strings.Contains(joined, "subscriber stopped before cancellation") {
		t.Fatalf("missing generic lifecycle log: %q", joined)
	}
	for _, private := range []string{privateEndpoint, privateSecret, "private.example"} {
		if strings.Contains(joined, private) {
			t.Fatalf("private subscriber material leaked into logs: %q", joined)
		}
	}
}
