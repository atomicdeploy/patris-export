package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/recordmap"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

var errCanonicalProjectionUnavailable = errors.New("canonical projection is unavailable")

type canonicalProjectionBuild struct {
	generation  uint64
	done        chan struct{}
	invalidated chan struct{}
	cancel      context.CancelFunc
	result      recordpipe.Result
	err         error
}

type cachedCanonicalProjection struct {
	result    recordpipe.Result
	createdAt time.Time
}

// canonicalProjectionCache retains one immutable canonical projection until an
// authoritative source, configuration, or pricing event invalidates it. The
// configured pricing max-stale window remains an absolute fail-closed bound;
// it is not extended by reads and it never permits stale-on-error fallback.
type canonicalProjectionCache struct {
	mu         sync.Mutex
	generation uint64
	cached     *cachedCanonicalProjection
	inFlight   *canonicalProjectionBuild
	now        func() time.Time
}

func newCanonicalProjectionCache() *canonicalProjectionCache {
	return &canonicalProjectionCache{now: time.Now}
}

func (cache *canonicalProjectionCache) invalidate() {
	cache.invalidateWith(nil)
}

func (cache *canonicalProjectionCache) invalidateWith(fencedAction func()) {
	if cache == nil {
		if fencedAction != nil {
			fencedAction()
		}
		return
	}
	cache.mu.Lock()
	cache.generation++
	cache.cached = nil
	if cache.inFlight != nil {
		cache.inFlight.cancel()
		close(cache.inFlight.invalidated)
	}
	cache.inFlight = nil
	if fencedAction != nil {
		fencedAction()
	}
	cache.mu.Unlock()
}

func (cache *canonicalProjectionCache) get(
	ctx context.Context,
	maxAge func() time.Duration,
	build func(context.Context) (recordpipe.Result, error),
) (recordpipe.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cache == nil || maxAge == nil || build == nil {
		return recordpipe.Result{}, errCanonicalProjectionUnavailable
	}

	for {
		if err := ctx.Err(); err != nil {
			return recordpipe.Result{}, err
		}
		maximum := maxAge()
		if maximum <= 0 {
			return build(ctx)
		}

		cache.mu.Lock()
		now := cache.now().UTC()
		if cache.cached != nil && canonicalProjectionWithinAge(now, cache.cached.createdAt, maximum) {
			result := cloneCanonicalProjection(cache.cached.result)
			cache.mu.Unlock()
			return result, nil
		}
		generation := cache.generation
		if active := cache.inFlight; active != nil && active.generation == generation {
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return recordpipe.Result{}, ctx.Err()
			case <-active.invalidated:
				continue
			case <-active.done:
			}
			cache.mu.Lock()
			current := cache.generation == generation
			result := cloneCanonicalProjection(active.result)
			err := active.err
			cache.mu.Unlock()
			if !current {
				continue
			}
			if err != nil {
				if ctx.Err() == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
					continue
				}
				return recordpipe.Result{}, err
			}
			if !validCachedCanonicalProjection(result) {
				return recordpipe.Result{}, errCanonicalProjectionUnavailable
			}
			return result, nil
		}

		buildContext, cancel := context.WithCancel(ctx)
		active := &canonicalProjectionBuild{
			generation:  generation,
			done:        make(chan struct{}),
			invalidated: make(chan struct{}),
			cancel:      cancel,
		}
		cache.inFlight = active
		cache.mu.Unlock()

		result, err := build(buildContext)
		cancel()
		if err == nil && !validCachedCanonicalProjection(result) {
			err = errCanonicalProjectionUnavailable
		}
		result = cloneCanonicalProjection(result)

		cache.mu.Lock()
		current := cache.generation == generation
		active.result = result
		active.err = err
		if current && err == nil {
			maximum = maxAge()
			if maximum > 0 {
				cache.cached = &cachedCanonicalProjection{
					result:    cloneCanonicalProjection(result),
					createdAt: cache.now().UTC(),
				}
			}
		}
		if cache.inFlight == active {
			cache.inFlight = nil
		}
		close(active.done)
		cache.mu.Unlock()

		if !current {
			continue
		}
		if err != nil {
			return recordpipe.Result{}, err
		}
		return cloneCanonicalProjection(result), nil
	}
}

func canonicalProjectionWithinAge(now, createdAt time.Time, maximum time.Duration) bool {
	if createdAt.IsZero() || maximum <= 0 {
		return false
	}
	age := now.Sub(createdAt)
	return age >= 0 && age <= maximum
}

func cloneCanonicalProjection(result recordpipe.Result) recordpipe.Result {
	contract := result.SyncEnvelope(nil)
	return recordpipe.Result{
		Rows:     recordmap.CopyRows(result.Rows),
		Payload:  contract,
		KeyField: result.KeyField,
		Raw:      result.Raw,
		Contract: contract,
	}
}

func canonicalProjectionMaxAge(cfg appconfig.Config) time.Duration {
	if !strings.EqualFold(strings.TrimSpace(cfg.Canonical.Pricing.Mode), "digitalogic") {
		return 0
	}
	maximum, err := time.ParseDuration(strings.TrimSpace(cfg.Canonical.Pricing.Digitalogic.MaxStale))
	if err != nil || maximum <= 0 {
		return 0
	}
	return maximum
}

func (s *Server) invalidateCanonicalProjection(resetPricingProvider bool) {
	if s == nil {
		return
	}
	reset := func() {
		if !resetPricingProvider {
			return
		}
		s.catalogProviderMu.Lock()
		s.catalogProvider = nil
		s.catalogProviderKey = ""
		s.catalogProviderMu.Unlock()
	}
	if s.canonicalProjection != nil {
		s.canonicalProjection.invalidateWith(reset)
		return
	}
	reset()
}

func validCachedCanonicalProjection(result recordpipe.Result) bool {
	return result.Contract != nil && result.Contract.Schema == canonical.ContractName
}
