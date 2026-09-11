package steps

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
)

// --- T019: the godog handler bridge ---

// TestMakeStepHandlerDeliversEveryCapture is the guard for the one thing godog does
// silently and destructively.
//
// godog's argument conversion loop runs `i < numIn` (internal/models/stepdef.go:58):
// too FEW arguments is an error, but SURPLUS arguments are discarded without a word.
// A hand-written fixed-arity handler bound to a pattern with more capture groups than
// it declares therefore loses captures with no diagnostic at all — the comparator
// receives a truncated expectation and asserts something the author never wrote.
//
// The bridge derives its arity from the SAME compiled pattern godog matches against,
// which makes the two structurally unable to disagree. This test pins that: every
// capture must arrive, at 0, 1 and 3 groups.
func TestMakeStepHandlerDeliversEveryCapture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		arity    int
		wantsDoc bool
		call     []any
		wantCaps []string
		wantDoc  string
	}{
		{
			name:     "zero captures",
			arity:    0,
			call:     nil,
			wantCaps: []string{},
		},
		{
			name:     "one capture",
			arity:    1,
			call:     []any{"quarterly"},
			wantCaps: []string{"quarterly"},
		},
		{
			name:     "three captures, none dropped",
			arity:    3,
			call:     []any{"alpha", "beta", "gamma"},
			wantCaps: []string{"alpha", "beta", "gamma"},
		},
		{
			name:     "empty captures are preserved, not elided",
			arity:    3,
			call:     []any{"", "beta", ""},
			wantCaps: []string{"", "beta", ""},
		},
		{
			name:     "captures plus docstring",
			arity:    2,
			wantsDoc: true,
			call:     []any{"a", "b", &godog.DocString{Content: "body"}},
			wantCaps: []string{"a", "b"},
			wantDoc:  "body",
		},
		{
			name:     "docstring only",
			arity:    0,
			wantsDoc: true,
			call:     []any{&godog.DocString{Content: "just a body"}},
			wantCaps: []string{},
			wantDoc:  "just a body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotCaps []string
			var gotBody string
			var gotHasBody bool
			h := makeStepHandler(tt.arity, tt.wantsDoc, func(caps []string, body string, hasBody bool) error {
				gotCaps, gotBody, gotHasBody = caps, body, hasBody
				return nil
			})

			fv := reflect.ValueOf(h)
			if fv.Kind() != reflect.Func {
				t.Fatalf("handler is %v, want a func", fv.Kind())
			}

			// The synthesized signature must be exactly what godog accepts: N strings,
			// optionally a *godog.DocString, returning error. godog rejects []string
			// and variadic handlers outright, so this is not a stylistic assertion.
			wantIn := tt.arity
			if tt.wantsDoc {
				wantIn++
			}
			if fv.Type().NumIn() != wantIn {
				t.Fatalf("handler takes %d args, want %d", fv.Type().NumIn(), wantIn)
			}
			if fv.Type().IsVariadic() {
				t.Fatal("handler is variadic; godog rejects variadic step handlers")
			}
			for i := 0; i < tt.arity; i++ {
				if fv.Type().In(i).Kind() != reflect.String {
					t.Errorf("arg %d is %v, want string", i, fv.Type().In(i).Kind())
				}
			}
			if fv.Type().NumOut() != 1 || fv.Type().Out(0).String() != "error" {
				t.Fatalf("handler returns %v, want exactly (error)", fv.Type())
			}

			args := make([]reflect.Value, 0, len(tt.call))
			for _, a := range tt.call {
				args = append(args, reflect.ValueOf(a))
			}
			out := fv.Call(args)
			if !out[0].IsNil() {
				t.Fatalf("handler returned %v, want nil", out[0].Interface())
			}

			if len(gotCaps) != len(tt.wantCaps) {
				t.Fatalf("parser received %d captures %q, want %d %q — godog drops surplus args SILENTLY, so a mismatch here is invisible in production",
					len(gotCaps), gotCaps, len(tt.wantCaps), tt.wantCaps)
			}
			for i := range tt.wantCaps {
				if gotCaps[i] != tt.wantCaps[i] {
					t.Errorf("capture[%d] = %q, want %q", i, gotCaps[i], tt.wantCaps[i])
				}
			}
			switch {
			case tt.wantDoc == "" && gotHasBody:
				t.Errorf("parser was told a body is present (%q) for a phrase that declares none", gotBody)
			case tt.wantDoc != "" && !gotHasBody:
				t.Errorf("parser was told no body is present, want %q", tt.wantDoc)
			case gotBody != tt.wantDoc:
				t.Errorf("docstring body = %q, want %q", gotBody, tt.wantDoc)
			}
		})
	}
}

// TestMakeStepHandlerPropagatesError pins that the bridge is a conduit, not a filter:
// an error from the parser must reach godog unchanged, because godog's step error is
// what the After hook turns into the scenario verdict. Swallowing it here would make
// a failed assertion report green.
func TestMakeStepHandlerPropagatesError(t *testing.T) {
	t.Parallel()

	want := errStub{"parser said no"}
	h := makeStepHandler(1, false, func([]string, string, bool) error { return want })
	out := reflect.ValueOf(h).Call([]reflect.Value{reflect.ValueOf("x")})
	if out[0].IsNil() {
		t.Fatal("handler returned nil for a parser that returned an error")
	}
	if got := out[0].Interface().(error); !errors.Is(got, want) {
		t.Fatalf("handler returned %v, want the parser's own error %v", got, want)
	}
}

type errStub struct{ msg string }

func (e errStub) Error() string { return e.msg }

// TestGodogAcceptsTheSynthesizedHandler is the test that justifies the whole bridge.
//
// It is not enough to assert the reflect type looks right: godog validates handler
// signatures itself and rejects []string and variadic handlers (reflect.Slice accepts
// []byte only). The only way to know a synthesized handler is acceptable is to hand it
// to a real suite and watch the step RUN.
//
// Strict is on so an unbound or undefined step is a suite failure rather than a
// silently-skipped step that would make this test vacuously green.
func TestGodogAcceptsTheSynthesizedHandler(t *testing.T) {
	t.Parallel()

	var got []string
	h := makeStepHandler(3, false, func(caps []string, _ string, _ bool) error {
		got = caps
		return nil
	})

	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the (\w+) is (\w+) and (\w+)$`, h)
		},
		Options: &godog.Options{
			Format: "pretty",
			Output: &out,
			Strict: true,
			FeatureContents: []godog.Feature{{
				Name: "bridge",
				Contents: []byte("Feature: bridge\n" +
					"  Scenario: a synthesized handler binds and runs\n" +
					"    Then the alpha is beta and gamma\n"),
			}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("godog rejected or failed the synthesized handler (status %d)\n%s", status, out.String())
	}
	want := []string{"alpha", "beta", "gamma"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("handler received %q, want %q\n%s", got, want, out.String())
	}
}

// --- T020/T021: seam routing and error paths ---

// phraseComparator is a comparator that contributes phrases. Which seams it
// implements is controlled per-test, because "which seam serves this phrase" is the
// property under test and a stub implementing both would hide a routing mistake.
type phraseComparator struct {
	gotCaps []string
	exp     core.Expectation
	err     error
	nilExp  bool
}

func (c *phraseComparator) Name() string { return "revenue-shape" }
func (c *phraseComparator) Compare(_ context.Context, _ core.Evidence, e core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}
func (c *phraseComparator) ParseCaptures(caps []string) (core.Expectation, error) {
	c.gotCaps = caps
	if c.err != nil {
		return nil, c.err
	}
	if c.nilExp {
		return nil, nil
	}
	return c.exp, nil
}

// captureOnly implements CaptureParser but NOT ExpectationParser.
type captureOnly struct{ *phraseComparator }

// neitherSeam contributes a phrase but implements no parser seam at all. It is
// declared standalone rather than embedding phraseComparator: embedding would
// PROMOTE ParseCaptures and quietly satisfy the very interface this stub exists to
// not satisfy.
type neitherSeam struct{}

func (neitherSeam) Name() string { return "revenue-shape" }
func (neitherSeam) Compare(_ context.Context, _ core.Evidence, _ core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

// # Mutation rehearsals for the routing and error paths (2026-09-11)
//
// Each mutation was applied under an assertion that the source edit actually landed,
// because 011 hit a rehearsal that silently failed to apply and "the mutation didn't
// fire" is indistinguishable from "the guard is real" from test output alone.
//
//	C. Inverted seam() so captures route to the docstring parser and vice versa.
//	   RED in TestContributedPhraseSeamRouting on two rows.
//	D. Deleted the `exp == nil` guard in guard(). RED on "parser returns a nil
//	   expectation with NO error" — the case that would otherwise let a comparator
//	   tolerating nil return a PASSING verdict for a step that asserted nothing.
//	E. Returned the parse error unwrapped instead of wrapping it with the comparator
//	   and pattern. RED: `error "bad shape" does not mention "revenue-shape"`. This is
//	   the one that keeps an author from having to guess which of their comparators
//	   rejected their sentence.
//	F. Made the captures path drop its final capture (in phrase.go). RED in the FACADE
//	   equivalence test: "routes disagree on the verdict: phrase Pass=false, generic
//	   Pass=true". Recorded here because it is the mutation that proves SC-001's
//	   equivalence assertion has teeth.
//
// All reverted; tests re-observed green.
//
// TestContributedPhraseSeamRouting pins the routing table (D6). Which parser serves a
// phrase is a property of its PATTERN, decided once at build, never a runtime guess —
// a guess would make the same sentence behave differently depending on what the
// comparator happened to implement.
func TestContributedPhraseSeamRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		arity    int
		wantsDoc bool
		wantSeam string // "captures" | "docstring"
	}{
		{name: "captures only routes to the capture parser", arity: 2, wantsDoc: false, wantSeam: "captures"},
		{name: "docstring only, no captures, routes to 011's ExpectationParser", arity: 0, wantsDoc: true, wantSeam: "docstring"},
		{name: "captures plus docstring routes to the capture parser", arity: 1, wantsDoc: true, wantSeam: "captures"},
		{name: "neither captures nor docstring routes to the capture parser", arity: 0, wantsDoc: false, wantSeam: "captures"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cp := contributedPhrase{
				comparator: "revenue-shape",
				phrase:     core.ContributedPhrase{Pattern: "^x$"},
				arity:      tt.arity,
				wantsDoc:   tt.wantsDoc,
			}
			if got := cp.seam(); got != tt.wantSeam {
				t.Fatalf("seam() = %q, want %q", got, tt.wantSeam)
			}
		})
	}
}

// TestContributedPhraseAppendsDocstringAsFinalCapture pins the "captures + docstring"
// row: the body arrives as the LAST element of the capture list, so a comparator sees
// one ordered argument list rather than two channels it must reconcile.
func TestContributedPhraseAppendsDocstringAsFinalCapture(t *testing.T) {
	t.Parallel()

	base := &phraseComparator{exp: "ok"}
	cp := contributedPhrase{
		comparator: "revenue-shape",
		phrase:     core.ContributedPhrase{Pattern: `^the revenue is (\w+):$`},
		arity:      1,
		wantsDoc:   true,
	}
	exp, err := cp.parse(captureOnly{base}, []string{"quarterly"}, `{"min":4}`, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if exp != "ok" {
		t.Fatalf("parse returned %v, want the comparator's own expectation", exp)
	}
	want := []string{"quarterly", `{"min":4}`}
	if strings.Join(base.gotCaps, "|") != strings.Join(want, "|") {
		t.Fatalf("parser received %q, want %q — the docstring must arrive as the final capture", base.gotCaps, want)
	}
}

// TestContributedPhraseErrorPaths pins every rejection (Constitution IV). Each message
// must name the comparator and the offending value: an error that says only "parse
// failed" makes an author guess which of their comparators rejected their sentence.
func TestContributedPhraseErrorPaths(t *testing.T) {
	t.Parallel()

	const pattern = `^the revenue is (\w+)$`

	tests := []struct {
		name     string
		cmp      core.Comparator
		cp       contributedPhrase
		caps     []string
		body     string
		hasBody  bool
		wantSubs []string
	}{
		{
			name: "parser returns an error: wrapped, naming the comparator",
			cmp:  captureOnly{&phraseComparator{err: errStub{"bad shape"}}},
			cp:   contributedPhrase{comparator: "revenue-shape", phrase: core.ContributedPhrase{Pattern: pattern}, arity: 1},
			caps: []string{"quarterly"},
			// The comparator, the pattern and the underlying cause must all survive.
			wantSubs: []string{"revenue-shape", strconv.Quote(pattern), "bad shape"},
		},
		{
			name: "parser returns a nil expectation with NO error: refused, not trusted",
			cmp:  captureOnly{&phraseComparator{nilExp: true}},
			cp:   contributedPhrase{comparator: "revenue-shape", phrase: core.ContributedPhrase{Pattern: pattern}, arity: 1},
			caps: []string{"quarterly"},
			// Forwarding the nil would let a comparator that tolerates nil return a
			// PASSING verdict for a step that asserted nothing.
			wantSubs: []string{"revenue-shape", "nil expectation", "no error"},
		},
		{
			name:     "contributes a phrase but implements no capture parser",
			cmp:      neitherSeam{},
			cp:       contributedPhrase{comparator: "revenue-shape", phrase: core.ContributedPhrase{Pattern: pattern}, arity: 1},
			caps:     []string{"quarterly"},
			wantSubs: []string{"revenue-shape", "CaptureParser"},
		},
		{
			name:     "docstring-routed phrase whose comparator has no ExpectationParser",
			cmp:      captureOnly{&phraseComparator{}},
			cp:       contributedPhrase{comparator: "revenue-shape", phrase: core.ContributedPhrase{Pattern: `^the revenue is:$`}, arity: 0, wantsDoc: true},
			body:     "x",
			hasBody:  true,
			wantSubs: []string{"revenue-shape", "ExpectationParser"},
		},
		{
			name:     "declared docstring missing from the step",
			cmp:      captureOnly{&phraseComparator{exp: "ok"}},
			cp:       contributedPhrase{comparator: "revenue-shape", phrase: core.ContributedPhrase{Pattern: `^the revenue is (\w+):$`}, arity: 1, wantsDoc: true},
			caps:     []string{"quarterly"},
			hasBody:  false,
			wantSubs: []string{"revenue-shape", "docstring"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exp, err := tt.cp.parse(tt.cmp, tt.caps, tt.body, tt.hasBody)
			if err == nil {
				t.Fatalf("parse succeeded (exp=%v); this path must be a hard, descriptive error", exp)
			}
			if exp != nil {
				t.Errorf("parse returned both an expectation (%v) and an error; a rejected parse must yield no expectation", exp)
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not mention %q", err.Error(), sub)
				}
			}
		})
	}
}

// TestContributedPhraseAcceptsZeroValuedExpectation is the other side of the nil
// guard, and the reason it checks nil ONLY. A zero-valued but non-nil expectation is
// legitimate — inspecting its shape would require knowing the expectation type, which
// is exactly the coupling this seam removes.
func TestContributedPhraseAcceptsZeroValuedExpectation(t *testing.T) {
	t.Parallel()

	cp := contributedPhrase{comparator: "revenue-shape", phrase: core.ContributedPhrase{Pattern: "^x$"}, arity: 0}
	exp, err := cp.parse(captureOnly{&phraseComparator{exp: revenueShapeExpectation{}}}, nil, "", false)
	if err != nil {
		t.Fatalf("a zero-valued expectation must be accepted, got error: %v", err)
	}
	if exp == nil {
		t.Fatal("parse returned nil for a legitimate zero-valued expectation")
	}
}

// mustInit unwraps a scenario initializer, panicking if building it failed.
//
// Building one can now fail: an engine whose comparators contribute a malformed
// phrase is rejected at composition rather than when a scenario happens to use it.
// Tests that do not exercise that path use this helper, so a genuine composition
// failure surfaces loudly (with the test name and a stack) instead of silently
// binding a nil initializer.
//
// It takes no *testing.T on purpose: Go permits f(g()) only when g()'s results are
// the SOLE argument, so a t parameter would force every call site to split into
// three lines for no gain. Tests that assert on a composition failure call the
// constructor directly.
func mustInit(init func(*godog.ScenarioContext), err error) func(*godog.ScenarioContext) {
	if err != nil {
		panic("build scenario initializer: " + err.Error())
	}
	return init
}

// --- T028: the L3 meta-test ---

// l3Phrase contributes a phrase and fails its assertion, with a distinctive reason.
type l3Phrase struct{ pass bool }

func (c *l3Phrase) Name() string { return "revenue-shape" }
func (c *l3Phrase) ContributedPhrases() []core.ContributedPhrase {
	return []core.ContributedPhrase{{
		Pattern: `^the revenue floor is (\d+) (\w+)$`,
		Group:   "Revenue",
		Summary: "Asserts the revenue floor is reachable.",
		Example: `Then the revenue floor is 4 USD`,
	}}
}
func (c *l3Phrase) ParseCaptures(caps []string) (core.Expectation, error) {
	return strings.Join(caps, " "), nil
}
func (c *l3Phrase) Compare(_ context.Context, _ core.Evidence, e core.Expectation) (core.Verdict, error) {
	if c.pass {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{"floor " + e.(string) + " is unreachable"}}, nil
}

// TestContributedPhraseGoesRedOnAFalseClaim is the L3 meta-test for 012
// (Constitution V): a scenario whose contributed-phrase assertion is FALSE must make
// the run RED, carrying the comparator's OWN reason.
//
// A test framework that has not been proven to fail on bad behaviour is
// unfalsifiable — every green it reports afterwards is unearned. The passing case is
// asserted in the same test precisely so a phrase route that failed EVERYTHING would
// not be mistaken for one that works.
//
// It lives in internal/steps rather than e2e/ deliberately (011's R6): the e2e lane
// drives a prebuilt cmd/mentat binary, which cannot contain a Go-registered
// comparator, so the proof is impossible to write there.
func TestContributedPhraseGoesRedOnAFalseClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pass       bool
		wantStatus int
		wantReason string
	}{
		{name: "a true claim is green", pass: true, wantStatus: 0},
		{name: "a false claim is RED, with the comparator's own reason", pass: false, wantStatus: 1, wantReason: "floor 4 USD is unreachable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eng := customComparatorEngine(t, withComparator("revenue-shape", &l3Phrase{pass: tt.pass}))
			status, out := runInlineFeature(eng, "l3", `Feature: l3
  Scenario: a contributed phrase asserts something false
    Given the agent target "bot"
    When I run scenario "x"
    Then the revenue floor is 4 USD
`)
			if status != tt.wantStatus {
				t.Fatalf("suite status %d, want %d — a framework that cannot go red on a false claim proves nothing\n%s", status, tt.wantStatus, out)
			}
			if tt.wantReason != "" && !strings.Contains(out, tt.wantReason) {
				t.Errorf("output does not carry the comparator's own reason %q; a generic failure message would hide WHY the assertion failed\n%s", tt.wantReason, out)
			}
		})
	}
}

// --- T035/T036/T037: validation rules V1–V5 ---

// validationComparator contributes whatever phrases a test hands it.
type validationComparator struct {
	name    string
	phrases []core.ContributedPhrase
}

func (c *validationComparator) Name() string { return c.name }
func (c *validationComparator) Compare(_ context.Context, _ core.Evidence, _ core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}
func (c *validationComparator) ContributedPhrases() []core.ContributedPhrase { return c.phrases }

// seamless contributes phrases but implements NO parser seam — declared standalone so
// embedding cannot promote one in by accident.
type seamless struct {
	name    string
	phrases []core.ContributedPhrase
}

func (c *seamless) Name() string { return c.name }
func (c *seamless) Compare(_ context.Context, _ core.Evidence, _ core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}
func (c *seamless) ContributedPhrases() []core.ContributedPhrase { return c.phrases }

func (c *validationComparator) ParseCaptures(caps []string) (core.Expectation, error) {
	return strings.Join(caps, ","), nil
}

// Implements BOTH parser seams so a validation test can use docstring-routed and
// capture-routed phrases from one stub. Tests that need a MISSING seam use `seamless`.
func (c *validationComparator) ParseExpectation(text string) (core.Expectation, error) {
	return text, nil
}

func wellFormed(pattern string) core.ContributedPhrase {
	return core.ContributedPhrase{
		Pattern: pattern,
		Group:   "Custom",
		Summary: "A contributed phrase.",
		Example: "Then something happens",
	}
}

// TestPhraseValidationRules pins V1–V5 ([data-model.md] §1). Every rule fails the
// ENGINE BUILD and names the contributor and the offending value.
//
// Naming both is the whole point. A consumer may register a dozen comparators from
// several modules; "invalid pattern" tells them a defect exists, not which of their
// dependencies shipped it.
//
// # Mutation rehearsals, one per rejection path (2026-09-11)
//
// Each guard was disabled in turn, under an assertion that the source edit applied,
// and the rows that went red were recorded:
//
//	guard disabled          rows red
//	V1 (compiles)           1  — "pattern does not compile"
//	V2 (anchored)           3  — start, end, and the escaped-dollar edge case
//	V3 (duplicate phrase)   1  — "naming BOTH"
//	V4 (built-in clash)     1  — "identical to a built-in step's"
//	V5 (non-blank fields)   3  — Group, Summary, Example
//	seam present            1  — "implements no capture parser"
//
// Each mutation reddened ONLY its own rows. That is the stronger result: it shows no
// rule is redundant with another, and no row is passing for a reason other than the
// one it names. All reverted; re-observed green.
func TestPhraseValidationRules(t *testing.T) {
	t.Parallel()

	// A real built-in pattern, for the V4 collision row.
	builtin := StepDocs()[0].Pattern

	tests := []struct {
		name     string
		cmps     []core.Comparator
		wantSubs []string
	}{
		{
			name:     "V1: pattern does not compile",
			cmps:     []core.Comparator{&validationComparator{name: "bad-regex", phrases: []core.ContributedPhrase{wellFormed(`^the (unclosed$`)}}},
			wantSubs: []string{"bad-regex", "^the (unclosed$"},
		},
		{
			name:     "V2: pattern is not anchored at the start",
			cmps:     []core.Comparator{&validationComparator{name: "unanchored-start", phrases: []core.ContributedPhrase{wellFormed(`the revenue is fine$`)}}},
			wantSubs: []string{"unanchored-start", "anchored"},
		},
		{
			name:     "V2: pattern is not anchored at the end",
			cmps:     []core.Comparator{&validationComparator{name: "unanchored-end", phrases: []core.ContributedPhrase{wellFormed(`^the revenue is fine`)}}},
			wantSubs: []string{"unanchored-end", "anchored"},
		},
		{
			name: "V2: a trailing ESCAPED dollar is not an anchor",
			// `\$` matches a literal dollar sign; the pattern is unanchored and can
			// swallow a neighbouring step. This is the edge case R8 calls out.
			cmps:     []core.Comparator{&validationComparator{name: "escaped-dollar", phrases: []core.ContributedPhrase{wellFormed(`^the price is 5\$`)}}},
			wantSubs: []string{"escaped-dollar", "anchored"},
		},
		{
			name: "V3: two comparators contribute an identical pattern, naming BOTH",
			cmps: []core.Comparator{
				&validationComparator{name: "alpha-cmp", phrases: []core.ContributedPhrase{wellFormed(`^the reading is fine$`)}},
				&validationComparator{name: "zeta-cmp", phrases: []core.ContributedPhrase{wellFormed(`^the reading is fine$`)}},
			},
			wantSubs: []string{"alpha-cmp", "zeta-cmp", "^the reading is fine$"},
		},
		{
			name:     "V4: pattern is identical to a built-in step's",
			cmps:     []core.Comparator{&validationComparator{name: "shadower", phrases: []core.ContributedPhrase{wellFormed(builtin)}}},
			wantSubs: []string{"shadower", strconv.Quote(builtin), "built-in"},
		},
		{
			name: "V5: blank Group",
			cmps: []core.Comparator{&validationComparator{name: "blank-group", phrases: []core.ContributedPhrase{
				{Pattern: `^ok$`, Group: "  ", Summary: "s", Example: "e"}}}},
			wantSubs: []string{"blank-group", "Group"},
		},
		{
			name: "V5: blank Summary",
			cmps: []core.Comparator{&validationComparator{name: "blank-summary", phrases: []core.ContributedPhrase{
				{Pattern: `^ok$`, Group: "g", Summary: "", Example: "e"}}}},
			wantSubs: []string{"blank-summary", "Summary"},
		},
		{
			name: "V5: blank Example",
			cmps: []core.Comparator{&validationComparator{name: "blank-example", phrases: []core.ContributedPhrase{
				{Pattern: `^ok$`, Group: "g", Summary: "s", Example: ""}}}},
			wantSubs: []string{"blank-example", "Example"},
		},
		{
			name:     "contributes a phrase but implements no capture parser",
			cmps:     []core.Comparator{&seamless{name: "no-parser", phrases: []core.ContributedPhrase{wellFormed(`^the reading is (\w+)$`)}}},
			wantSubs: []string{"no-parser", "CaptureParser"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := make([]engine.Option, 0, len(tt.cmps))
			for _, c := range tt.cmps {
				opts = append(opts, withComparator(c.Name(), c))
			}
			eng := customComparatorEngine(t, opts...)

			_, err := resolvePhrases(eng)
			if err == nil {
				t.Fatal("validation accepted a malformed phrase; every rule must fail the engine build")
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not mention %q", err.Error(), sub)
				}
			}
		})
	}
}

// TestPhraseValidationAcceptsWellFormedPhrases is the other side: validation must not
// be so eager that nothing passes. A test suite where every row is a rejection cannot
// distinguish "correctly strict" from "rejects everything".
func TestPhraseValidationAcceptsWellFormedPhrases(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t, withComparator("good-cmp", &validationComparator{
		name: "good-cmp",
		phrases: []core.ContributedPhrase{
			wellFormed(`^the reading is (\w+)$`),
			wellFormed(`^the reading is:$`),
			wellFormed(`^the reading holds$`),
		},
	}))

	got, err := resolvePhrases(eng)
	if err != nil {
		t.Fatalf("validation rejected well-formed phrases: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("resolved %d phrases, want 3", len(got))
	}
	// Arity and docstring-ness are derived from the pattern, not declared.
	wantArity := []int{1, 0, 0}
	wantDoc := []bool{false, true, false}
	for i := range got {
		if got[i].arity != wantArity[i] {
			t.Errorf("phrase %d arity = %d, want %d", i, got[i].arity, wantArity[i])
		}
		if got[i].wantsDoc != wantDoc[i] {
			t.Errorf("phrase %d wantsDoc = %v, want %v", i, got[i].wantsDoc, wantDoc[i])
		}
	}
}

// TestAnchoredPatternEdgeCases pins the anchoring predicate directly, including the
// escaped-dollar case that a naive HasSuffix("$") check gets wrong.
func TestAnchoredPatternEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		want    bool
	}{
		{`^ok$`, true},
		{`^the price is (\d+)$`, true},
		{`ok$`, false},
		{`^ok`, false},
		{`^the price is 5\$`, false},   // escaped dollar: a literal $, not an anchor
		{`^the price is 5\\$`, true},   // escaped BACKSLASH then a real anchor
		{`^the price is 5\\\$`, false}, // escaped backslash then escaped dollar
		{`$`, false},                   // no start anchor
		{`^$`, true},                   // degenerate but genuinely anchored

		// Top-level alternation binds LOOSER than the anchors. Found by review after a
		// string-level check ("starts ^, ends $") had shipped and accepted the first
		// row below — which the runner, matching with an UNANCHORED
		// FindStringSubmatch, then matches inside "I check the beta reading". That is
		// exactly the swallowing V2 exists to prevent.
		{`^the alpha reading|the beta reading$`, false}, // parses as (^alpha)|(beta$)
		{`^a|b$`, false},
		{`^a$|^b$`, true},                  // every branch anchored: legitimate
		{`^(?:alpha|beta) reading$`, true}, // alternation INSIDE the anchors
		{`^(alpha|beta) reading$`, true},   // same, with a capture group
		{`^a$|b`, false},                   // one unanchored branch is enough
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()
			if got := isAnchored(tt.pattern); got != tt.want {
				t.Errorf("isAnchored(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

// --- T046: edge cases the spec lists ---

// TestSameComparatorUnderTwoNamesCollidesWithItself covers the case an author is most
// likely to create by accident: one comparator instance registered under two names.
//
// It is a genuine collision even though only one object is involved — the sentence
// would match two registered step definitions — and the error must name both
// REGISTERED NAMES, since that is what the author has to reconcile. Naming the
// comparator's own Name() twice would be useless.
func TestSameComparatorUnderTwoNamesCollidesWithItself(t *testing.T) {
	t.Parallel()

	shared := &validationComparator{name: "shared", phrases: []core.ContributedPhrase{wellFormed(`^the reading is fine$`)}}
	eng := customComparatorEngine(t,
		withComparator("first-name", shared),
		withComparator("second-name", shared),
	)

	_, err := resolvePhrases(eng)
	if err == nil {
		t.Fatal("one comparator registered under two names contributed the same pattern twice without complaint")
	}
	for _, want := range []string{"first-name", "second-name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the registered name %q; the author reconciles REGISTRATIONS, not object identity", err.Error(), want)
		}
	}
}

// mutatingContributor hands out a phrase slice and then mutates it, modelling a
// comparator that tries to add phrases after the engine is built.
type mutatingContributor struct {
	phrases []core.ContributedPhrase
}

func (c *mutatingContributor) Name() string { return "mutating" }
func (c *mutatingContributor) Compare(_ context.Context, _ core.Evidence, _ core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}
func (c *mutatingContributor) ContributedPhrases() []core.ContributedPhrase { return c.phrases }
func (c *mutatingContributor) ParseCaptures(caps []string) (core.Expectation, error) {
	return strings.Join(caps, ","), nil
}

// TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine pins that the resolved set
// is a snapshot.
//
// A comparator mutating its phrase list after the engine is built has NO effect, and
// that is the correct outcome rather than a limitation: the engine's seam registry is
// sealed, and a step set that could grow mid-run would mean the drift partition and the
// binding precheck were both computed against a set that no longer exists.
func TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine(t *testing.T) {
	t.Parallel()

	c := &mutatingContributor{phrases: []core.ContributedPhrase{wellFormed(`^the reading is (\w+)$`)}}
	eng := customComparatorEngine(t, withComparator("mutating", c))

	before, err := resolvePhrases(eng)
	if err != nil {
		t.Fatalf("resolvePhrases: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("resolved %d phrases, want 1", len(before))
	}

	// The comparator tries to add a phrase after the fact.
	c.phrases = append(c.phrases, wellFormed(`^the reading is definitely (\w+)$`))

	if len(before) != 1 {
		t.Errorf("the already-resolved set grew to %d; it must be a snapshot", len(before))
	}
	// Re-resolving DOES see it — resolution is a function of the engine's comparators
	// at call time. What must never happen is the previously-built binding changing
	// underneath a running suite, which the snapshot above proves.
	after, err := resolvePhrases(eng)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if len(after) != 2 {
		t.Errorf("re-resolution saw %d phrases, want 2; resolution must reflect the comparators as they are when called", len(after))
	}
}

// TestContributedPhraseNeverSelectedByTagsIsStillValidated pins that validation is
// unconditional. A phrase belonging to a scenario the tag expression filters out is
// still registered and still validated: deferring validation to first use would mean a
// malformed phrase lay dormant until someone happened to run the right tag, which is
// the least useful moment to discover it.
func TestContributedPhraseNeverSelectedByTagsIsStillValidated(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t, withComparator("unanchored", &validationComparator{
		name:    "unanchored",
		phrases: []core.ContributedPhrase{wellFormed(`the never selected reading$`)},
	}))

	if _, err := resolvePhrases(eng); err == nil {
		t.Fatal("a phrase no scenario would select was accepted; validation must not depend on whether a phrase is reached")
	}
}

// docstringOnly implements ExpectationParser but NOT CaptureParser, and PANICS if the
// capture route is taken. A stub that implemented both could not detect a routing
// mistake — it would quietly succeed either way.
type docstringOnly struct {
	gotText string
	exp     core.Expectation
	err     error
}

func (c *docstringOnly) Name() string { return "doc-only" }
func (c *docstringOnly) Compare(_ context.Context, _ core.Evidence, _ core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}
func (c *docstringOnly) ParseExpectation(text string) (core.Expectation, error) {
	c.gotText = text
	return c.exp, c.err
}

// TestContributedPhraseDocstringRouteReachesExpectationParser covers row 1 of the
// routing table — the one advertised everywhere as "ExpectationParser (unchanged from
// 011)" and, until review caught it, executed by no test in the repo.
//
// A mutation replacing the body with a constant left the entire suite green, which is
// the only evidence that matters about a test's absence.
//
// The body must arrive VERBATIM. Whitespace can be significant to a comparator's own
// format, so the untrimmed guarantee is asserted rather than assumed — the surrounding
// code claims it and nothing checked it.
func TestContributedPhraseDocstringRouteReachesExpectationParser(t *testing.T) {
	t.Parallel()

	const body = "  leading and trailing space matters  \n\n  second line  "

	c := &docstringOnly{exp: "parsed-by-expectation-parser"}
	cp := contributedPhrase{
		comparator: "doc-only",
		phrase:     core.ContributedPhrase{Pattern: `^the revenue matches:$`},
		arity:      0,
		wantsDoc:   true,
	}
	if cp.seam() != "docstring" {
		t.Fatalf("seam() = %q, want docstring — this test is asserting the wrong route", cp.seam())
	}

	exp, err := cp.parse(c, nil, body, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if exp != "parsed-by-expectation-parser" {
		t.Errorf("parse returned %v, want the ExpectationParser's own expectation", exp)
	}
	if c.gotText != body {
		t.Errorf("ExpectationParser received %q, want %q verbatim — the body is never trimmed or normalized because whitespace may be significant to the comparator's format",
			c.gotText, body)
	}
}

// TestContributedPhraseDocstringRoutePropagatesParseFailures pins that the docstring
// route honours the SAME error contract as the captures route: a parse error is wrapped
// naming the comparator, and a nil expectation with no error is refused rather than
// forwarded into a passing verdict.
func TestContributedPhraseDocstringRoutePropagatesParseFailures(t *testing.T) {
	t.Parallel()

	cp := contributedPhrase{
		comparator: "doc-only",
		phrase:     core.ContributedPhrase{Pattern: `^the revenue matches:$`},
		wantsDoc:   true,
	}

	t.Run("parse error is wrapped and names the comparator", func(t *testing.T) {
		t.Parallel()
		_, err := cp.parse(&docstringOnly{err: errStub{"bad body"}}, nil, "x", true)
		if err == nil {
			t.Fatal("parse succeeded despite the parser returning an error")
		}
		for _, want := range []string{"doc-only", "bad body"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err.Error(), want)
			}
		}
	})

	t.Run("nil expectation with no error is refused", func(t *testing.T) {
		t.Parallel()
		exp, err := cp.parse(&docstringOnly{exp: nil}, nil, "x", true)
		if err == nil {
			t.Fatal("a nil expectation with no error was accepted; it would produce a passing verdict for a step that asserted nothing")
		}
		if exp != nil {
			t.Errorf("parse returned an expectation (%v) alongside an error", exp)
		}
	})
}

// --- step-argument agreement (found by review, twice) ---

// TestStepArgumentKindNamesEveryArgument pins the reject-by-default property that makes
// this a fix to the MECHANISM rather than to two instances.
//
// The runner discards any argument the handler did not declare, silently. So an
// argument kind this code does not recognise must be reported as SOMETHING: returning
// "" for it would quietly re-open the hole for the next type godog adds, which is
// exactly how the data-table case survived the first fix.
func TestStepArgumentKindNamesEveryArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		arg  *messages.PickleStepArgument
		want string
	}{
		{name: "no argument", arg: nil, want: ""},
		{name: "docstring", arg: &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: "x"}}, want: "docstring"},
		{name: "data table", arg: &messages.PickleStepArgument{DataTable: &messages.PickleTable{}}, want: "data table"},
		{
			// A populated argument struct with neither known field set stands in for
			// an argument kind added by a future godog. It must NOT read as "none".
			name: "unrecognised kind is still reported",
			arg:  &messages.PickleStepArgument{},
			want: "step argument of an unrecognised kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stepArgumentKind(&messages.PickleStep{Text: "x", Argument: tt.arg})
			if got != tt.want {
				t.Errorf("stepArgumentKind = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStepArgumentsProblemMessages covers every branch of the disagreement matrix and
// asserts each message names the comparator, the pattern and the offending argument —
// the three things an author needs to locate the mistake.
func TestStepArgumentsProblemMessages(t *testing.T) {
	t.Parallel()

	captures := contributedPhrase{
		comparator: "cap-cmp",
		phrase:     core.ContributedPhrase{Pattern: `^the revenue matches (\w+)$`},
		re:         regexp.MustCompile(`^the revenue matches (\w+)$`),
		arity:      1,
	}
	docstring := contributedPhrase{
		comparator: "doc-cmp",
		phrase:     core.ContributedPhrase{Pattern: `^the revenue matches:$`},
		re:         regexp.MustCompile(`^the revenue matches:$`),
		wantsDoc:   true,
	}
	pa := newStepArguments([]contributedPhrase{captures, docstring})

	docArg := &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: "x"}}
	tableArg := &messages.PickleStepArgument{DataTable: &messages.PickleTable{}}

	tests := []struct {
		name     string
		text     string
		arg      *messages.PickleStepArgument
		wantOK   bool
		wantSubs []string
	}{
		{name: "captures phrase, no argument", text: "the revenue matches quarterly", wantOK: true},
		{name: "docstring phrase, docstring", text: "the revenue matches:", arg: docArg, wantOK: true},
		{
			name: "captures phrase, surplus docstring", text: "the revenue matches quarterly", arg: docArg,
			wantSubs: []string{"cap-cmp", strconv.Quote(`^the revenue matches (\w+)$`), "silently discarded"},
		},
		{
			name: "captures phrase, surplus data table", text: "the revenue matches quarterly", arg: tableArg,
			wantSubs: []string{"cap-cmp", "data table", "cannot receive one", "no contributed-phrase seam"},
		},
		{
			name: "docstring phrase, missing body", text: "the revenue matches:",
			wantSubs: []string{"doc-cmp", "carries none"},
		},
		{
			name: "docstring phrase, given a table instead", text: "the revenue matches:", arg: tableArg,
			wantSubs: []string{"doc-cmp", "data table", "expects a docstring body"},
		},
		{name: "step matching no contributed phrase is not our business", text: "something else entirely", arg: tableArg, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pa.stepProblem(&messages.PickleStep{Text: tt.text, Argument: tt.arg})
			if tt.wantOK {
				if got != "" {
					t.Fatalf("legitimate usage rejected: %s", got)
				}
				return
			}
			if got == "" {
				t.Fatal("a mismatched step argument was accepted; the runner would discard it silently and report a verdict that never read it")
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("message %q does not mention %q", got, want)
				}
			}
		})
	}
}

// TestStepArgumentsAcceptsWhatEachStepActuallyDeclares pins that the check is an
// agreement test, not a ban on arguments: a step carrying exactly what its definition
// declares must pass, and a sentence matching no definition at all is not this check's
// business (it is an unbound step, reported by StepBindingFindings).
//
// This REPLACED a test asserting that an engine with no contributed phrases compiles no
// built-in patterns and rejects nothing — "the common path must cost nothing". That
// property was deliberately given up: the surplus-argument defect needs no comparator to
// reach, so the engine that contributes nothing is the one that most needs the check.
// SC-009 is unaffected, being about byte-identical golden OUTPUT rather than work done.
func TestStepArgumentsAcceptsWhatEachStepActuallyDeclares(t *testing.T) {
	t.Parallel()

	pa := newStepArguments(nil)
	if len(pa.builtins) != len(stepDefs) {
		t.Errorf("derived %d built-in expectations for %d rows; an engine with no phrases must still check every built-in", len(pa.builtins), len(stepDefs))
	}

	ok := []*messages.PickleStep{
		// Declares a docstring, carries one.
		{Text: "the run satisfies:", Argument: &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: "true"}}},
		// Declares none, carries none.
		{Text: `the agent target "bot"`},
		// Matches no step definition at all: an unbound step, not an argument problem.
		{Text: "the moon is made of green cheese", Argument: &messages.PickleStepArgument{DataTable: &messages.PickleTable{}}},
	}
	if err := pa.check(ok); err != nil {
		t.Errorf("a step carrying exactly what it declares was rejected: %v", err)
	}
	st := &messages.PickleStep{Text: "anything", Argument: &messages.PickleStepArgument{DataTable: &messages.PickleTable{}}}
	if err := pa.check([]*messages.PickleStep{st}); err != nil {
		t.Errorf("a sentence matching no step definition was rejected: %v", err)
	}
	if got := pa.Findings([]*messages.PickleStep{st}, Source{}); len(got) != 0 {
		t.Errorf("want no findings, got %+v", got)
	}
}

// The static path's refusal to fall back to "no phrases" on a malformed engine — which
// would make Validate report clean on a suite whose engine cannot even be built — is
// pinned by TestEngineStepChecksPropagatesValidationFailure in suite_test.go. This file
// asserted the argument half through EnginePhraseArguments until convergence (T073)
// removed that accessor; that test already covered both halves, so the assertion was
// absorbed rather than lost.
