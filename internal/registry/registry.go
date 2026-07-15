package registry

import (
	"sort"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// The seam registries are package-global maps. A single RWMutex guards them and a
// sealed flag (feature 003, FR-009): registration is allowed only while the
// registry is open (during the composition root, engine.Build/BuildStore); once
// sealed, any Register* panics loudly instead of racing a concurrent reader. The
// mutex removes the audit-B5 data race even in the pre-seal window.
var (
	mu     sync.RWMutex
	sealed bool

	comparators          = map[string]core.Comparator{}
	aggregateComparators = map[string]core.AggregateComparator{}
	drivers              = map[string]core.Driver{}
	matchers             = map[string]core.Matcher{}
	reporters            = map[string]core.Reporter{}
)

const sealedMsg = "registry: Register called after engine build — registries are sealed at the composition root"

// Seal closes the registry to further registration. engine.Build and BuildStore
// call it once wiring completes, turning FR-009's build-once discipline from a
// comment into enforced behaviour. Idempotent.
func Seal() {
	mu.Lock()
	sealed = true
	mu.Unlock()
}

// Reopen re-opens the registry so the composition root can (re)wire it. engine.Build
// and BuildStore call it at the start so they are re-entrant (tests build many
// engines); ResetForTest calls it too. Idempotent.
func Reopen() {
	mu.Lock()
	sealed = false
	mu.Unlock()
}

// ResetForTest re-opens the registry for a test that registers custom seams outside
// the composition root. It requires a non-nil *testing.T so it can only be reached
// from test code, never from a production path.
func ResetForTest(t *testing.T) {
	if t == nil {
		panic("registry: ResetForTest requires a non-nil *testing.T")
	}
	Reopen()
}

// registerLocked is the shared write path: it panics on a sealed registry, else
// runs set under the write lock.
func registerLocked(set func()) {
	mu.Lock()
	defer mu.Unlock()
	if sealed {
		panic(sealedMsg)
	}
	set()
}

// resolve is the shared read path for the seam resolvers: it reads m[name] under
// the read lock so a lookup never races a composition-root registration.
func resolve[V any](m map[string]V, name string) (V, bool) {
	mu.RLock()
	defer mu.RUnlock()
	v, ok := m[name]
	return v, ok
}

// StoreFactory builds a TraceStore from config. Stores are stateful (endpoints,
// clients), so the store seam registers factories rather than shared instances.
type StoreFactory func(cfg config.Config) (core.TraceStore, error)

var stores = map[string]StoreFactory{}

// RegisterStore registers a TraceStore factory under the given name.
func RegisterStore(name string, f StoreFactory) { registerLocked(func() { stores[name] = f }) }

// Store resolves a registered TraceStore factory by name.
func Store(name string) (StoreFactory, bool) { return resolve(stores, name) }

// JudgeFactory builds a Judge from config. Judges are stateful (model clients,
// credentials), so the judge seam registers factories rather than shared instances.
type JudgeFactory func(cfg config.Config) (core.Judge, error)

var judges = map[string]JudgeFactory{}

// RegisterJudge registers a Judge factory under the given name.
func RegisterJudge(name string, f JudgeFactory) { registerLocked(func() { judges[name] = f }) }

// Judge resolves a registered Judge factory by name.
func Judge(name string) (JudgeFactory, bool) { return resolve(judges, name) }

// RegisterComparator registers a Comparator under the given name.
func RegisterComparator(name string, c core.Comparator) {
	registerLocked(func() { comparators[name] = c })
}

// Comparator resolves a registered Comparator by name.
func Comparator(name string) (core.Comparator, bool) { return resolve(comparators, name) }

// RegisterAggregateComparator registers an AggregateComparator under the given name.
func RegisterAggregateComparator(name string, c core.AggregateComparator) {
	registerLocked(func() { aggregateComparators[name] = c })
}

// AggregateComparator resolves a registered AggregateComparator by name.
func AggregateComparator(name string) (core.AggregateComparator, bool) {
	return resolve(aggregateComparators, name)
}

// Comparators returns all registered comparator names.
func Comparators() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(comparators))
	for n := range comparators {
		names = append(names, n)
	}
	return names
}

// Drivers returns all registered driver schemes, sorted. engine.Build validates
// each target's adapter against this set — the driver registry is the single
// runtime source of truth for which adapters exist (feature 005, D3/FR-005) — and
// names the sorted set in its rejection error so a phantom adapter is diagnosable.
func Drivers() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(drivers))
	for n := range drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RegisterDriver registers a Driver under the given scheme.
func RegisterDriver(scheme string, d core.Driver) { registerLocked(func() { drivers[scheme] = d }) }

// Driver resolves a registered Driver by scheme.
func Driver(scheme string) (core.Driver, bool) { return resolve(drivers, scheme) }

// RegisterMatcher registers a result Matcher under the given name.
func RegisterMatcher(name string, m core.Matcher) { registerLocked(func() { matchers[name] = m }) }

// Matcher resolves a registered Matcher by name.
func Matcher(name string) (core.Matcher, bool) { return resolve(matchers, name) }

// RegisterReporter registers a Reporter under the given name.
func RegisterReporter(name string, r core.Reporter) { registerLocked(func() { reporters[name] = r }) }

// Reporter resolves a registered Reporter by name.
func Reporter(name string) (core.Reporter, bool) { return resolve(reporters, name) }
