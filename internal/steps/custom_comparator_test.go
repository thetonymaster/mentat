package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	// fail, when set, makes Compare return a failing verdict.
	fail bool
}

func (c *revenueShape) Name() string { return "revenue-shape" }

func (c *revenueShape) ParseExpectation(text string) (core.Expectation, error) {
	c.parsedText = append(c.parsedText, text)
	if c.parseErr != nil {
		return nil, c.parseErr
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
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: name, Contents: []byte(contents)}},
		},
	}
	return suite.Run(), out.String()
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
