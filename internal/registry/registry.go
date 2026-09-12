package registry

import (
	"sort"
	"sync"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/result"
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
// a shared map. The SEALED FLAG is what delivers that (FR-009), not the mutex: every
// Register* call site is inside the composition root (engine.Build / BuildStore) and
// runs before Seal, and godog's scenario goroutines start only after Build returns, so
// no production path registers concurrently with a read. Once sealed, any Register*
// panics loudly instead of mutating a map a reader may hold. The RWMutex guards no
// production race today; it is cheap insurance for that ordering being broken later,
// and for tests that register off the composition root.
type Registry struct {
	mu     sync.RWMutex
	sealed bool

	comparators          map[string]core.Comparator
	aggregateComparators map[string]core.AggregateComparator
	drivers              map[string]core.Driver
	matchers             map[string]core.Matcher
	judges               map[string]JudgeFactory
	stores               map[string]StoreFactory
	reporters            map[string]result.Reporter
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
		reporters:            map[string]result.Reporter{},
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

// Comparators returns the registered comparator names in sorted order, so an unknown-
// name error can name the alternatives instead of just rejecting the input — the same
// contract Reporters has.
//
// The sort is load-bearing rather than cosmetic: feature 011's Gherkin step renders
// this list into its unknown-comparator error, and map iteration order would reorder
// that message between runs of an unchanged suite.
func (r *Registry) Comparators() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.comparators))
	for n := range r.comparators {
		names = append(names, n)
	}
	sort.Strings(names)
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

// --- Reporters: per-engine, like every other seam -----------------------------
//
// Until feature 010 reporters lived in a package-GLOBAL map under their own mutex,
// never sealed. The justification was that emission happened in the CLI after Run had
// returned — holding results, not an Engine, and so having no registry to consult. That
// call path stopped existing at the 007 recompose, which moved emission inside Run,
// where the engine is still in scope; the reason went stale and the global outlived it.
//
// The global had to go before WithReporter could exist: a per-run registration option
// writing into shared state is exactly the reentrancy defect T010/T011 closed for the
// other seams, and no seal would have caught it. Two concurrent Runs registering
// different reporters under the same name would have raced, then silently used each
// other's.

// RegisterReporter registers a Reporter under the given name. Panics if the registry
// is already sealed.
func (r *Registry) RegisterReporter(name string, rep result.Reporter) {
	r.register(func() { r.reporters[name] = rep })
}

// Reporter resolves a registered Reporter by name.
func (r *Registry) Reporter(name string) (result.Reporter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rep, ok := r.reporters[name]
	return rep, ok
}

// Reporters returns the registered reporter names in sorted order, so an unknown-name
// error can name the alternatives instead of just rejecting the input.
func (r *Registry) Reporters() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.reporters))
	for n := range r.reporters {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
