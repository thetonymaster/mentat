package comparator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/trace"
)

// newResultCmp returns the "result" comparator backed by a fresh per-test registry
// with the built-in matchers registered — the composition-root wiring in miniature,
// since result.Compare now resolves matchers from the registry it was built with.
func newResultCmp() core.Comparator {
	reg := registry.New()
	RegisterBuiltinMatchers(reg)
	return NewResult(reg)
}

// recordingMatcher proves result.Compare dispatches to a registered matcher
// rather than a hard-coded switch.
type recordingMatcher struct{ called *bool }

func (recordingMatcher) Name() string { return "recording" }
func (r recordingMatcher) Match(_ context.Context, _ core.Evidence, _, _ string) (core.Verdict, error) {
	*r.called = true
	return core.Verdict{Pass: true}, nil
}

// bulkTrace builds n "bulk" tool spans whose result attribute is val(i),
// start-ordered — the many-matched-spans shape for quantified expectations.
func bulkTrace(n int, val func(i int) string) *trace.Trace {
	t0 := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	tr := &trace.Trace{}
	for i := range n {
		s := toolSpan(fmt.Sprintf("b%d", i), "bulk", val(i), t0.Add(time.Duration(i)*time.Millisecond))
		tr.Roots = append(tr.Roots, s)
		tr.Spans = append(tr.Spans, s)
	}
	return tr
}

// TestMatcherCompileOncePerExpectation proves audit C6 is dead (FR-005): a
// quantified expectation over 500 matched spans compiles its pattern/schema
// exactly once. Serial by design: it swaps the package compile seams, which
// are shared mutable state (t.Parallel would race the counting wrappers
// against every other regex/schema test in the package).
func TestMatcherCompileOncePerExpectation(t *testing.T) {
	const spanCount = 500
	tests := []struct {
		name string
		exp  ResultExpectation
		val  func(i int) string
		swap func(t *testing.T, count *int)
	}{
		{
			name: "regex compiles once across 500 matched spans",
			exp:  ResultExpectation{Matcher: "regex", Want: `val-\d+`, Source: toolSource("bulk", QuantEvery, 0)},
			val:  func(i int) string { return fmt.Sprintf("val-%d", i) },
			swap: func(t *testing.T, count *int) {
				orig := compileRegexp
				compileRegexp = func(pat string) (*regexp.Regexp, error) { *count++; return orig(pat) }
				t.Cleanup(func() { compileRegexp = orig })
			},
		},
		{
			name: "schema compiles once across 500 matched spans",
			exp:  ResultExpectation{Matcher: "schema", Want: `{"type":"object","required":["ok"]}`, Source: toolSource("bulk", QuantEvery, 0)},
			val:  func(int) string { return `{"ok":true}` },
			swap: func(t *testing.T, count *int) {
				orig := compileSchemaDoc
				compileSchemaDoc = func(want string) (*jsonschema.Schema, error) { *count++; return orig(want) }
				t.Cleanup(func() { compileSchemaDoc = orig })
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := 0
			tt.swap(t, &count)
			ev := core.Evidence{Trace: bulkTrace(spanCount, tt.val)}
			v, err := newResultCmp().Compare(context.Background(), ev, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !v.Pass {
				t.Fatalf("want passing verdict over %d spans, got %+v", spanCount, v)
			}
			if count != 1 {
				t.Fatalf("compilations = %d over %d matched spans, want exactly 1 per expectation", count, spanCount)
			}
		})
	}
}

// TestMatcherCompileErrorAtConstruction proves authoring errors (invalid
// pattern/schema) surface when the expectation is bound for evaluation —
// before any span or target is read — mirroring the CEL precompile lifecycle
// (research R5). The span-source rows use spans that LACK the extracted
// attribute: if evaluation reached a span first, the error would be the
// extraction error, not the compile error.
func TestMatcherCompileErrorAtConstruction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ev         core.Evidence
		exp        ResultExpectation
		errSub     string // substring the construction-time error must carry
		errExclude string // evaluation-time error text that must NOT appear
	}{
		{
			name:       "invalid regex over span source errors before span extraction",
			ev:         core.Evidence{Trace: noResultAttrTrace()},
			exp:        ResultExpectation{Matcher: "regex", Want: "(((", Source: toolSource("search", QuantEvery, 0)},
			errSub:     `bad regex "((("`,
			errExclude: "no attribute",
		},
		{
			name:       "invalid schema over span source errors before span extraction",
			ev:         core.Evidence{Trace: noResultAttrTrace()},
			exp:        ResultExpectation{Matcher: "schema", Want: `{"type":123}`, Source: toolSource("search", QuantEvery, 0)},
			errSub:     "invalid JSON Schema",
			errExclude: "mem:///",
		},
		{
			name:       "invalid regex at boundary errors before target resolution",
			ev:         core.Evidence{Output: core.Output{Answer: "x"}},
			exp:        ResultExpectation{Matcher: "regex", Want: "(((", Target: "body"},
			errSub:     `bad regex "((("`,
			errExclude: "unsupported Target",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newResultCmp().Compare(context.Background(), tt.ev, tt.exp)
			if err == nil {
				t.Fatal("want a construction-time compile error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error %q missing construction-time text %q", err.Error(), tt.errSub)
			}
			if strings.Contains(err.Error(), tt.errExclude) {
				t.Errorf("error %q carries evaluation-time text %q — compile did not precede evaluation", err.Error(), tt.errExclude)
			}
		})
	}
}

// TestCompiledMatcherGoldenVerdicts locks the regex/schema pass/fail outcomes
// across the compile-once refactor (FR-005, SC-004: zero verdict changes),
// through both the boundary and span-source paths.
func TestCompiledMatcherGoldenVerdicts(t *testing.T) {
	t.Parallel()
	const objSchema = `{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "regex boundary match passes",
			ev:       core.Evidence{Output: core.Output{Answer: "order-123"}},
			exp:      ResultExpectation{Matcher: "regex", Want: `order-\d+`},
			wantPass: true,
		},
		{
			name:     "regex boundary miss fails",
			ev:       core.Evidence{Output: core.Output{Answer: "no digits"}},
			exp:      ResultExpectation{Matcher: "regex", Want: `order-\d+`},
			wantPass: false,
		},
		{
			name:     "regex every span passes",
			ev:       core.Evidence{Trace: bulkTrace(3, func(i int) string { return fmt.Sprintf("val-%d", i) })},
			exp:      ResultExpectation{Matcher: "regex", Want: `val-\d+`, Source: toolSource("bulk", QuantEvery, 0)},
			wantPass: true,
		},
		{
			name: "regex every span fails on one miss",
			ev: core.Evidence{Trace: bulkTrace(3, func(i int) string {
				if i == 1 {
					return "nope"
				}
				return fmt.Sprintf("val-%d", i)
			})},
			exp:      ResultExpectation{Matcher: "regex", Want: `val-\d+`, Source: toolSource("bulk", QuantEvery, 0)},
			wantPass: false,
		},
		{
			name:     "schema boundary valid body passes",
			ev:       core.Evidence{Output: core.Output{Body: []byte(`{"ok":true}`)}},
			exp:      ResultExpectation{Matcher: "schema", Want: objSchema},
			wantPass: true,
		},
		{
			name:     "schema boundary wrong type fails",
			ev:       core.Evidence{Output: core.Output{Body: []byte(`{"ok":"nope"}`)}},
			exp:      ResultExpectation{Matcher: "schema", Want: objSchema},
			wantPass: false,
		},
		{
			name:     "schema every span passes",
			ev:       core.Evidence{Trace: bulkTrace(3, func(int) string { return `{"ok":true}` })},
			exp:      ResultExpectation{Matcher: "schema", Want: objSchema, Source: toolSource("bulk", QuantEvery, 0)},
			wantPass: true,
		},
		{
			name:     "schema every span fails on one invalid instance",
			ev:       core.Evidence{Trace: bulkTrace(3, func(i int) string { return fmt.Sprintf(`{"ok":%v}`, i != 1) })},
			exp:      ResultExpectation{Matcher: "schema", Want: `{"type":"object","properties":{"ok":{"const":true}}}`, Source: toolSource("bulk", QuantEvery, 0)},
			wantPass: false,
		},
		{
			name:    "schema non-JSON span value is a hard error",
			ev:      core.Evidence{Trace: bulkTrace(3, func(int) string { return "not json" })},
			exp:     ResultExpectation{Matcher: "schema", Want: objSchema, Source: toolSource("bulk", QuantEvery, 0)},
			wantErr: true,
		},
		{
			name:    "regex valid pattern with unsupported target is error",
			ev:      core.Evidence{Output: core.Output{Answer: "order-1"}},
			exp:     ResultExpectation{Matcher: "regex", Want: `order-\d+`, Target: "body"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := newResultCmp().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
			if !got.Pass && len(got.Reasons) == 0 {
				t.Error("failing verdict must carry at least one Reason")
			}
		})
	}
}

// TestMatcherParallelReuseSameExpectation guards the spec edge case: parallel
// scenarios evaluating the SAME expectation objects must be race-free (run
// under -race). Sixteen goroutines share one regex and one schema expectation
// over one many-span trace.
func TestMatcherParallelReuseSameExpectation(t *testing.T) {
	t.Parallel()
	ev := core.Evidence{Trace: bulkTrace(100, func(int) string { return `{"ok":true}` })}
	exps := []ResultExpectation{
		{Matcher: "regex", Want: `"ok":true`, Source: toolSource("bulk", QuantEvery, 0)},
		{Matcher: "schema", Want: `{"type":"object","required":["ok"]}`, Source: toolSource("bulk", QuantEvery, 0)},
	}
	var wg sync.WaitGroup
	for range 16 {
		for i := range exps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, err := newResultCmp().Compare(context.Background(), ev, exps[i])
				if err != nil {
					t.Errorf("matcher %q: unexpected error: %v", exps[i].Matcher, err)
					return
				}
				if !v.Pass {
					t.Errorf("matcher %q: want pass, got %+v", exps[i].Matcher, v)
				}
			}()
		}
	}
	wg.Wait()
}

// TestMatcherPrototypeDirectUse pins the core.Matcher contract for a
// registered prototype called directly, without Compile: it compiles per call,
// erroring loudly on a bad pattern/schema, so direct users of the registry
// seam behave exactly as before the compile-once hoist. (The result comparator
// itself always compiles first — see TestMatcherCompileOncePerExpectation.)
func TestMatcherPrototypeDirectUse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		m        core.Matcher
		ev       core.Evidence
		want     string
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "regex prototype match passes",
			m:        regexMatcher{},
			ev:       core.Evidence{Output: core.Output{Answer: "order-9"}},
			want:     `order-\d+`,
			wantPass: true,
		},
		{
			name:     "regex prototype miss fails",
			m:        regexMatcher{},
			ev:       core.Evidence{Output: core.Output{Answer: "no digits"}},
			want:     `order-\d+`,
			wantPass: false,
		},
		{
			name:    "regex prototype bad pattern is error",
			m:       regexMatcher{},
			ev:      core.Evidence{Output: core.Output{Answer: "x"}},
			want:    "(((",
			wantErr: true,
		},
		{
			name:     "schema prototype valid body passes",
			m:        schemaMatcher{},
			ev:       core.Evidence{Output: core.Output{Body: []byte(`{"ok":true}`)}},
			want:     `{"type":"object","required":["ok"]}`,
			wantPass: true,
		},
		{
			name:     "schema prototype missing field fails",
			m:        schemaMatcher{},
			ev:       core.Evidence{Output: core.Output{Body: []byte(`{}`)}},
			want:     `{"type":"object","required":["ok"]}`,
			wantPass: false,
		},
		{
			name:    "schema prototype invalid schema is error",
			m:       schemaMatcher{},
			ev:      core.Evidence{Output: core.Output{Body: []byte(`{}`)}},
			want:    `{"type":123}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.m.Match(context.Background(), tt.ev, tt.want, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
			if !got.Pass && len(got.Reasons) == 0 {
				t.Error("failing verdict must carry at least one Reason")
			}
		})
	}
}

func TestResultDispatchesToRegisteredMatcher(t *testing.T) {
	called := false
	reg := registry.New()
	RegisterBuiltinMatchers(reg)
	reg.RegisterMatcher("recording", recordingMatcher{called: &called})

	v, err := NewResult(reg).Compare(context.Background(), core.Evidence{}, ResultExpectation{Matcher: "recording"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("result.Compare did not dispatch to the registered matcher")
	}
	if !v.Pass {
		t.Fatalf("want Pass=true from recording matcher, got %+v", v)
	}
}
