package steps

import (
	"strconv"
	"strings"
	"testing"

	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
)

// stubPrecheckEngine satisfies PrecheckEngine with real cel/aggregate-cel
// comparators (constructed directly, no registry) and an in-memory pattern map —
// so the precheck functions can be exercised hermetically and in parallel.
type stubPrecheckEngine struct {
	pats map[string][]comparator.ShapeExpectation
}

func (s stubPrecheckEngine) Comparator(name string) (core.Comparator, bool) {
	if name == "cel" {
		return comparator.NewCEL(nil), true
	}
	return nil, false
}

func (s stubPrecheckEngine) AggregateComparator(name string) (core.AggregateComparator, bool) {
	if name == "aggregate-cel" {
		return comparator.NewAggregateCEL(nil), true
	}
	return nil, false
}

func (s stubPrecheckEngine) ShapePattern(name string) ([]comparator.ShapeExpectation, bool) {
	p, ok := s.pats[name]
	return p, ok
}

var _ PrecheckEngine = stubPrecheckEngine{}

func pstep(text string) *messages.PickleStep {
	return &messages.PickleStep{Text: text, AstNodeIds: []string{"n1"}}
}

func lineSrc(file string, line int) Source {
	return Source{File: file, Line: func(string) int { return line }}
}

// TestStepBindingFindingsIsPerPatternSet proves step-binding checks are a function of
// the pattern set handed to them, not of package state.
//
// This is the assertion the deleted sync.Once cache (precheck.go's stepPatternsOnce)
// could not satisfy: it fixed the first-compiled pattern set for the life of the
// process, so whichever set was evaluated FIRST would answer for every later one.
//
// Both orders are exercised deliberately. A first-writer-wins cache passes one ordering
// and fails the other, so a single-order test would have reported the bug as fixed
// roughly half the time — the same defect class 007 closed for registries
// (mentat_run_reentrancy_test.go exists because it already bit once).
//
// It matters now rather than in the abstract: with comparator-contributed phrases the
// pattern set differs PER ENGINE, so a second engine in one process would be validated
// against the first engine's phrases and report a valid feature file as broken.
//
// Mutation rehearsal (2026-09-11): inserted `pats = BuiltinStepPatterns()` as the first
// statement of StepBindingFindings, so the argument is accepted and then discarded —
// the shape the sync.Once cache had. RED in both orderings, all four rows reporting
// unbound=true against their own set. Reverted; re-observed green.
func TestStepBindingFindingsIsPerPatternSet(t *testing.T) {
	t.Parallel()

	setA := mustCompileSet(t, `^alpha happens$`)
	setB := mustCompileSet(t, `^beta happens$`)

	// Each set binds its own sentence and rejects the other's. Nothing is shared.
	cases := []struct {
		set         StepPatterns
		text        string
		wantUnbound bool
	}{
		{setA, "alpha happens", false},
		{setA, "beta happens", true},
		{setB, "beta happens", false},
		{setB, "alpha happens", true},
	}

	check := func(t *testing.T, order string) {
		t.Helper()
		for _, c := range cases {
			got := StepBindingFindings(c.set, []*messages.PickleStep{pstep(c.text)}, lineSrc("f.feature", 1))
			unbound := len(got) > 0
			if unbound != c.wantUnbound {
				t.Errorf("[%s] text %q against its set: unbound=%v, want %v (findings: %+v)",
					order, c.text, unbound, c.wantUnbound, got)
			}
		}
	}

	// Evaluate A before B, then B before A. Under a package-level cache the second
	// ordering returns the first ordering's answers.
	t.Run("A then B", func(t *testing.T) { check(t, "A then B") })
	t.Run("B then A", func(t *testing.T) {
		for i := len(cases)/2 - 1; i >= 0; i-- {
			cases[i], cases[len(cases)-1-i] = cases[len(cases)-1-i], cases[i]
		}
		check(t, "B then A")
	})
}

func mustCompileSet(t *testing.T, patterns ...string) StepPatterns {
	t.Helper()
	set, err := CompileStepPatterns(patterns)
	if err != nil {
		t.Fatalf("CompileStepPatterns(%q): %v", patterns, err)
	}
	return set
}

// TestStepBindingFindings pins the THREE-WAY classification of "how many step
// definitions match this sentence": 0 => unbound-step, 1 => nothing, >1 =>
// ambiguous-step.
//
// The three answers are asserted in one table on purpose. They are one exhaustive
// classification of a single count, and splitting them across two functions is what
// let Validate certify a suite as clean while Run refused it as ambiguous.
//
// Ordering rows: "two matches, broad first" and "two matches, specific first" carry
// the SAME two patterns in opposite pattern-set order and demand the message follow
// that order. Matching expressions must line up with godog's own matchingExpressions
// list, which is built by iterating registered definitions in registration order
// (godog suite.go:511-556).
//
// # Mutation rehearsals (2026-09-11)
//
// The mutation is recorded, not just the fact that red occurred: 011 hit a rehearsal
// that stayed green because the MUTATION had not applied, and "the mutation didn't
// fire" is indistinguishable from "the guard is real" from output alone. Each was
// grepped out of precheck.go after editing to confirm it had landed, then reverted
// and the suite re-observed green.
//
//  1. `len(matched) > 1` -> `len(matched) > 2` in StepBindingFindings. RED on both
//     two-match rows ("want 1 finding, got 0") and on
//     TestStepBindingFindingsAgreesWithTheRunnerOnAmbiguity. The three-match row
//     stayed GREEN (3 > 2), which is why the two-match rows carry the guard and the
//     three-match row alone would not have caught this.
//
//  2. `slices.Sort(matched)` inserted before the message is built. RED on "two
//     matches, specific first" AND on "three matches are all named" — sorting puts
//     both `(`-prefixed patterns ahead of `^the widget is green$`. "Two matches,
//     broad first" stayed GREEN, because sorted order and registration order
//     coincide there. That asymmetry is the row pair's entire reason for existing:
//     one ordering alone cannot tell a sorted implementation from an
//     order-preserving one.
//
//     Recorded as measured rather than as predicted: the prediction was that only
//     the specific-first row would fire, and the three-match row firing too was not
//     foreseen.
func TestStepBindingFindings(t *testing.T) {
	t.Parallel()

	// Two patterns that both match ambiguousStepText and no built-in (see
	// TestBuiltinStepPatternsArePairwiseDisjoint). Reused from the runtime ambiguity
	// test so static and runtime assertions are about the same collision.
	const alsoMatching = `^the widget is (green|red)$`

	tests := []struct {
		name string
		// pats is the pattern set this row binds against; nil means the built-ins.
		pats      []string
		text      string
		wantClass string // "" => no finding
		// wantSubs must appear in the message in this order.
		wantSubs []string
	}{
		{name: "bound target step", text: `the agent target "researchbot"`},
		{name: "bound cel step", text: `the run satisfies "tokens < 5000"`},
		{name: "bound shape step", text: `the run matches shape "flow"`},
		{
			name:      "unbound gibberish",
			text:      `the moon is made of cheese`,
			wantClass: "unbound-step",
			wantSubs:  []string{`no step matches "the moon is made of cheese"`},
		},
		{
			name: "exactly one match is not a finding",
			pats: []string{ambiguousBroadPattern},
			text: ambiguousStepText,
		},
		{
			name:      "two matches, broad first",
			pats:      []string{ambiguousBroadPattern, ambiguousSpecificPattern},
			text:      ambiguousStepText,
			wantClass: "ambiguous-step",
			wantSubs: []string{
				strconv.Quote(ambiguousStepText),
				strconv.Quote(ambiguousBroadPattern),
				strconv.Quote(ambiguousSpecificPattern),
			},
		},
		{
			name:      "two matches, specific first",
			pats:      []string{ambiguousSpecificPattern, ambiguousBroadPattern},
			text:      ambiguousStepText,
			wantClass: "ambiguous-step",
			wantSubs: []string{
				strconv.Quote(ambiguousSpecificPattern),
				strconv.Quote(ambiguousBroadPattern),
			},
		},
		{
			name:      "three matches are all named",
			pats:      []string{ambiguousBroadPattern, ambiguousSpecificPattern, alsoMatching},
			text:      ambiguousStepText,
			wantClass: "ambiguous-step",
			wantSubs: []string{
				strconv.Quote(ambiguousBroadPattern),
				strconv.Quote(ambiguousSpecificPattern),
				strconv.Quote(alsoMatching),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pats := BuiltinStepPatterns()
			if tt.pats != nil {
				pats = mustCompileSet(t, tt.pats...)
			}
			got := StepBindingFindings(pats, []*messages.PickleStep{pstep(tt.text)}, lineSrc("f.feature", 12))
			if tt.wantClass == "" {
				if len(got) != 0 {
					t.Fatalf("want no finding, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want 1 finding, got %d: %+v", len(got), got)
			}
			if got[0].Class != tt.wantClass {
				t.Fatalf("class = %q, want %q (message: %s)", got[0].Class, tt.wantClass, got[0].Message)
			}
			if got[0].File != "f.feature" || got[0].Line != 12 {
				t.Fatalf("location = %s:%d, want f.feature:12", got[0].File, got[0].Line)
			}
			assertSubstringsInOrder(t, got[0].Message, tt.wantSubs)
		})
	}
}

// assertSubstringsInOrder fails unless every want appears in msg, at strictly
// increasing positions. Order is asserted by position rather than by comparing a
// rebuilt string, so the test does not reimplement the formatter it is checking.
func assertSubstringsInOrder(t *testing.T, msg string, want []string) {
	t.Helper()
	at := -1
	for _, w := range want {
		i := strings.Index(msg, w)
		if i < 0 {
			t.Fatalf("message %q must contain %q", msg, w)
		}
		if i <= at {
			t.Fatalf("message %q lists %q out of order (want the substrings %q at increasing positions)", msg, w, want)
		}
		at = i
	}
}

// TestStepBindingFindingsAgreesWithTheRunnerOnAmbiguity is the assertion T090 exists
// for: the static classification must fire on the SAME sentence the live runner
// refuses, or Validate goes on certifying a suite Run rejects.
//
// It asserts both halves in one test rather than trusting a shared constant to keep
// them aligned. The runtime half drives the real godog suite through ambiguityProbe
// (whose own contract TestAmbiguousStepIsRecordedAsFailed pins), and the static half
// binds the same sentence against the same engine's pattern set with the probe's two
// patterns appended — the same set in the same order the probe registers.
//
// # It was GREEN the first time it ran, and that is stated rather than dressed up
//
// The ambiguous-step branch already existed when this was written (the table above
// drove it red first), so there is no honest red→green pair to show here: this adds
// no branch, it cross-checks one. Its red is the mutation rehearsal recorded on
// TestStepBindingFindings — mutation 1 (`> 1` -> `> 2`) turns this test red with
// "want 1 ambiguous-step finding, got 0", which is the failure it is here to catch.
// Same posture as TestAmbiguousStepIsRecordedAsFailed, for the same reason.
func TestStepBindingFindingsAgreesWithTheRunnerOnAmbiguity(t *testing.T) {
	t.Parallel()

	// Static half: the engine's own pattern set, plus the two the probe registers on
	// top of it. Derived through EngineStepChecks rather than hand-listed, so it is
	// the set that engine actually binds.
	eng := customComparatorEngine(t)
	pats, _, _, err := EngineStepChecks(eng)
	if err != nil {
		t.Fatalf("EngineStepChecks: %v", err)
	}
	pats = append(pats, mustCompileSet(t, ambiguousBroadPattern, ambiguousSpecificPattern)...)

	findings := StepBindingFindings(pats, []*messages.PickleStep{pstep(ambiguousStepText)}, lineSrc("agreement.feature", 3))
	if len(findings) != 1 || findings[0].Class != "ambiguous-step" {
		t.Fatalf("want 1 ambiguous-step finding for %q, got %+v; a validator silent here certifies a suite the runner refuses", ambiguousStepText, findings)
	}

	// Runtime half: the same sentence, under Strict, through the real collector path.
	sr, out, _, _ := ambiguityProbe(t, true)
	if sr.Pass {
		t.Fatalf("runner recorded Pass=true for %q; this test's premise is that the runner refuses it\n%s", ambiguousStepText, out)
	}
	reasons := strings.Join(sr.Reasons, "\n")
	if !strings.Contains(reasons, "ambiguous step definition") {
		t.Fatalf("runner failed %q for some reason other than ambiguity: %q\n%s", ambiguousStepText, sr.Reasons, out)
	}

	// Both halves must name the same expressions, so an author reading either one is
	// sent to the same two patterns. %q escapes the backslash in (\w+), which is why
	// the static side is searched for strconv.Quote(p) and the runner's plain text
	// for p.
	for _, p := range []string{ambiguousBroadPattern, ambiguousSpecificPattern} {
		if !strings.Contains(reasons, p) {
			t.Errorf("runner reasons %q do not name %q", sr.Reasons, p)
		}
		if !strings.Contains(findings[0].Message, strconv.Quote(p)) {
			t.Errorf("finding %q does not name %q, which the runner's own message names", findings[0].Message, p)
		}
	}
}

func TestTargetFindings(t *testing.T) {
	t.Parallel()
	known := map[string]bool{"researchbot": true}
	tests := []struct {
		name    string
		text    string
		wantHit bool
	}{
		{name: "known agent target", text: `the agent target "researchbot"`},
		{name: "known service target", text: `the service target "researchbot"`},
		{name: "unknown target", text: `the agent target "ghost"`, wantHit: true},
		{name: "non-target step ignored", text: `the run satisfies "true"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := TargetFindings(known, []*messages.PickleStep{pstep(tt.text)}, lineSrc("f.feature", 3))
			if tt.wantHit {
				if len(got) != 1 || got[0].Class != "unknown-target" {
					t.Fatalf("want 1 unknown-target finding, got %+v", got)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("want no finding, got %+v", got)
			}
		})
	}
}

func TestCELFindings(t *testing.T) {
	t.Parallel()
	eng := stubPrecheckEngine{}
	tests := []struct {
		name    string
		step    *messages.PickleStep
		wantHit bool
	}{
		{name: "good inline run", step: pstep(`the run satisfies "tokens < 5000"`)},
		{name: "bad inline run", step: pstep(`the run satisfies "tokens <"`), wantHit: true},
		{name: "good inline runs", step: pstep(`the runs satisfy "rate(r, !r.failed) >= 0.5"`)},
		{name: "bad inline runs", step: pstep(`the runs satisfy "rate(r, "`), wantHit: true},
		{name: "unrelated step ignored", step: pstep(`the agent target "x"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CELFindings(eng, []*messages.PickleStep{tt.step}, lineSrc("f.feature", 6))
			if tt.wantHit {
				if len(got) != 1 || got[0].Class != "bad-cel" {
					t.Fatalf("want 1 bad-cel finding, got %+v", got)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("want no finding, got %+v", got)
			}
		})
	}
}

func TestCELFindingsMissingComparator(t *testing.T) {
	t.Parallel()
	// A PrecheckEngine with no cel comparator must yield a hard finding, never a
	// silent pass (Constitution IV).
	eng := emptyPrecheckEngine{}
	got := CELFindings(eng, []*messages.PickleStep{pstep(`the run satisfies "true"`)}, Source{})
	if len(got) != 1 || got[0].Class != "bad-cel" {
		t.Fatalf("want 1 bad-cel finding for missing comparator, got %+v", got)
	}
}

// emptyPrecheckEngine registers no comparators and no patterns.
type emptyPrecheckEngine struct{}

func (emptyPrecheckEngine) Comparator(string) (core.Comparator, bool) { return nil, false }
func (emptyPrecheckEngine) AggregateComparator(string) (core.AggregateComparator, bool) {
	return nil, false
}
func (emptyPrecheckEngine) ShapePattern(string) ([]comparator.ShapeExpectation, bool) {
	return nil, false
}

func TestShapePatternFindings(t *testing.T) {
	t.Parallel()
	eng := stubPrecheckEngine{pats: map[string][]comparator.ShapeExpectation{
		"known": {{Kind: "exists"}},
	}}
	tests := []struct {
		name    string
		text    string
		wantHit bool
	}{
		{name: "known pattern", text: `the run matches shape "known"`},
		{name: "unknown pattern", text: `the run matches shape "missing"`, wantHit: true},
		{name: "non-shape step ignored", text: `the run satisfies "true"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ShapePatternFindings(eng, []*messages.PickleStep{pstep(tt.text)}, lineSrc("f.feature", 7))
			if tt.wantHit {
				if len(got) != 1 || got[0].Class != "unknown-shape" {
					t.Fatalf("want 1 unknown-shape finding, got %+v", got)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("want no finding, got %+v", got)
			}
		})
	}
}

func TestRunsTagFindings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tags     []*messages.PickleTag
		wantHit  bool
		wantSubs []string
	}{
		{name: "absent tag", tags: nil},
		{name: "good tag", tags: []*messages.PickleTag{{Name: "@runs(3)"}}},
		{name: "good parallel tag", tags: []*messages.PickleTag{{Name: "@runs(2,parallel)"}}},
		{name: "malformed tag", tags: []*messages.PickleTag{{Name: "@runs(bad)", AstNodeId: "t1"}}, wantHit: true},
		{name: "zero n", tags: []*messages.PickleTag{{Name: "@runs(0)", AstNodeId: "t1"}}, wantHit: true},
		{
			name:     "two valid tags are ambiguous",
			tags:     []*messages.PickleTag{{Name: "@runs(2)", AstNodeId: "t1"}, {Name: "@runs(3)", AstNodeId: "t2"}},
			wantHit:  true,
			wantSubs: []string{"ambiguous", "@runs(2)", "@runs(3)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RunsTagFindings(tt.tags, Source{File: "f.feature", Line: func(string) int { return 2 }})
			if tt.wantHit {
				if len(got) != 1 || got[0].Class != "bad-runs-tag" {
					t.Fatalf("want 1 bad-runs-tag finding, got %+v", got)
				}
				if got[0].Line != 2 {
					t.Fatalf("line = %d, want 2", got[0].Line)
				}
				for _, sub := range tt.wantSubs {
					if !strings.Contains(got[0].Message, sub) {
						t.Fatalf("finding message %q must name %q", got[0].Message, sub)
					}
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("want no finding, got %+v", got)
			}
		})
	}
}

// TestCollidingPatternsYieldOneAmbiguousStepFinding is US1's T009 and pins SC-001.
//
// It is the other half of the deferral: stepProblem declines to diagnose a step several
// definitions match, and this is the check that reports it instead. If this did not
// hold, the deferral would be silence rather than a hand-off.
//
// Both collision SHAPES are covered — two built-in-shaped patterns and two
// contributed-shaped ones — because StepBindingFindings classifies by COUNT over the
// whole pattern set and has no notion of source. That is exactly why one finding is
// correct for both.
//
// # How this differs from TestValidateReportsAmbiguousStep, above
//
// That test is stronger on one axis and silent on another. It pairs the static finding
// with the RUNTIME half (the runner refusing the same sentence under Strict), which this
// test does not attempt — but it drives a single broad-plus-specific pair, so it says
// nothing about whether the classification is independent of where the patterns came
// from. SC-001 asks for both combinations by name, and that is the gap this fills.
// Neither subsumes the other; do not delete one on the grounds that the other is green.
func TestCollidingPatternsYieldOneAmbiguousStepFinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		text     string
		// wantAmbiguous is false for the negative control. A gate only ever exercised
		// on collisions is untested in the direction that matters most in practice:
		// every ordinary step must stay clean.
		wantAmbiguous bool
	}{
		{
			name:          "two built-in-shaped patterns",
			patterns:      []string{overlappingPair[0], overlappingPair[1]},
			text:          overlappingWitness,
			wantAmbiguous: true,
		},
		{
			name:          "two contributed-shaped phrases",
			patterns:      []string{`^the widget is "([^"]*)"$`, `^the widget is "gold"$`},
			text:          `the widget is "gold"`,
			wantAmbiguous: true,
		},
		{
			name:     "negative control: two patterns that do not collide",
			patterns: []string{disjointPair[0], disjointPair[1]},
			text:     `the tool "x" is never called`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pats, err := CompileStepPatterns(tt.patterns)
			if err != nil {
				t.Fatalf("compiling fixtures: %v", err)
			}
			got := StepBindingFindings(pats, []*messages.PickleStep{{Text: tt.text}}, Source{})

			if !tt.wantAmbiguous {
				if len(got) != 0 {
					t.Fatalf("a step matching exactly one pattern must yield no finding, got %+v", got)
				}
				return
			}

			if len(got) != 1 {
				t.Fatalf("want exactly 1 finding for a step two patterns match, got %d: %+v", len(got), got)
			}
			if got[0].Class != "ambiguous-step" {
				t.Errorf("class = %q, want %q", got[0].Class, "ambiguous-step")
			}
			// Every match must be named. A finding that listed only one would send the
			// author to a definition that does not bind — the same defect the deferral
			// in stepProblem exists to avoid.
			for _, p := range tt.patterns {
				// strconv.Quote, not the raw pattern: the message renders each match with
				// %q, so a pattern containing quotes appears escaped. Asserting the raw
				// form fails against a CORRECT message — the trap stepargs_test.go
				// already documents for step text.
				if !strings.Contains(got[0].Message, strconv.Quote(p)) {
					t.Errorf("message does not name matching pattern %q:\n  %s", p, got[0].Message)
				}
			}
		})
	}
}
