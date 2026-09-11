package steps

import (
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

func TestStepBindingFindings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		text      string
		wantClass string // "" => no finding
	}{
		{name: "bound target step", text: `the agent target "researchbot"`},
		{name: "bound cel step", text: `the run satisfies "tokens < 5000"`},
		{name: "bound shape step", text: `the run matches shape "flow"`},
		{name: "unbound gibberish", text: `the moon is made of cheese`, wantClass: "unbound-step"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := StepBindingFindings(BuiltinStepPatterns(), []*messages.PickleStep{pstep(tt.text)}, lineSrc("f.feature", 12))
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
				t.Fatalf("class = %q, want %q", got[0].Class, tt.wantClass)
			}
			if got[0].File != "f.feature" || got[0].Line != 12 {
				t.Fatalf("location = %s:%d, want f.feature:12", got[0].File, got[0].Line)
			}
		})
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
