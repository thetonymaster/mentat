package steps

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/core"
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
	if got := out[0].Interface().(error); got != want {
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
	gotText string
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

// docOnly implements ExpectationParser but NOT CaptureParser.
type docOnly struct{ *phraseComparator }

func (c docOnly) ParseCaptures([]string) (core.Expectation, error) {
	panic("docOnly must not be routed to ParseCaptures")
}
func (c docOnly) ParseExpectation(text string) (core.Expectation, error) {
	c.gotText = text
	return c.exp, c.err
}

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
