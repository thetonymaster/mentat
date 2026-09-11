package steps

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
)

// TestBuiltinStepWithSurplusArgumentIsRejected closes the hole 012 left open.
//
// Measured on godog v0.15.1 before this test existed: a built-in step carrying an
// argument its handler never declared runs on its captures alone, the argument is
// discarded by the `i < numIn` conversion loop, and the scenario reports PASSED. That is
// an unearned green on 40 steps, reachable by anyone who types a docstring under the
// wrong step — and it needs no custom comparator to reach, unlike the contributed-phrase
// half 012 closed.
func TestBuiltinStepWithSurplusArgumentIsRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		arg      *messages.PickleStepArgument
		wantSubs []string
		// wantNot guards the half a substring check cannot: a message must not promise
		// an outcome the runner does not produce. The surplus-argument cases DO report a
		// passing verdict (measured: status 0); the wrong-KIND case does not.
		wantNot []string
	}{
		{
			name:     "docstring on a step that takes none",
			text:     `the agent target "bot"`,
			arg:      &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: "{}"}},
			wantSubs: []string{"docstring", "silently discarded", "report a verdict", "agent target"},
		},
		{
			name:     "data table on a step that takes none",
			text:     `the agent target "bot"`,
			arg:      &messages.PickleStepArgument{DataTable: &messages.PickleTable{}},
			wantSubs: []string{"data table", "silently discarded", "report a verdict", "agent target"},
		},
		{
			name: "data table on a step that expects a docstring",
			text: "the run satisfies:",
			arg:  &messages.PickleStepArgument{DataTable: &messages.PickleTable{}},
			// No "verdict" promise here: measured with the check disabled, this step
			// fails loudly (status 1, "expected a docstring expression, got none")
			// rather than passing on an unread argument.
			wantSubs: []string{"data table", "docstring body", "is discarded", "fails instead"},
			wantNot:  []string{"report a verdict"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// No contributed phrases: this must hold for the overwhelmingly common
			// engine, which is the whole point — the defect predates phrases entirely.
			sa := newStepArguments(nil)
			got := sa.stepProblem(&messages.PickleStep{Text: tt.text, Argument: tt.arg})
			if got == "" {
				t.Fatal("a surplus step argument was accepted; godog discards it and the scenario reports PASSED on an expectation nobody read")
			}
			// The offending sentence must be quoted back. strconv.Quote, not the raw
			// text: the message renders it with %q, so a step text containing quotes
			// appears escaped. Asserting the raw form fails against a correct message.
			if !strings.Contains(got, strconv.Quote(tt.text)) {
				t.Errorf("message %q does not quote the offending step %q", got, tt.text)
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("message %q does not mention %q", got, want)
				}
			}
			for _, bad := range tt.wantNot {
				if strings.Contains(got, bad) {
					t.Errorf("message %q claims %q, which this case does not do — the step fails loudly instead of reporting a verdict", got, bad)
				}
			}
		})
	}
}

// TestBuiltinStepArgumentsAreDerivedFromHandlerSignatures pins that the expectation
// comes from the HANDLER, not from a hand-kept list beside it. A second list would be a
// second source of truth for the thing stepDefs exists to be the only source of.
func TestBuiltinStepArgumentsAreDerivedFromHandlerSignatures(t *testing.T) {
	t.Parallel()

	args := builtinStepArguments()
	if len(args) != len(stepDefs) {
		t.Fatalf("derived %d argument expectations for %d stepDefs rows; every row must be accounted for", len(args), len(stepDefs))
	}

	counts := map[string]int{}
	for _, a := range args {
		counts[a.want]++
	}
	// Measured from the handler signatures in steps.go at the time of writing. The
	// numbers are asserted so that ADDING a step with an argument, or changing a
	// handler's signature, has to be acknowledged here rather than slipping through.
	for want, n := range map[string]int{"docstring": 9, "data table": 2, "": 29} {
		if counts[want] != n {
			t.Errorf("%d rows expect %q, want %d", counts[want], want, n)
		}
	}
}

// TestBuiltinStepWithSurplusArgumentMakesTheRunRed is the end-to-end half, and the one
// that actually proves the defect is closed.
//
// The unit test above proves the CHECK reports a problem. This proves the RUNNER acts on
// it, which is a different claim: the check is invoked from the Before hook, and a guard
// that computes the right answer and is never consulted is indistinguishable from no
// guard at all from a user's point of view.
//
// # Measured before the fix (godog v0.15.1, recorded in R13)
//
// This exact feature file reported suite status 0 — "1 scenarios (1 passed)" — with the
// docstring never read. The step's handler takes only its capture, so the conversion loop
// (`i < numIn`) dropped the body without a word. An author who wrote an expectation as a
// docstring under the wrong step was told their assertion passed.
//
// The scenario must now fail BEFORE the SUT is driven: the check runs at scenario init,
// so a malformed suite never reaches the agent.
// # Mutation rehearsal (observed 2026-09-11)
//
// A red-on-bad test that has never been seen green-on-broken proves nothing, so the
// guard was removed and this test re-run. Mutated steps.go:107,
// `stepArgs := newStepArguments(resolved)` -> `stepArgs := StepArguments{}` — precisely
// the pre-fix state, since the old constructor returned an inert zero for an engine with
// no contributed phrases. Verified the edit landed by grepping for the marker first.
//
// RED, reproducing R13 exactly: "the suite PASSED with a docstring the step never read
// (status 0)", with the pretty formatter rendering the step line green and the docstring cyan under a bound
// `the result contains "hi"` as though it had been consumed. Reverted, re-observed green.
func TestBuiltinStepWithSurplusArgumentMakesTheRunRed(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, happyTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	// `the result contains "hi"` is TRUE of happyTrace, so this scenario passed on its
	// merits before the fix. That is deliberate: if the assertion itself were false the
	// test could not tell "red because the argument was rejected" from "red because the
	// assertion failed".
	feature := `Feature: unearned green
  Scenario: a surplus docstring on a built-in step that takes none
    Given the agent target "bot"
    When I run scenario "happy"
    And the result contains "hi"
      """
      {"this": "is never read by the step above"}
      """
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: mustInit(Initializer(eng)),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "unearned green", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("the suite PASSED with a docstring the step never read (status 0) — the unearned green is back\n%s", out.String())
	}
	if got := out.String(); !strings.Contains(got, "silently discarded") {
		t.Errorf("the failure does not explain why; the author needs to be told the body would be discarded\n%s", got)
	}
}

// TestBuiltinHandlerArityMatchesItsPatternAndArgument is the invariant that makes
// handlerArgumentKind safe, and it is the guard the count test above cannot be.
//
// Measured on the pinned godog v0.15.1: a handler declared `func(name, body string) error`
// bound to a ONE-capture pattern receives the docstring body in `body` —
// `status=0 called=true seen="PAYLOAD"` — because shouldBeString
// (internal/models/stepdef.go:285-296) converts a *messages.PickleStepArgument into a
// plain string parameter. handlerArgumentKind classifies that handler as taking NO
// argument, so the check would reject every scenario using the step: a FALSE RED, which
// is worse than the unearned green this file exists to close.
//
// Changing a built-in from `(s string)` to `(s, body string)` keeps its classification ""
// and keeps the totals at 29, so TestBuiltinStepArgumentsAreDerivedFromHandlerSignatures
// stays green through exactly that drift. Counting parameters against capture groups is
// what actually catches it.
// # Mutation rehearsal (observed 2026-09-11)
//
// Applied the exact drift described above: `resultContains(s string)` ->
// `resultContains(s, body string)` in steps.go. Verified the edit landed by grep first.
// Note for whoever repeats it: qualifier_test.go calls resultContains directly, so that
// call site needs the extra argument too or the package will not compile and the
// rehearsal proves nothing.
//
//	THIS test:  FAIL — step "^the result contains \"([^\"]*)\"$": handler takes 2
//	            parameters, want 1 (1 capture groups, no argument)
//	count test: PASS — classification stayed "", totals stayed 9/2/29
//
// The count test being green under the mutation is the point: it is why this test has to
// exist. Reverted, both re-observed green.
func TestBuiltinHandlerArityMatchesItsPatternAndArgument(t *testing.T) {
	t.Parallel()

	w := &world{}
	for _, sd := range stepDefs {
		re, err := regexp.Compile(sd.pattern)
		if err != nil {
			t.Errorf("step %q: pattern does not compile: %v", sd.pattern, err)
			continue
		}
		h := sd.handler(w)
		if err := checkBuiltinArity(sd.pattern, re, h, handlerArgumentKind(h)); err != nil {
			t.Error(err)
		}
	}
}

// TestBuiltinExampleCarriesTheArgumentItsHandlerDeclares pins the promise docs/steps.md
// makes to authors: "Each step takes exactly the argument shown in its example."
//
// A generated page asserting a property of 40 rows with nothing checking it is the same
// shape as the comments this feature spent three rounds correcting. The example is also
// the only place an author can SEE the rule, so an example missing its docstring teaches
// the opposite of what the check enforces.
var reTableRow = regexp.MustCompile(`(?m)^\s*\|.*\|\s*$`)

func TestBuiltinExampleCarriesTheArgumentItsHandlerDeclares(t *testing.T) {
	t.Parallel()

	w := &world{}
	for _, sd := range stepDefs {
		kind := handlerArgumentKind(sd.handler(w))
		// The reference renders a docstring example as a `"""` block and a data-table
		// example as `|` rows; a step taking neither shows a bare sentence.
		hasDoc := strings.Contains(sd.example, `"""`)
		// A table ROW, not a bare pipe. A CEL example like `a || b`, a regex
		// alternation, or a pipe in prose would otherwise be read as a data table and
		// produce a false red blaming the handler.
		hasTable := reTableRow.MatchString(sd.example)
		var shown string
		switch {
		case hasDoc:
			shown = "docstring"
		case hasTable:
			shown = "data table"
		}
		if shown != kind {
			t.Errorf("step %q: example shows %q but its handler declares %q\nexample: %s",
				sd.pattern, shown, kind, sd.example)
		}
	}
}

// TestCheckBuiltinArityRejectsAStringTypedArgument covers the error path the drift test
// only reaches when a real row is wrong — and pins the message, because the whole value
// of this guard is telling the next maintainer WHY two parameters for one capture group
// is a defect rather than a harmless extra.
func TestCheckBuiltinArityRejectsAStringTypedArgument(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`^the widget "([^"]+)"$`)
	// The shape godog would happily feed the docstring into, and handlerArgumentKind
	// would classify as taking no argument at all.
	h := func(_, _ string) error { return nil }

	err := checkBuiltinArity(`^the widget "([^"]+)"$`, re, h, handlerArgumentKind(h))
	if err == nil {
		t.Fatal("a two-parameter handler on a one-capture pattern was accepted; godog routes a step argument into that second string and the classification would be wrong")
	}
	// "1 capture group", singular — the pluralisation exists so the message reads as an
	// instruction rather than as generated output.
	for _, want := range []string{"takes 2 parameters", "want 1", "1 capture group,", "plain string parameter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	// The agreeing shape must be accepted, or the guard would reject every valid row.
	ok := func(_ string) error { return nil }
	if err := checkBuiltinArity(`^the widget "([^"]+)"$`, re, ok, handlerArgumentKind(ok)); err != nil {
		t.Errorf("a matching handler was rejected: %v", err)
	}

	// godog allows a leading context.Context and discounts it (stepdef.go), so a
	// context-taking handler is legal and must not be reported as a repo defect.
	withCtx := func(_ context.Context, _ string) error { return nil }
	if err := checkBuiltinArity(`^the widget "([^"]+)"$`, re, withCtx, handlerArgumentKind(withCtx)); err != nil {
		t.Errorf("a context-taking handler was rejected: %v", err)
	}
}

// TestCheckBuiltinArityExplainsEachDirectionDifferently pins that the FOUR shapes carry
// four explanations, because each has a different consequence and one text covering them
// all would be false for three of them. Every branch below was measured against the
// pinned godog v0.15.1 before being asserted here.
//
// It said "three" until a fourth review round measured the fourth. The name says
// "direction", and direction is exactly what turned out NOT to determine the consequence:
// too-many-parameters means one thing on a row declaring no argument and something else
// entirely on a row declaring one. The missing row is why the wrong explanation survived
// a round — the test asserted each direction and there were never only three.
func TestCheckBuiltinArityExplainsEachDirectionDifferently(t *testing.T) {
	t.Parallel()

	two := regexp.MustCompile(`^the (\w+) and (\w+)$`)
	twoDoc := regexp.MustCompile(`^the (\w+) and (\w+):$`)

	tests := []struct {
		name    string
		re      *regexp.Regexp
		h       any
		wantSub string
	}{
		{
			// Measured: called=true seen="PAYLOAD" — the argument lands in the surplus string.
			name:    "too many parameters, no declared argument",
			re:      regexp.MustCompile(`^the widget "([^"]+)"$`),
			h:       func(_, _ string) error { return nil },
			wantSub: "would be REJECTED by this check",
		},
		{
			// TWO surplus parameters and NO declared argument. godog can supply at most
			// capture-count+1, so this exceeds the ceiling and is refused on arity — the
			// same mechanism as the row below, despite `kind == ""` matching the row
			// above it. Measured: `func(a, b, c string)` on a 1-capture pattern ->
			// status=1, "expected 3 arguments, matched 2 from step".
			//
			// This row exists because the switch first split on `kind`, which correlates
			// with the ceiling without being it. That left this shape explained as
			// string-routing, which it is not.
			name:    "two surplus parameters, no declared argument",
			re:      regexp.MustCompile(`^the widget "([^"]+)"$`),
			h:       func(_, _, _ string) error { return nil },
			wantSub: "refuse the handler outright",
		},
		{
			// Measured: status=1 ran=false, "func expected more arguments than given:
			// expected 3 arguments, matched 2 from step". godog refuses on arity, so
			// nothing is routed into a string and this check rejects nothing — the
			// direction alone does not decide the consequence.
			//
			// This row exists because the switch was written as THREE branches and this
			// shape fell into the one above it, inheriting an explanation false in all
			// three of its clauses. The test asserting "each direction" had no row for it,
			// which is why the false claim survived a round of review.
			name:    "too many parameters, docstring declared",
			re:      regexp.MustCompile(`^the widget (\w+):$`),
			h:       func(_, _ string, _ *godog.DocString) error { return nil },
			wantSub: "refuse the handler outright",
		},
		{
			// Measured: ran=true seen="one" — the second capture vanishes.
			name:    "too few parameters, no declared argument",
			re:      two,
			h:       func(_ string) error { return nil },
			wantSub: "surplus CAPTURE would be discarded",
		},
		{
			// Measured: status=1 ran=false, "cannot convert argument 1 ... to
			// *messages.PickleDocString" — nothing is discarded, the handler never runs.
			name:    "too few parameters, docstring declared",
			re:      twoDoc,
			h:       func(_ string, _ *godog.DocString) error { return nil },
			wantSub: "conversion error before the handler ran",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkBuiltinArity(tt.re.String(), tt.re, tt.h, handlerArgumentKind(tt.h))
			if err == nil {
				t.Fatal("mismatched arity was accepted")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not explain this direction (%q)", err, tt.wantSub)
			}
		})
	}
}
