package steps

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// stepSpy is a stepRegistrar stand-in that records every (pattern, handler) pair
// the registration path emits. It observes the LIVE registration — the exact same
// registerSteps call the InitializerWithCollector closure delegates to — so the
// drift test compares what is really registered against the metadata table, not a
// re-read of the table itself.
type stepSpy struct {
	patterns []string
	nilFuncs []string
}

func (s *stepSpy) Step(expr, stepFunc any) {
	p, ok := expr.(string)
	if !ok {
		// The metadata table registers string patterns; anything else is a defect.
		s.patterns = append(s.patterns, "<non-string pattern>")
		return
	}
	s.patterns = append(s.patterns, p)
	if stepFunc == nil {
		s.nilFuncs = append(s.nilFuncs, p)
	}
}

// registeredPatterns drives the real registerSteps path with a spy and returns the
// patterns it emitted, plus any pattern registered with a nil handler.
func registeredPatterns(t *testing.T) (patterns []string, nilFuncs []string) {
	t.Helper()
	// A zero world is sufficient: registerSteps only binds method values
	// (w.method), it never invokes them, so no engine/ctx is needed.
	spy := &stepSpy{}
	registerSteps(spy, &world{})
	return spy.patterns, spy.nilFuncs
}

func tablePatterns() []string {
	out := make([]string, 0, len(stepDefs))
	for _, sd := range stepDefs {
		out = append(out, sd.pattern)
	}
	return out
}

// TestStepMetadataMatchesRegistration is the drift guard (US1, E1): every pattern
// the registration path emits must have a metadata entry and vice-versa, and the
// two counts must match. It asserts against the LIVE registration (via the spy),
// not a hardcoded 37, so the invariant holds as steps are added or removed.
func TestStepMetadataMatchesRegistration(t *testing.T) {
	t.Parallel()

	registered, nilFuncs := registeredPatterns(t)
	table := tablePatterns()

	if len(nilFuncs) > 0 {
		t.Errorf("patterns registered with a nil handler: %v", nilFuncs)
	}

	regSet := toSet(registered)
	tabSet := toSet(table)

	// Duplicate registration would make godog ambiguous and hide a drift.
	if len(regSet) != len(registered) {
		t.Errorf("duplicate pattern registrations: %d calls, %d distinct", len(registered), len(regSet))
	}
	if len(tabSet) != len(table) {
		t.Errorf("duplicate metadata entries: %d entries, %d distinct patterns", len(table), len(tabSet))
	}

	for _, p := range sortedKeys(regSet) {
		if _, ok := tabSet[p]; !ok {
			t.Errorf("registered pattern has no metadata entry: %q", p)
		}
	}
	for _, p := range sortedKeys(tabSet) {
		if _, ok := regSet[p]; !ok {
			t.Errorf("metadata entry is never registered: %q", p)
		}
	}

	if len(registered) != len(stepDefs) {
		t.Errorf("count mismatch: %d steps registered, %d metadata entries", len(registered), len(stepDefs))
	}
}

// TestStepMetadataFieldsPresent proves no blank documentation slips through: every
// entry carries a non-empty pattern, summary, and example plus a non-nil handler.
func TestStepMetadataFieldsPresent(t *testing.T) {
	t.Parallel()

	if len(stepDefs) == 0 {
		t.Fatal("stepDefs is empty; the metadata table must document every step")
	}
	for _, sd := range stepDefs {
		if strings.TrimSpace(sd.pattern) == "" {
			t.Errorf("metadata entry has an empty pattern (summary=%q)", sd.summary)
			continue
		}
		if strings.TrimSpace(sd.group) == "" {
			t.Errorf("metadata entry %q has an empty group", sd.pattern)
		}
		if strings.TrimSpace(sd.summary) == "" {
			t.Errorf("metadata entry %q has an empty summary", sd.pattern)
		}
		if strings.TrimSpace(sd.example) == "" {
			t.Errorf("metadata entry %q has an empty example", sd.pattern)
		}
		if sd.handler == nil {
			t.Errorf("metadata entry %q has a nil handler", sd.pattern)
			continue
		}
		if sd.handler(&world{}) == nil {
			t.Errorf("metadata entry %q binds to a nil handler value", sd.pattern)
		}
	}
}

// TestNoDirectStepRegistration closes the drift hole the spy alone cannot: it fails
// if steps.go registers any step directly via sc.Step( instead of through the
// metadata table. All registration must flow through registerSteps so the table
// stays the single source of truth.
func TestNoDirectStepRegistration(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("steps.go")
	if err != nil {
		t.Fatalf("read steps.go: %v", err)
	}
	if strings.Contains(string(data), "sc.Step(") {
		t.Error("steps.go calls sc.Step( directly; register every step through the metadata table (registerSteps)")
	}
}

func toSet(xs []string) map[string]struct{} {
	m := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		m[x] = struct{}{}
	}
	return m
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
