package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
)

// revenueShapeExpectation is the test comparator's OWN expectation type. Nothing in
// stepDefs knows how to construct it, and that is the point: before 011 a comparator
// keyed on a type like this was reachable only from Go.
type revenueShapeExpectation struct {
	Min      int    `json:"min"`
	Currency string `json:"currency"`
}

// revenueShape is a custom comparator that opts into Gherkin invocation by
// implementing core.ExpectationParser alongside core.Comparator.
//
// It records the text it parsed and the expectation it was later handed, so a test
// can PROVE the round trip rather than infer it from a green suite: a step that
// silently passed a nil or zero-value expectation would also produce a green suite.
type revenueShape struct {
	parsedText []string
	comparedAs []core.Expectation
	// parseErr, when set, makes ParseExpectation fail instead of parsing.
	parseErr error
	// parseNil, when set, makes ParseExpectation claim success while returning a nil
	// expectation — a buggy comparator, and the case CodeRabbit flagged on PR #39.
	parseNil bool
	// fail, when set, makes Compare return a failing verdict.
	fail bool
}

// nilTolerantComparator returns a PASSING verdict for a nil expectation, and pairs
// with a parser that returns (nil, nil). Both halves are the same author's bug, but
// between them they would produce a green step that asserted nothing, so the step
// itself has to refuse the nil.
type nilTolerantComparator struct{ compared int }

func (c *nilTolerantComparator) Name() string { return "nil-tolerant" }

func (c *nilTolerantComparator) ParseExpectation(string) (core.Expectation, error) {
	return nil, nil
}

func (c *nilTolerantComparator) Compare(context.Context, core.Evidence, core.Expectation) (core.Verdict, error) {
	c.compared++
	return core.Verdict{Pass: true}, nil
}

func (c *revenueShape) Name() string { return "revenue-shape" }

func (c *revenueShape) ParseExpectation(text string) (core.Expectation, error) {
	c.parsedText = append(c.parsedText, text)
	if c.parseErr != nil {
		return nil, c.parseErr
	}
	if c.parseNil {
		return nil, nil
	}
	var exp revenueShapeExpectation
	if err := json.Unmarshal([]byte(text), &exp); err != nil {
		return nil, fmt.Errorf("parsing expectation %q: %w", text, err)
	}
	return exp, nil
}

func (c *revenueShape) Compare(_ context.Context, _ core.Evidence, e core.Expectation) (core.Verdict, error) {
	c.comparedAs = append(c.comparedAs, e)
	exp, ok := e.(revenueShapeExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("revenue-shape: expected revenueShapeExpectation, got %T", e)
	}
	if c.fail {
		return core.Verdict{Pass: false, Reasons: []string{
			fmt.Sprintf("revenue below the %d %s floor", exp.Min, exp.Currency),
		}}, nil
	}
	return core.Verdict{Pass: true}, nil
}

// customComparatorEngine builds the hermetic engine every 011 test uses: gomock
// store, fixed correlation, happyTrace. Mirrors TestFeatureExercisesGrammarAgainstFakeEngine
// (steps_test.go:58) so the new phrase is exercised on exactly the footing the
// built-in grammar is.
func customComparatorEngine(t *testing.T, opts ...engine.Option) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, happyTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor, opts...)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// withComparator registers c under name through the existing extras option — the
// feature adds no new registration path.
func withComparator(name string, c core.Comparator) engine.Option {
	return engine.WithExtraComparator(name, func(config.Config) (core.Comparator, error) { return c, nil })
}

// runInlineFeature runs an inline feature in-process against eng, returning godog's
// exit status and the captured output. Hermetic: no file, no network.
//
// Strict is set deliberately. Godog's default is non-strict, where an UNDEFINED step
// is reported but still exits 0 — so a suite that lost its stepDefs row would keep
// reporting success. Every assertion below about a step "running" would then be
// vacuous. Strict makes the row's existence a real failure, not a hopeful one.
func runInlineFeature(eng *engine.Engine, name, contents string) (int, string) {
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: mustInit(Initializer(eng)),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: name, Contents: []byte(contents)}},
		},
	}
	return suite.Run(), out.String()
}

// TestCustomComparatorRejectedInMultirunScenario proves the new step INHERITS the
// single-run guard (steps.go:256) rather than bypassing it, mirroring
// TestSingleRunStepRejectedInMultirunScenario for the built-in grammar.
//
// This is the concrete stake in routing through checkExp instead of calling
// Engine.Compare directly (FR-006): a handler that called Compare would evaluate only
// the first run of a @runs(2) scenario and report a confident green. The comparator
// here returns Pass, so a GREEN suite means the guard was lost.
//
// Falsified by replacing the checkExp call with a direct Engine.Compare, which turns
// this test red while the rest of the feature stays green.
func TestCustomComparatorRejectedInMultirunScenario(t *testing.T) {
	eng := customComparatorEngine(t, withComparator("revenue-shape", &revenueShape{}))

	feature := `Feature: mixed-grammar
  @runs(2)
  Scenario: the Extend step under @runs is rejected
    Given the agent target "bot"
    When I run scenario "x"
    Then the "revenue-shape" comparator is satisfied by:
      """
      {"min": 4}
      """
`
	status, out := runInlineFeature(eng, "mixed-grammar", feature)
	if status == 0 {
		t.Fatalf("expected RED: the Extend step inside @runs(2) must be rejected, but the suite passed\n%s", out)
	}
	if !strings.Contains(out, "@runs(2)") {
		t.Fatalf("expected the error to name @runs(2), got:\n%s", out)
	}
	if !strings.Contains(out, "the runs satisfy") {
		t.Fatalf("expected the error to point at \"the runs satisfy\", got:\n%s", out)
	}
}

// plainComparator is registered but does NOT implement core.ExpectationParser — the
// majority case, and the one that must fail loudly rather than be silently skipped.
type plainComparator struct{}

func (plainComparator) Name() string { return "plain" }
func (plainComparator) Compare(context.Context, core.Evidence, core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

// TestCustomComparatorErrors is US2: every failure mode names the offending value.
// One row per D5 mode, plus the verbatim-echo case. All four were written before the
// branches existed.
//
// Asserted at the handler rather than through a suite because the point here is the
// exact error TEXT; that the suite goes red on these is proven separately by
// TestCustomComparatorGoesRed and TestCustomComparatorParseError.
func TestCustomComparatorErrors(t *testing.T) {
	tests := []struct {
		name         string
		comparator   string
		wantContains []string
	}{
		{
			// FR-007. Listing the alternatives mirrors 010's WithReports unknown-name
			// behaviour and is the difference between a usable seam and a guessing game.
			name:         "unregistered name lists the registered ones",
			comparator:   "typo-name",
			wantContains: []string{"typo-name", "plain", "revenue-shape"},
		},
		{
			// FR-008. Not an assertion failure and not a skip — a loud error.
			name:         "registered but not an ExpectationParser",
			comparator:   "plain",
			wantContains: []string{"plain", "cannot be driven from Gherkin"},
		},
		{
			// FR-009. A parse failure is not an assertion failure, so it is wrapped and
			// surfaced rather than converted into a failing verdict.
			name:         "parser error is wrapped and named by comparator",
			comparator:   "boom",
			wantContains: []string{"boom", "detonated"},
		},
		{
			// The captured name is echoed with %q, so an otherwise invisible difference
			// — here a trailing space — is visible in the error instead of presenting as
			// a mysterious unknown name.
			name:         "captured name is echoed verbatim so invisible characters show",
			comparator:   "revenue-shape ",
			wantContains: []string{`"revenue-shape "`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := customComparatorEngine(t,
				withComparator("revenue-shape", &revenueShape{}),
				withComparator("plain", plainComparator{}),
				withComparator("boom", &revenueShape{parseErr: errors.New("detonated")}),
			)
			w := &world{eng: eng, ctx: context.Background(), target: "bot"}

			err := w.comparatorSatisfiedByDoc(tt.comparator, &godog.DocString{Content: `{"min": 4}`})
			if err == nil {
				t.Fatalf("comparator %q: expected an error, got nil", tt.comparator)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must contain %q", err, want)
				}
			}
		})
	}
}

// TestCustomComparatorPatternRejectsEmbeddedQuote records a correction to this
// feature's own spec.
//
// The spec's edge case and contracts/step-grammar.md both claimed a comparator name
// containing a quote is TRUNCATED at that quote, and that echoing the capture verbatim
// is what makes the truncation visible. That cannot happen: the pattern is anchored at
// both ends and ([^"]+) cannot cross a quote, so such a line matches NOTHING and the
// step is undefined instead of mis-captured.
//
// A follow-up claim once recorded here — that the consequence is worse than truncation,
// because godog's non-strict default lets an undefined step pass silently in a real run
// — was investigated and is largely FALSE. mentat.Run discards the suite status and
// derives Results from the collector, whose After hook receives "step is undefined: …"
// as stepErr and records the scenario as failed (TestUndefinedStepFailsTheRun, root
// package). The gap was real only in ctl.ReplayFeature, which read the suite status
// directly; it is fixed with Strict there.
//
// So an embedded quote yields an undefined step, and an undefined step fails the run.
// The pattern-level assertion below is what pins the first half.
func TestCustomComparatorPatternRejectsEmbeddedQuote(t *testing.T) {
	var pattern string
	for _, sd := range stepDefs {
		if sd.group == "Extend" {
			pattern = sd.pattern
		}
	}
	if pattern == "" {
		t.Fatal("no Extend row found in stepDefs")
	}
	re := regexp.MustCompile(pattern)

	if m := re.FindStringSubmatch(`the "revenue-shape" comparator is satisfied by:`); m == nil {
		t.Fatal("the well-formed phrase must match")
	} else if m[1] != "revenue-shape" {
		t.Fatalf("captured %q, want %q", m[1], "revenue-shape")
	}

	// No match at all — NOT a truncated capture of "rev".
	if m := re.FindStringSubmatch(`the "rev"enue" comparator is satisfied by:`); m != nil {
		t.Fatalf("embedded quote matched with capture %q; the spec's truncation claim would then hold", m[1])
	}
	// An empty name is rejected by ([^"]+) rather than resolving to "".
	if re.MatchString(`the "" comparator is satisfied by:`) {
		t.Fatal("an empty comparator name must not match")
	}
}

// customComparatorFeature is the inline feature both red proofs drive: identical
// except for how the registered comparator is rigged to misbehave.
const customComparatorFeature = `Feature: red proof
  Scenario: a custom comparator is not satisfied
    Given the agent target "bot"
    When I run scenario "happy"
    Then the "revenue-shape" comparator is satisfied by:
      """
      {"min": 4, "currency": "USD"}
      """
`

// TestCustomComparatorGoesRed is the L3 obligation for US4 (Constitution V): a test
// framework that cannot be SHOWN to fail correctly is unfalsifiable.
//
// A registered custom comparator returning a failing verdict must fail the suite AND
// surface the comparator's own reasons. Status alone is insufficient — a suite that
// exits non-zero while swallowing the reason tells the author nothing.
//
// This lives in internal/steps rather than e2e/ deliberately (research R6): the e2e
// suite drives a prebuilt cmd/mentat binary, which structurally cannot contain a
// Go-registered comparator, and sits behind //go:build e2e, which `make ci` never
// compiles.
//
// Falsified by T027 — see the rehearsal transcript at the foot of this file.
func TestCustomComparatorGoesRed(t *testing.T) {
	cmp := &revenueShape{fail: true}
	eng := customComparatorEngine(t, withComparator("revenue-shape", cmp))

	status, out := runInlineFeature(eng, "red-proof", customComparatorFeature)
	if status == 0 {
		t.Fatalf("expected RED: a failing custom verdict must fail the suite\n%s", out)
	}
	if !strings.Contains(out, "revenue below the 4 USD floor") {
		t.Fatalf("output must carry the comparator's OWN reason, got:\n%s", out)
	}
	if !strings.Contains(out, "revenue-shape") {
		t.Fatalf("output must name the comparator, got:\n%s", out)
	}
	// The comparator really ran — the failure is a verdict, not a lookup error.
	if len(cmp.comparedAs) != 1 {
		t.Fatalf("Compare called %d time(s), want 1: the suite failed before reaching the comparator", len(cmp.comparedAs))
	}
}

// TestCustomComparatorParseError is the other half of US4: a parser that errors must
// fail the run LOUDLY rather than skip the assertion. A skipped assertion is the
// silent-fallback failure Constitution IV exists to prevent, and it is indistinguishable
// from success in a report.
//
// Falsified by T028 — see the rehearsal transcript at the foot of this file.
func TestCustomComparatorParseError(t *testing.T) {
	cmp := &revenueShape{parseErr: errors.New("detonated")}
	eng := customComparatorEngine(t, withComparator("revenue-shape", cmp))

	status, out := runInlineFeature(eng, "red-proof", customComparatorFeature)
	if status == 0 {
		t.Fatalf("expected RED: a parser error must fail the suite, not skip the assertion\n%s", out)
	}
	if !strings.Contains(out, "revenue-shape") {
		t.Fatalf("output must name the comparator, got:\n%s", out)
	}
	if !strings.Contains(out, "detonated") {
		t.Fatalf("output must carry the wrapped cause, got:\n%s", out)
	}
	// The parse failed, so Compare must never have been reached — a parse failure is
	// not an assertion failure, and must not be converted into one.
	if len(cmp.comparedAs) != 0 {
		t.Fatalf("Compare was called %d time(s) despite a parse error", len(cmp.comparedAs))
	}
}

// TestCustomComparatorRejectsNilExpectation closes the hole CodeRabbit found on PR
// #39: a parser that claims success while returning a nil expectation.
//
// The three failure modes in spec D5 all end in an error, and the spec states that
// none of them may produce a nil expectation or a passing step. A SUCCESSFUL parse
// returning nil was the fourth case nobody enumerated: the step forwarded nil to
// Compare, and a comparator that tolerates nil then returned a passing verdict — a
// green step that asserted nothing, which is exactly the silent success Constitution
// IV exists to prevent.
//
// The step refuses the nil rather than trusting the parser's claim. Note the limit of
// what this can catch: only an UNTYPED nil. A typed nil pointer — `var e *myExp;
// return e, nil` — is a non-nil interface and reaches Compare, where the comparator's
// own type assertion succeeds and it dereferences its own nil. That is genuinely the
// comparator's bug and not something the step can see without knowing the type.
func TestCustomComparatorRejectsNilExpectation(t *testing.T) {
	t.Run("nil expectation from a tolerant comparator does not pass", func(t *testing.T) {
		cmp := &nilTolerantComparator{}
		eng := customComparatorEngine(t, withComparator("nil-tolerant", cmp))
		w := &world{eng: eng, ctx: context.Background(), target: "bot"}

		err := w.comparatorSatisfiedByDoc("nil-tolerant", &godog.DocString{Content: `{}`})
		if err == nil {
			t.Fatal("a nil expectation must not produce a passing step")
		}
		if !strings.Contains(err.Error(), "nil-tolerant") {
			t.Errorf("error %q must name the comparator", err)
		}
		if cmp.compared != 0 {
			t.Errorf("Compare was called %d time(s); the nil must be refused before Compare", cmp.compared)
		}
	})

	t.Run("the refusal is not a blanket rejection of falsy expectations", func(t *testing.T) {
		// A zero-valued but non-nil expectation is legitimate and must still run: the
		// guard rejects nil, not emptiness.
		cmp := &revenueShape{}
		eng := customComparatorEngine(t, withComparator("revenue-shape", cmp))
		w := &world{eng: eng, ctx: context.Background(), target: "bot"}

		if err := w.comparatorSatisfiedByDoc("revenue-shape", &godog.DocString{Content: `{}`}); err != nil {
			t.Fatalf("a zero-valued expectation is valid and must reach Compare: %v", err)
		}
		if len(cmp.comparedAs) != 1 {
			t.Fatalf("Compare called %d time(s), want 1", len(cmp.comparedAs))
		}
	})
}

// sensitivityEngine builds an engine with a single request-scoped target under the
// given completeness mode ("settle" → bounded, "strict" → not), plus c registered as
// "revenue-shape".
func sensitivityEngine(t *testing.T, mode string, c core.Comparator) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			"web": {
				Adapter:        "http",
				MaxConcurrency: 1,
				Completeness:   config.Completeness{Mode: mode, Settle: 5 * time.Second},
			},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := engine.Build(cfg, st, cor, withComparator("revenue-shape", c))
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// TestCustomComparatorIsCompletenessSensitive pins SC-011 / FR-010 (spec D3): the
// Extend step marks its expectation completeness-SENSITIVE, so against a bounded
// (request-scoped, non-strict) target the engine attaches the ingestion-window
// qualifier, and against a strict target it does not.
//
// This exists because `sensitive` is a bare literal in the handler. Without an
// assertion reaching the engine, it could be flipped to false with every other gate in
// this feature staying green — turning a conservative caveat into an unsound green,
// the exact property feature 008 exists to protect. Falsified by flipping that literal
// and observing the bounded row go red (see the rehearsal note at the foot of this
// file).
//
// The step's own pass/fail is irrelevant here; only qualifier recording matters, so the
// comparator is set up to return a verdict rather than an error.
func TestCustomComparatorIsCompletenessSensitive(t *testing.T) {
	ev := qualEvidence()

	tests := []struct {
		name          string
		mode          string
		wantQualified bool
	}{
		{"bounded target qualifies the verdict", "settle", true},
		{"strict target does not", "strict", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp := &revenueShape{}
			eng := sensitivityEngine(t, tt.mode, cmp)
			w := &world{eng: eng, ctx: context.Background(), target: "web", ev: ev}

			doc := &godog.DocString{Content: `{"min": 4, "currency": "USD"}`}
			if err := w.comparatorSatisfiedByDoc("revenue-shape", doc); err != nil {
				t.Fatalf("step returned an error, so no verdict was produced: %v", err)
			}

			if got := len(w.lastQualifiers) > 0; got != tt.wantQualified {
				t.Fatalf("qualified=%v (qualifiers=%v), want %v", got, w.lastQualifiers, tt.wantQualified)
			}
			if tt.wantQualified {
				if q := w.lastQualifiers[0]; !strings.Contains(q, "trace-completeness") {
					t.Fatalf("qualifier = %q, want the canonical trace-completeness caveat", q)
				}
			}
		})
	}
}

// TestCustomComparatorDocNil pins FR-017: a malformed step with no docstring is
// rejected with a descriptive error BEFORE doc.Content is touched. Seven of the eight
// existing docstring handlers guard this; the eighth (responseBodyJSONContains,
// steps.go:543) does not and panics — a pre-existing defect recorded in this
// feature's research, not fixed here.
//
// Written and observed failing (by panic) before the guard existed, so the guard is
// proven rather than assumed. Mirrors TestResultMeansDocNil.
func TestCustomComparatorDocNil(t *testing.T) {
	w := &world{}
	err := w.comparatorSatisfiedByDoc("revenue-shape", nil)
	if err == nil {
		t.Fatal("expected a descriptive error for a nil docstring, got nil")
	}
	if !strings.Contains(err.Error(), "revenue-shape") {
		t.Fatalf("error %q must name the comparator so the author can locate the step", err)
	}
}

// TestCustomComparatorFromGherkin is US1: a comparator registered by name and
// implementing ExpectationParser is named from a .feature file, parses the docstring
// into its own type, and receives exactly that value at Compare.
//
// The round-trip assertions are deliberately part of this test from the start. A
// version asserting only `status == 0` would pass against a handler that fabricated a
// zero-value expectation, which is precisely the silent fallback Constitution IV
// forbids.
func TestCustomComparatorFromGherkin(t *testing.T) {
	cmp := &revenueShape{}
	eng := customComparatorEngine(t, withComparator("revenue-shape", cmp))

	feature := `Feature: custom comparator
  Scenario: a registered comparator is named from Gherkin
    Given the agent target "bot"
    When I run scenario "happy"
    Then the "revenue-shape" comparator is satisfied by:
      """
      {"min": 4, "currency": "USD"}
      """
`
	status, out := runInlineFeature(eng, "custom-comparator", feature)
	if status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out)
	}

	// The docstring reached ParseExpectation verbatim, exactly once.
	wantText := `{"min": 4, "currency": "USD"}`
	if len(cmp.parsedText) != 1 {
		t.Fatalf("ParseExpectation called %d time(s), want exactly 1: %q", len(cmp.parsedText), cmp.parsedText)
	}
	if cmp.parsedText[0] != wantText {
		t.Fatalf("ParseExpectation got %q, want the docstring verbatim %q", cmp.parsedText[0], wantText)
	}

	// Compare received the value ParseExpectation produced — not a nil, not a
	// zero value, not something the step invented.
	if len(cmp.comparedAs) != 1 {
		t.Fatalf("Compare called %d time(s), want exactly 1", len(cmp.comparedAs))
	}
	want := revenueShapeExpectation{Min: 4, Currency: "USD"}
	if got := cmp.comparedAs[0]; got != core.Expectation(want) {
		t.Fatalf("Compare received %#v, want %#v", got, want)
	}
}

// --- Mutation rehearsals (011 T013, T020, T027, T028; observed 2026-09-10) ---------
//
// Recorded because a red proof nobody watched go red proves nothing, and because two
// of these caught mistakes in the rehearsal itself rather than in the code.
//
// # T027 / T028 — the two red proofs are independently specific
//
// Two guards need two mutations. One mutation covering both would leave the other test
// red for its original reason and demonstrate nothing about either.
//
//	A. handler swallows the verdict:   `_ = w.checkExp(name, exp, true); return nil`
//	   -> TestCustomComparatorGoesRed     FAIL
//	      TestCustomComparatorParseError  PASS   <- the cross-check
//
//	B. handler swallows the parse error: `if err != nil { return nil }`
//	   -> TestCustomComparatorGoesRed     PASS   <- the cross-check
//	      TestCustomComparatorParseError  FAIL
//
// # T020 — routing through checkExp is load-bearing (FR-006)
//
// Replacing the checkExp call with a direct w.eng.Compare reddens BOTH
// TestCustomComparatorRejectedInMultirunScenario and the bounded row of
// TestCustomComparatorIsCompletenessSensitive — the single-run guard and qualifier
// recording (and judge accounting with it) are all things checkExp does and Compare
// does not.
//
// The first attempt at this rehearsal did not go red, and the bug was in the
// rehearsal: a `perl -0p` substitution with no /g replaced the FIRST match in the
// file, which is checkSensitive (:247), not this handler. Every completeness-sensitive
// built-in got the mutation while the handler under test kept routing through checkExp
// and passing. Recorded because "the mutation didn't fire" and "the guard is real" look
// identical from the test output alone.
//
// # T013 — the sensitivity literal is pinned, not decorative
//
// Flipping `sensitive` from true to false in the handler reddens the BOUNDED row of
// TestCustomComparatorIsCompletenessSensitive and leaves the strict row green. Without
// that test the literal could have been flipped with every other gate in this feature
// staying green, converting a conservative caveat into an unsound green.
