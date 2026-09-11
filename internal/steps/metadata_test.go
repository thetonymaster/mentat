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
func registeredPatterns(t *testing.T, phrases ...phraseStep) (patterns []string, nilFuncs []string) {
	t.Helper()
	// A zero world is sufficient: registerSteps only binds method values
	// (w.method), it never invokes them, so no engine/ctx is needed.
	//
	// That zero world is also WHY registerSteps takes its contributed phrases as a
	// parameter. w.eng is nil here, so reaching through it for phrases would panic,
	// and nil-guarding it would be exactly the silent fallback No Silent Fallbacks
	// forbids: the drift test would then be asserting over an empty set while
	// believing it covered the real one.
	spy := &stepSpy{}
	registerSteps(spy, &world{}, phrases)
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

// TestStepRegistrationIsAPartition is the drift gate generalised for 012.
//
// The gate is NOT relaxed into "registration may be a superset of the table". That
// would surrender exactly what stepDefs is for. It becomes a PARTITION:
//
//	every registered pattern is EITHER a stepDefs row OR a member of the contributed
//	set supplied for this engine — and the counts add up.
//
// An unaccounted registered pattern is still a hard failure. The gate loses no
// strength; it gains a second ACCOUNTED source.
//
// Both halves are asserted, because each fails differently: a built-in going missing
// is a regression in the table, while a contributed phrase going missing means the
// engine's own comparators were silently ignored.
//
// Mutation rehearsals (2026-09-11), naming what was mutated:
//
//	A. Deleted the contributed-phrase registration loop from registerSteps
//	   (`_ = phrases`). RED: "0 contributed patterns registered, want 2" and the
//	   count mismatch. This is the mutation that matters — a partition test that
//	   could not detect the contributed half going missing would be strictly weaker
//	   than the bidirectional equality it replaced.
//	B. Swapped the two loops so phrases register first. RED in
//	   TestBuiltinsRegisterBeforeContributedPhrases: "a contributed phrase registered
//	   at index 0, before the last built-in at 40".
//
// Both reverted; tests re-observed green.
func TestStepRegistrationIsAPartition(t *testing.T) {
	t.Parallel()

	contributed := []phraseStep{
		{pattern: `^the revenue is shaped like a (\w+) report$`, handler: func(string) error { return nil }},
		{pattern: `^the ledger balances$`, handler: func() error { return nil }},
	}

	registered, nilFuncs := registeredPatterns(t, contributed...)
	if len(nilFuncs) > 0 {
		t.Errorf("patterns registered with a nil handler: %v", nilFuncs)
	}

	builtin := toSet(tablePatterns())
	contrib := make(map[string]struct{}, len(contributed))
	for _, p := range contributed {
		contrib[p.pattern] = struct{}{}
	}

	var fromBuiltin, fromContrib int
	for _, p := range registered {
		_, isBuiltin := builtin[p]
		_, isContrib := contrib[p]
		switch {
		case isBuiltin && isContrib:
			t.Errorf("pattern %q is BOTH a stepDefs row and a contributed phrase; the two sources must be disjoint or the partition is meaningless", p)
		case isBuiltin:
			fromBuiltin++
		case isContrib:
			fromContrib++
		default:
			t.Errorf("registered pattern belongs to neither source: %q", p)
		}
	}

	if fromBuiltin != len(stepDefs) {
		t.Errorf("%d built-in patterns registered, want %d", fromBuiltin, len(stepDefs))
	}
	if fromContrib != len(contributed) {
		t.Errorf("%d contributed patterns registered, want %d", fromContrib, len(contributed))
	}
	if len(registered) != len(stepDefs)+len(contributed) {
		t.Errorf("count mismatch: %d registered, want %d built-in + %d contributed", len(registered), len(stepDefs), len(contributed))
	}
}

// TestBuiltinsRegisterBeforeContributedPhrases pins the ordering rule collision
// resolution depends on. The runner returns the FIRST matching step definition, so a
// contributed phrase colliding with a built-in must never be able to displace it.
//
// Under Strict the collision surfaces as an ambiguity failure rather than the silent
// shadowing that would otherwise occur — but the ordering is what decides which way
// a non-strict runner would resolve it, so it is asserted directly rather than left
// to the flag.
func TestBuiltinsRegisterBeforeContributedPhrases(t *testing.T) {
	t.Parallel()

	contributed := []phraseStep{
		{pattern: `^the contributed phrase runs$`, handler: func() error { return nil }},
	}
	registered, _ := registeredPatterns(t, contributed...)

	builtin := toSet(tablePatterns())
	lastBuiltin, firstContrib := -1, -1
	for i, p := range registered {
		if _, ok := builtin[p]; ok {
			lastBuiltin = i
			continue
		}
		if firstContrib == -1 {
			firstContrib = i
		}
	}
	if firstContrib == -1 {
		t.Fatalf("no contributed pattern was registered at all: %v", registered)
	}
	if lastBuiltin > firstContrib {
		t.Errorf("a contributed phrase registered at index %d, before the last built-in at %d; built-ins must register first so a colliding phrase cannot displace one",
			firstContrib, lastBuiltin)
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
