package registry

import "github.com/thetonymaster/mentat/internal/core"

var (
	comparators = map[string]core.Comparator{}
	drivers     = map[string]core.Driver{}
	matchers    = map[string]core.Matcher{}
)

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
