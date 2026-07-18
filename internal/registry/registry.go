package registry

import (
	"sort"
	"sync"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

const sealedMsg = "registry: Register called after engine build — registries are sealed at the composition root"

// StoreFactory builds a TraceStore from config. Stores are stateful (endpoints,
// clients), so the store seam registers factories rather than shared instances.
type StoreFactory func(cfg config.Config) (core.TraceStore, error)

// JudgeFactory builds a Judge from config. Judges are stateful (model clients,
// credentials), so the judge seam registers factories rather than shared instances.
type JudgeFactory func(cfg config.Config) (core.Judge, error)

// Registry is the REGISTRY-OWNERSHIP axis of the seam taxonomy: it owns the six
// name-resolvable seams (drivers, comparators, aggregate comparators, matchers,
// judges, stores) for ONE composed engine. That set is deliberately not the same as
// the public-hook set on the mentat facade — correlators are an engine.Build
// parameter and never a registry entry, reporters are package-global (see below),
// and matchers/aggregate comparators are registry-owned but internal-only. For the
// canonical table of both axes, plus the new-seam checklist, see
// docs/extending/new-seam.md.
//
// engine.Build constructs a fresh Registry per call (via New), registers every seam,
// then Seal()s it — so two Runs never share seam state (spec 007 US2, T010/T011): sequential runs
// cannot leak a custom registration into one another, and concurrent runs cannot race
// a shared map. A single RWMutex guards the maps and a sealed flag (FR-009):
// registration is allowed only while open (during the composition root); once sealed,
// any Register* panics loudly instead of racing a concurrent reader.
type Registry struct {
	mu     sync.RWMutex
	sealed bool

	comparators          map[string]core.Comparator
	aggregateComparators map[string]core.AggregateComparator
	drivers              map[string]core.Driver
	matchers             map[string]core.Matcher
	judges               map[string]JudgeFactory
	stores               map[string]StoreFactory
}

// New returns an empty, open Registry ready for composition-root registration.
func New() *Registry {
	return &Registry{
		comparators:          map[string]core.Comparator{},
		aggregateComparators: map[string]core.AggregateComparator{},
		drivers:              map[string]core.Driver{},
		matchers:             map[string]core.Matcher{},
		judges:               map[string]JudgeFactory{},
		stores:               map[string]StoreFactory{},
	}
}

// Seal closes the registry to further registration. engine.Build and BuildStore call
// it once wiring completes, turning FR-009's build-once discipline from a comment into
// enforced behaviour. Idempotent.
func (r *Registry) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// register is the shared write path: it panics on a sealed registry, else runs set
// under the write lock.
func (r *Registry) register(set func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		panic(sealedMsg)
	}
	set()
}

// RegisterDriver registers a Driver under the given scheme.
func (r *Registry) RegisterDriver(scheme string, d core.Driver) {
	r.register(func() { r.drivers[scheme] = d })
}

// Driver resolves a registered Driver by scheme.
func (r *Registry) Driver(scheme string) (core.Driver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[scheme]
	return d, ok
}

// Drivers returns all registered driver schemes, sorted. engine.Build validates each
// target's adapter against this set — the driver registry is the single runtime source
// of truth for which adapters exist (feature 005, D3/FR-005) — and names the sorted set
// in its rejection error so a phantom adapter is diagnosable.
func (r *Registry) Drivers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.drivers))
	for n := range r.drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RegisterComparator registers a Comparator under the given name.
func (r *Registry) RegisterComparator(name string, c core.Comparator) {
	r.register(func() { r.comparators[name] = c })
}

// Comparator resolves a registered Comparator by name.
func (r *Registry) Comparator(name string) (core.Comparator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.comparators[name]
	return c, ok
}

// Comparators returns all registered comparator names.
func (r *Registry) Comparators() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.comparators))
	for n := range r.comparators {
		names = append(names, n)
	}
	return names
}

// RegisterAggregateComparator registers an AggregateComparator under the given name.
func (r *Registry) RegisterAggregateComparator(name string, c core.AggregateComparator) {
	r.register(func() { r.aggregateComparators[name] = c })
}

// AggregateComparator resolves a registered AggregateComparator by name.
func (r *Registry) AggregateComparator(name string) (core.AggregateComparator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.aggregateComparators[name]
	return c, ok
}

// RegisterMatcher registers a result Matcher under the given name.
func (r *Registry) RegisterMatcher(name string, m core.Matcher) {
	r.register(func() { r.matchers[name] = m })
}

// Matcher resolves a registered Matcher by name.
func (r *Registry) Matcher(name string) (core.Matcher, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.matchers[name]
	return m, ok
}

// RegisterJudge registers a Judge factory under the given name.
func (r *Registry) RegisterJudge(name string, f JudgeFactory) {
	r.register(func() { r.judges[name] = f })
}

// Judge resolves a registered Judge factory by name.
func (r *Registry) Judge(name string) (JudgeFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.judges[name]
	return f, ok
}

// RegisterStore registers a TraceStore factory under the given name.
func (r *Registry) RegisterStore(name string, f StoreFactory) {
	r.register(func() { r.stores[name] = f })
}

// Store resolves a registered TraceStore factory by name.
func (r *Registry) Store(name string) (StoreFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.stores[name]
	return f, ok
}

// --- Reporters: package-global, POST-run rendering seam -----------------------
//
// Reporters (json/html/junit) are a post-run rendering concern: cmd/mentat calls
// report.EmitReports AFTER Run returns Results (not the Engine), so reporters cannot
// be per-engine. They stay package-global under their OWN mutex (reporterMu), never
// sealed — registration is idempotent and never gated by a build seal.
var (
	reporterMu sync.RWMutex
	reporters  = map[string]core.Reporter{}
)

// RegisterReporter registers a Reporter under the given name.
func RegisterReporter(name string, r core.Reporter) {
	reporterMu.Lock()
	defer reporterMu.Unlock()
	reporters[name] = r
}

// Reporter resolves a registered Reporter by name.
func Reporter(name string) (core.Reporter, bool) {
	reporterMu.RLock()
	defer reporterMu.RUnlock()
	r, ok := reporters[name]
	return r, ok
}
