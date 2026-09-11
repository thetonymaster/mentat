// Package mentat_test — US2 for 012: contributed phrases are scoped to the engine
// that resolved them.
//
// This is the property that decides whether the feature is SOUND, which is why it is
// P1 alongside US1 rather than a nice-to-have. Shipping the phrase route without it
// would mean shipping a latent cross-run contamination bug: a suite would bind a
// sentence belonging to a comparator it never registered, and either assert something
// nobody wrote or fail for a reason nobody can locate.
//
// 007 established exactly this property for seam registries, and
// mentat_run_reentrancy_test.go exists because it had already bitten once. The deleted
// sync.Once in precheck.go was the same defect class waiting to happen again.
//
// The harness (bus/busDriver/busStore, writeFile) lives in mentat_run_test.go.
package mentat_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat"
)

// isolationComparator contributes exactly one phrase, parameterised by a token that
// appears in both its pattern and its verdict. Two instances with different tokens
// therefore have disjoint vocabularies, and any leak is visible as a bound step rather
// than inferred from a count.
type isolationComparator struct {
	token string
	// seen records every capture list this comparator was asked to parse, so a leak
	// that somehow still produced a green scenario is still caught.
	seen []string
}

func (c *isolationComparator) Name() string { return "iso-" + c.token }

func (c *isolationComparator) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{{
		Pattern: fmt.Sprintf(`^the %s reading is (\w+)$`, c.token),
		Group:   "Isolation",
		Summary: "Asserts a reading specific to the " + c.token + " engine.",
		Example: fmt.Sprintf("Then the %s reading is fine", c.token),
	}}
}

func (c *isolationComparator) ParseCaptures(caps []string) (mentat.Expectation, error) {
	c.seen = append(c.seen, strings.Join(caps, ","))
	return strings.Join(caps, ","), nil
}

func (c *isolationComparator) Compare(_ context.Context, _ mentat.Evidence, _ mentat.Expectation) (mentat.Verdict, error) {
	return mentat.Verdict{Pass: true}, nil
}

// runIsolated builds ONE engine (one mentat.Run) registering only cmp, over a feature
// written in the given sentence.
func runIsolated(t *testing.T, cmp mentat.Comparator, sentence string) (mentat.Results, string, error) {
	t.Helper()
	b := newBus()
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "iso.feature")
	body := fmt.Sprintf(`Feature: isolation
  Scenario: a phrase bound only by its own engine
    Given the agent target "bot"
    When I run scenario "any"
    Then %s
`, sentence)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	cfg := mentat.Config{
		Store: "iso-reg",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "iso-reg", Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithConcurrency(1),
		mentat.WithOutput(&buf),
		mentat.WithDriver("iso-reg", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore("iso-reg", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator(cmp.Name(), func(mentat.Config) (mentat.Comparator, error) {
			return cmp, nil
		}),
	)
	return res, buf.String(), err
}

// TestContributedPhrasesAreScopedToTheirEngine runs two engines with disjoint phrase
// vocabularies in ONE process, in BOTH orders, and asserts each binds only its own.
//
// Both orders are required and not belt-and-braces. A first-writer-wins cache — which
// is exactly what the deleted sync.Once was — passes one ordering and fails the other,
// so a single-order test would have reported this bug fixed roughly half the time.
//
// Mutation rehearsal (2026-09-11): reintroduced a package-level first-writer-wins
// phrase cache in resolvePhrases (the shape of the deleted sync.Once), under an
// assertion that the source edit applied. RED in both orderings — "[beta] its OWN
// phrase did not bind: passed=0 failed=1" — and RED in
// TestAForeignPhraseIsUnboundUnderAnotherEngine too. Reverted; re-observed green,
// including under -race.
func TestContributedPhrasesAreScopedToTheirEngine(t *testing.T) {
	orders := []struct {
		name   string
		first  string
		second string
	}{
		{name: "alpha then beta", first: "alpha", second: "beta"},
		{name: "beta then alpha", first: "beta", second: "alpha"},
	}

	for _, o := range orders {
		t.Run(o.name, func(t *testing.T) {
			for _, token := range []string{o.first, o.second} {
				cmp := &isolationComparator{token: token}
				sentence := fmt.Sprintf("the %s reading is fine", token)

				res, out, err := runIsolated(t, cmp, sentence)
				if err != nil {
					t.Fatalf("[%s] Run returned a harness error: %v\n%s", token, err, out)
				}
				if res.Passed != 1 || res.Failed != 0 {
					t.Fatalf("[%s] its OWN phrase did not bind: passed=%d failed=%d\n%s", token, res.Passed, res.Failed, out)
				}
				if len(cmp.seen) != 1 || cmp.seen[0] != "fine" {
					t.Errorf("[%s] comparator parsed %q, want exactly [\"fine\"]", token, cmp.seen)
				}
			}
		})
	}
}

// TestAForeignPhraseIsUnboundUnderAnotherEngine is the other half, and the one that
// actually detects a leak.
//
// The test above would still pass if every engine saw every phrase — each engine's own
// sentence would bind either way. This one drives engine B with engine A's sentence and
// requires it to be UNBOUND: reported with the same wording any unknown step gets, and
// failing the scenario rather than passing it.
func TestAForeignPhraseIsUnboundUnderAnotherEngine(t *testing.T) {
	// The engine registers only the beta comparator; the feature speaks alpha.
	beta := &isolationComparator{token: "beta"}
	res, out, err := runIsolated(t, beta, "the alpha reading is fine")
	if err != nil {
		t.Fatalf("Run returned a harness error: %v\n%s", err, out)
	}

	if res.Passed != 0 {
		t.Errorf("a foreign phrase was BOUND by an engine that never registered its comparator (passed=%d); contributed phrases leaked across engines\n%s",
			res.Passed, out)
	}
	if res.Failed != 1 {
		t.Fatalf("failed=%d, want 1: an unbound step must fail the scenario, not be skipped\n%s", res.Failed, out)
	}
	if len(beta.seen) != 0 {
		t.Errorf("the beta comparator was asked to parse %q — it must never see a sentence it did not contribute", beta.seen)
	}
	// Same wording as any other unknown step: a leaked-vs-mistyped distinction would
	// be a false one, since neither binds anything that will run.
	if reasons := strings.Join(res.Scenarios[0].Reasons, " "); !strings.Contains(reasons, "undefined") {
		t.Errorf("scenario reason %q does not report the step as undefined; a foreign phrase must read like any unknown step", reasons)
	}
}

// TestContributedPhrasesAreScopedUnderConcurrency is the same property with the two
// engines running AT THE SAME TIME rather than one after the other.
//
// Sequential isolation can hold while concurrent isolation does not: shared state that
// is overwritten per run passes a sequential test (each run rewrites it before use) and
// fails under concurrency. Run with -race, this also catches a data race on any
// remaining shared phrase state.
func TestContributedPhrasesAreScopedUnderConcurrency(t *testing.T) {
	tokens := []string{"alpha", "beta", "gamma", "delta"}

	var wg sync.WaitGroup
	errs := make([]error, len(tokens))
	outs := make([]string, len(tokens))
	results := make([]mentat.Results, len(tokens))

	for i, token := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmp := &isolationComparator{token: token}
			res, out, err := runIsolated(t, cmp, fmt.Sprintf("the %s reading is fine", token))
			results[i], outs[i], errs[i] = res, out, err
		}()
	}
	wg.Wait()

	for i, token := range tokens {
		if errs[i] != nil {
			t.Fatalf("[%s] Run returned a harness error: %v\n%s", token, errs[i], outs[i])
		}
		if results[i].Passed != 1 || results[i].Failed != 0 {
			t.Errorf("[%s] concurrent run did not bind its own phrase: passed=%d failed=%d\n%s",
				token, results[i].Passed, results[i].Failed, outs[i])
		}
	}
}

// --- T036/T038: composition-time rejection and match-time collision ---

// brokenPhrase contributes a single malformed phrase and records whether it was ever
// asked to parse anything.
type brokenPhrase struct {
	pattern string
	parsed  bool
}

func (c *brokenPhrase) Name() string { return "broken" }
func (c *brokenPhrase) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{{
		Pattern: c.pattern, Group: "Broken", Summary: "s", Example: "e",
	}}
}
func (c *brokenPhrase) ParseCaptures(caps []string) (mentat.Expectation, error) {
	c.parsed = true
	return "x", nil
}
func (c *brokenPhrase) Compare(_ context.Context, _ mentat.Evidence, _ mentat.Expectation) (mentat.Verdict, error) {
	return mentat.Verdict{Pass: true}, nil
}

// TestMalformedPhraseFailsBeforeAnyScenarioRuns is T036/SC-003: a phrase defect stops
// the run at COMPOSITION, not partway through a suite.
//
// The distinction is not pedantic. A defect surfacing mid-suite means some scenarios
// already drove a real SUT — spending time, tokens and money — before the run was
// abandoned for a reason that was knowable before any of it started.
func TestMalformedPhraseFailsBeforeAnyScenarioRuns(t *testing.T) {
	cmp := &brokenPhrase{pattern: `the reading is fine`} // unanchored: violates V2

	res, out, err := runIsolated(t, cmp, "the reading is fine")

	if err == nil {
		t.Fatalf("Run accepted an unanchored contributed pattern; it must fail the build\n%s", out)
	}
	if !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "anchored") {
		t.Errorf("error %q must name the contributing comparator and the rule it broke", err)
	}
	// The decisive assertions: nothing ran.
	if res.Total != 0 || res.Passed != 0 || res.Failed != 0 {
		t.Errorf("scenarios executed despite a composition failure: total=%d passed=%d failed=%d", res.Total, res.Passed, res.Failed)
	}
	if cmp.parsed {
		t.Error("the comparator was asked to parse a sentence even though the engine build failed")
	}
}

// overlapComparator contributes TWO well-formed, anchored patterns that both match one
// sentence. Neither V3 nor V4 can catch this: the patterns are not identical, and
// deciding regex overlap in general is not something Mentat should attempt.
type overlapComparator struct{ ran []string }

func (c *overlapComparator) Name() string { return "overlap" }
func (c *overlapComparator) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{
		{Pattern: `^the (\w+) reading is fine$`, Group: "Overlap", Summary: "broad", Example: "Then the alpha reading is fine"},
		{Pattern: `^the alpha reading is fine$`, Group: "Overlap", Summary: "specific", Example: "Then the alpha reading is fine"},
	}
}
func (c *overlapComparator) ParseCaptures(caps []string) (mentat.Expectation, error) {
	c.ran = append(c.ran, strings.Join(caps, ","))
	return "x", nil
}
func (c *overlapComparator) Compare(_ context.Context, _ mentat.Evidence, _ mentat.Expectation) (mentat.Verdict, error) {
	return mentat.Verdict{Pass: true}, nil
}

// TestGenuinelyOverlappingPhrasesFailLoudly is T038/SC-011 — the END-TO-END counterpart
// of internal/steps' characterization test, and the first point in this repo's history
// where the ambiguous branch is reachable through the public surface.
//
// Two anchored, well-formed, non-identical patterns both match one sentence. Anchoring
// does not prevent this and was never claimed to; Strict is the backstop, and the
// build-time rules are the fast feedback for the cases that ARE cheaply decidable.
//
// Before Strict this scenario reported PASSED with the broad pattern silently
// swallowing the specific one — a green verdict nobody wrote. That is precisely what
// this test would catch if the flag were ever removed, which makes it the behavioural
// guard for run.go that the internal characterization test structurally could not be.
func TestGenuinelyOverlappingPhrasesFailLoudly(t *testing.T) {
	cmp := &overlapComparator{}
	res, out, err := runIsolated(t, cmp, "the alpha reading is fine")
	if err != nil {
		t.Fatalf("Run returned a harness error: %v\n%s", err, out)
	}

	if res.Passed != 0 {
		t.Fatalf("an ambiguous step PASSED; the first-registered pattern silently swallowed the other and the scenario reported a verdict nobody wrote\n%s", out)
	}
	if res.Failed != 1 {
		t.Fatalf("failed=%d, want 1\n%s", res.Failed, out)
	}

	reasons := strings.Join(res.Scenarios[0].Reasons, "\n")
	if !strings.Contains(reasons, "ambiguous") {
		t.Errorf("reason %q does not report the ambiguity", reasons)
	}
	// Naming EVERY matching expression is what makes the failure actionable: the
	// author has to know which two patterns to reconcile.
	for _, want := range []string{`^the (\w+) reading is fine$`, `^the alpha reading is fine$`} {
		if !strings.Contains(reasons, want) {
			t.Errorf("reason does not name the matching expression %q; an ambiguity error that does not list the candidates leaves the author guessing\ngot: %s", want, reasons)
		}
	}
	if len(cmp.ran) != 0 {
		t.Errorf("a handler ran despite the ambiguity (%q); neither side of an ambiguous match may execute", cmp.ran)
	}
}
