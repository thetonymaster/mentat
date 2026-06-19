package registry

import (
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// Stateless seams register instances; the stateful store seam registers factories (see StoreFactory).
var (
	comparators = map[string]core.Comparator{}
	drivers     = map[string]core.Driver{}
	matchers    = map[string]core.Matcher{}
)

// StoreFactory builds a TraceStore from config. Stores are stateful (endpoints,
// clients), so the store seam registers factories rather than shared instances.
type StoreFactory func(cfg config.Config) (core.TraceStore, error)

var stores = map[string]StoreFactory{}

// RegisterStore registers a TraceStore factory under the given name.
func RegisterStore(name string, f StoreFactory) { stores[name] = f }

// Store resolves a registered TraceStore factory by name.
func Store(name string) (StoreFactory, bool) { f, ok := stores[name]; return f, ok }

// RegisterComparator registers a Comparator under the given name.
func RegisterComparator(name string, c core.Comparator) { comparators[name] = c }

// Comparator resolves a registered Comparator by name.
func Comparator(name string) (core.Comparator, bool) { c, ok := comparators[name]; return c, ok }

// Comparators returns all registered comparator names.
func Comparators() []string {
	names := make([]string, 0, len(comparators))
	for n := range comparators {
		names = append(names, n)
	}
	return names
}

// RegisterDriver registers a Driver under the given scheme.
func RegisterDriver(scheme string, d core.Driver) { drivers[scheme] = d }

// Driver resolves a registered Driver by scheme.
func Driver(scheme string) (core.Driver, bool) { d, ok := drivers[scheme]; return d, ok }

// RegisterMatcher registers a result Matcher under the given name.
func RegisterMatcher(name string, m core.Matcher) { matchers[name] = m }

// Matcher resolves a registered Matcher by name.
func Matcher(name string) (core.Matcher, bool) { m, ok := matchers[name]; return m, ok }
