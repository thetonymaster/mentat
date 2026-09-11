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
func runIsolated(t *testing.T, cmp *isolationComparator, sentence string) (mentat.Results, string, error) {
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
