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
	"strconv"
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
	return runIsolatedFeature(t, cmp, fmt.Sprintf(`Feature: isolation
  Scenario: a phrase bound only by its own engine
    Given the agent target "bot"
    When I run scenario "any"
    Then %s
`, sentence))
}

// runIsolatedFeature is runIsolated for tests that need to control the whole feature
// body (docstrings, multiple steps).
func runIsolatedFeature(t *testing.T, cmp mentat.Comparator, body string) (mentat.Results, string, error) {
	t.Helper()
	b := newBus()
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "iso.feature")
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

// --- the docstring-agreement guard (found by review, 2026-09-11) ---

// docPhrase contributes one phrase whose docstring-ness is controlled per test, and
// records whether Compare ever ran.
type docPhrase struct {
	pattern  string
	compared int
}

func (c *docPhrase) Name() string { return "doc-phrase" }
func (c *docPhrase) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{{
		Pattern: c.pattern, Group: "Doc", Summary: "s", Example: "e",
	}}
}
func (c *docPhrase) ParseCaptures(caps []string) (mentat.Expectation, error) {
	return strings.Join(caps, ","), nil
}
func (c *docPhrase) ParseExpectation(text string) (mentat.Expectation, error) { return text, nil }
func (c *docPhrase) Compare(_ context.Context, _ mentat.Evidence, _ mentat.Expectation) (mentat.Verdict, error) {
	c.compared++
	return mentat.Verdict{Pass: true}, nil
}

// TestStepArgumentMismatchIsRejectedBeforeTheStepRuns closes a hole review found after
// the feature was otherwise complete — twice, which is the more useful part of the story.
//
// Measured on godog v0.15.1: a step carrying a docstring, matched by a phrase that
// declares none, has its body SILENTLY DISCARDED — the argument conversion loop runs
// `i < numIn`, so the surplus argument vanishes, the handler runs on the captures
// alone, and the scenario reports PASSED. An author who wrote an expectation body got a
// green verdict from a comparator that never read it.
//
// That is the unearned green this framework exists to prevent, and it was reachable
// through the public surface the moment contributed phrases shipped, because
// docstring-ness is INFERRED from a trailing `:$` in the pattern — a convention an
// author can simply forget.
//
// The FIRST fix checked docstrings only, and review immediately found the identical
// hole one field over: a surplus DATA TABLE was still discarded and the scenario still
// passed. That is why the check is now written against the mechanism — any argument the
// phrase cannot receive — with an unrecognised argument kind rejected by default rather
// than treated as "none".
//
// The opposite direction is also asserted. godog does fail there, so nothing was
// silent, but its message ("func expected more arguments than given") names neither the
// comparator nor the pattern, which is useless to someone with several phrases.
func TestStepArgumentMismatchIsRejectedBeforeTheStepRuns(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		sentence string
		body     string
		table    string
		wantSubs []string
	}{
		{
			name:     "step carries a body the phrase does not declare",
			pattern:  `^the revenue matches (\w+)$`,
			sentence: "the revenue matches quarterly",
			body:     "SURPRISE BODY",
			// Without the guard this scenario PASSES with the body discarded.
			wantSubs: []string{"doc-phrase", strconv.Quote(`^the revenue matches (\w+)$`), "silently discarded"},
		},
		{
			name:     "step omits a body the phrase declares",
			pattern:  `^the revenue matches (\w+):$`,
			sentence: "the revenue matches quarterly:",
			body:     "",
			wantSubs: []string{"doc-phrase", strconv.Quote(`^the revenue matches (\w+):$`), "carries none"},
		},
		{
			// Review found this one AFTER the docstring direction was fixed: the same
			// mechanism, one field over. No contributed-phrase seam can receive a
			// table — CaptureParser takes []string, ExpectationParser takes string —
			// so a table here is always an expectation that cannot be read. Measured
			// before the fix: passed=1, Compare ran, table gone.
			name:     "step carries a data table no seam can receive",
			pattern:  `^the revenue matches (\w+)$`,
			sentence: "the revenue matches quarterly",
			table:    "      | min | 4   |\n      | cur | USD |\n",
			wantSubs: []string{"doc-phrase", "data table", "silently discarded"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp := &docPhrase{pattern: tt.pattern}

			step := "    Then " + tt.sentence + "\n"
			if tt.body != "" {
				step += "      \"\"\"\n      " + tt.body + "\n      \"\"\"\n"
			}
			step += tt.table
			body := "Feature: step-argument agreement\n  Scenario: mismatched\n" +
				"    Given the agent target \"bot\"\n    When I run scenario \"any\"\n" + step

			res, out, err := runIsolatedFeature(t, cmp, body)
			if err != nil {
				t.Fatalf("Run returned a harness error: %v\n%s", err, out)
			}

			if res.Passed != 0 {
				t.Errorf("scenario PASSED on a docstring mismatch; a body the comparator never read must never yield a green verdict\n%s", out)
			}
			if res.Failed != 1 {
				t.Fatalf("failed=%d, want 1\n%s", res.Failed, out)
			}
			if cmp.compared != 0 {
				t.Errorf("Compare ran %d time(s); the mismatch must be caught at scenario init, before any SUT is driven", cmp.compared)
			}
			reasons := strings.Join(res.Scenarios[0].Reasons, " ")
			for _, want := range tt.wantSubs {
				if !strings.Contains(reasons, want) {
					t.Errorf("reason does not mention %q — godog's own message names neither the comparator nor the pattern, which is why this check exists\ngot: %s", want, reasons)
				}
			}
		})
	}
}

// TestMatchingDocstringUsageStillRuns is the guard against over-correction: the two
// legitimate shapes must keep working, or the check above would "pass" by rejecting
// everything.
func TestMatchingDocstringUsageStillRuns(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		sentence string
		body     string
	}{
		{name: "captures, no body", pattern: `^the revenue matches (\w+)$`, sentence: "the revenue matches quarterly"},
		{name: "captures and a body", pattern: `^the revenue matches (\w+):$`, sentence: "the revenue matches quarterly:", body: `{"min":4}`},
		{name: "body only, no captures", pattern: `^the revenue matches:$`, sentence: "the revenue matches:", body: `{"min":4}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp := &docPhrase{pattern: tt.pattern}
			step := "    Then " + tt.sentence + "\n"
			if tt.body != "" {
				step += "      \"\"\"\n      " + tt.body + "\n      \"\"\"\n"
			}
			body := "Feature: docstring agreement\n  Scenario: matched\n" +
				"    Given the agent target \"bot\"\n    When I run scenario \"any\"\n" + step

			res, out, err := runIsolatedFeature(t, cmp, body)
			if err != nil {
				t.Fatalf("Run returned a harness error: %v\n%s", err, out)
			}
			if res.Passed != 1 || res.Failed != 0 {
				t.Fatalf("a legitimate docstring usage was rejected: passed=%d failed=%d\n%s", res.Passed, res.Failed, out)
			}
			if cmp.compared != 1 {
				t.Errorf("Compare ran %d time(s), want 1", cmp.compared)
			}
		})
	}
}

// TestStepArgumentGuardSkipsStepsMatchingABuiltin pins a misdiagnosis review caught.
//
// A contributed pattern can overlap a BUILT-IN step that legitimately takes an
// argument. Built-ins register first, so either the built-in binds the step — and its
// own rules apply — or the two genuinely collide, which the strict matcher reports by
// naming every matching expression. Complaining "the body would be silently discarded"
// about a step whose built-in handler consumes the body sends the author to the wrong
// place entirely.
func TestStepArgumentGuardSkipsStepsMatchingABuiltin(t *testing.T) {
	// # This test was mutation-dead until 2026-09-11, and the fix is the overlap it picks
	//
	// It used to pair contributed `^the run (\w+):$` with built-in `^the run satisfies:$`
	// and a step carrying a docstring. Both sources want a docstring, so argumentProblem
	// saw got == want and returned "" whether the skip existed or not. Measured: deleting
	// `case b != nil && cp != nil: return ""` left the ENTIRE suite green, including this
	// test — written specifically to pin that branch.
	//
	// The overlap below makes the skip observable, because the step carries an argument
	// the BUILT-IN cannot receive: contributed `^the agent target "(\w+)"$` (no `:$`, so
	// it declares no docstring) overlaps built-in `^the (?:agent|service) target
	// "([^"]+)"$` (which takes no argument), and the step carries a docstring. Without the
	// skip the want=="" branch fires and claims the body is "silently discarded"; with it,
	// the ambiguity is left to the strict matcher, which is the true diagnosis.
	cmp := &docPhrase{pattern: `^the agent target "(\w+)"$`}
	res, out, err := runIsolatedFeature(t, cmp, `Feature: overlap
  Scenario: a built-in step carrying an argument it cannot receive
    Given the agent target "bot"
      """
      body
      """
    When I run scenario "any"
`)
	if err != nil {
		t.Fatalf("harness error: %v\n%s", err, out)
	}
	reasons := strings.Join(res.Scenarios[0].Reasons, " ")
	if strings.Contains(reasons, "silently discarded") {
		t.Errorf("the step-argument guard diagnosed an argument problem, but this step matches two patterns and binds NEITHER under Strict — so the body is not discarded, the step never runs. The real defect is the overlap, which the strict matcher reports by naming every matching expression\ngot: %s", reasons)
	}
	// It still fails — the patterns genuinely collide — but for the right reason.
	if !strings.Contains(reasons, "ambiguous") {
		t.Errorf("want the ambiguity reported, got: %s", reasons)
	}
}

// TestValidateAgreesWithRunOnStepArguments pins that the static and runtime paths
// answer the same question about the same suite.
//
// Review measured them disagreeing: Validate reported `findings=[] err=<nil>` for a
// feature file Run rejects at scenario init. A validator that certifies a suite the
// runner then refuses is worse than no validator — it spends the author's trust to
// tell them something false.
func TestValidateAgreesWithRunOnStepArguments(t *testing.T) {
	body := `Feature: agreement
  Scenario: a surplus data table
    Given the agent target "bot"
    When I run scenario "any"
    Then the revenue matches quarterly
      | min | 4 |
`
	path := filepath.Join(t.TempDir(), "agree.feature")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	cfg := mentat.Config{
		Store:   "agree-reg",
		Targets: map[string]mentat.Target{"bot": {Adapter: "agree-reg", Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	opts := []mentat.Option{
		mentat.WithFeatures(path),
		mentat.WithDriver("agree-reg", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore("agree-reg", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		mentat.WithComparator("doc-phrase", func(mentat.Config) (mentat.Comparator, error) {
			return &docPhrase{pattern: `^the revenue matches (\w+)$`}, nil
		}),
	}

	findings, err := mentat.Validate(context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Class == "step-argument" {
			found = true
			if f.Line == 0 {
				t.Errorf("finding has no source line: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("Validate reported no step-argument finding for a suite Run rejects; the two paths must agree about the same file\ngot: %+v", findings)
	}

	// And the run really does reject it, or the agreement is vacuous.
	res, err := mentat.Run(context.Background(), cfg, append(opts, mentat.WithConcurrency(1))...)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Passed != 0 {
		t.Errorf("Run passed a suite Validate flagged; passed=%d", res.Passed)
	}
}
