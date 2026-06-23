package comparator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// toolSpan builds an execute_tool span carrying a tool name + result attribute.
func toolSpan(id, tool, result string, start time.Time) *trace.Span {
	return &trace.Span{
		ID:    id,
		Name:  "execute_tool " + tool,
		Start: start,
		Attrs: map[string]string{
			genai.Op:         genai.OpExecuteTool,
			genai.ToolName:   tool,
			genai.ToolResult: result,
		},
	}
}

// resultTrace: two "search" calls (distinct results + start times) and one
// "summarize" — enough for one-match, ambiguity, ordinals, and quantifiers.
func resultTrace() *trace.Trace {
	t0 := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	s1 := toolSpan("s1", "search", "first-result", t0)
	s2 := toolSpan("s2", "search", "second-result", t0.Add(time.Second))
	s3 := toolSpan("s3", "summarize", "the summary", t0.Add(2*time.Second))
	return &trace.Trace{Roots: []*trace.Span{s1, s2, s3}, Spans: []*trace.Span{s1, s2, s3}}
}

// toolSource builds a tool-convenience SpanSource for tests.
func toolSource(tool string, q Quant, idx int) *SpanSource {
	return &SpanSource{
		Selector: Selector{{Key: genai.ToolName, Value: tool}},
		Attr:     genai.ToolResult,
		Quant:    q,
		Index:    idx,
	}
}

func TestResultSpanSourceOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "one match, contains passes",
			ev:       core.Evidence{Trace: resultTrace()},
			exp:      ResultExpectation{Matcher: "contains", Want: "summary", Source: toolSource("summarize", QuantOne, 0)},
			wantPass: true,
		},
		{
			name:     "one match, contains fails",
			ev:       core.Evidence{Trace: resultTrace()},
			exp:      ResultExpectation{Matcher: "contains", Want: "nope", Source: toolSource("summarize", QuantOne, 0)},
			wantPass: false,
		},
		{
			name:     "one match, json-subset on attr body",
			ev:       core.Evidence{Trace: jsonResultTrace(`{"ok":true,"n":3}`)},
			exp:      ResultExpectation{Matcher: "json-subset", Want: `{"ok":true}`, Source: toolSource("lookup", QuantOne, 0)},
			wantPass: true,
		},
		{
			name:    "zero matches is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("delete", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "ambiguous bare (2 matches) is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("search", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "missing result attribute is error",
			ev:      core.Evidence{Trace: noResultAttrTrace()},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("search", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "nil trace is error",
			ev:      core.Evidence{},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("search", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "status matcher with span source is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "status", Want: "200", Source: toolSource("summarize", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:    "unknown matcher with span source is error",
			ev:      core.Evidence{Trace: resultTrace()},
			exp:     ResultExpectation{Matcher: "telepathy", Want: "x", Source: toolSource("summarize", QuantOne, 0)},
			wantErr: true,
		},
		{
			name:     "Source nil unchanged (boundary path still reads Answer)",
			ev:       core.Evidence{Output: core.Output{Answer: "boundary"}},
			exp:      ResultExpectation{Matcher: "contains", Want: "boundary"},
			wantPass: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewResult().Compare(context.Background(), tt.ev, tt.exp)
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

// jsonResultTrace: one "lookup" tool span whose result is the given JSON string.
func jsonResultTrace(jsonResult string) *trace.Trace {
	s := toolSpan("j1", "lookup", jsonResult, time.Time{})
	return &trace.Trace{Roots: []*trace.Span{s}, Spans: []*trace.Span{s}}
}

// noResultAttrTrace: one "search" span with NO gen_ai.tool.call.result attribute.
func noResultAttrTrace() *trace.Trace {
	s := &trace.Span{ID: "n1", Name: "execute_tool search", Attrs: map[string]string{
		genai.Op: genai.OpExecuteTool, genai.ToolName: "search",
	}}
	return &trace.Trace{Roots: []*trace.Span{s}, Spans: []*trace.Span{s}}
}

func TestResultSpanSourceQuantifiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "every: all search results contain 'result' passes",
			exp:      ResultExpectation{Matcher: "contains", Want: "result", Source: toolSource("search", QuantEvery, 0)},
			wantPass: true,
		},
		{
			name:     "every: one search result lacks 'first' fails",
			exp:      ResultExpectation{Matcher: "contains", Want: "first", Source: toolSource("search", QuantEvery, 0)},
			wantPass: false,
		},
		{
			name:     "any: at least one search result contains 'second' passes",
			exp:      ResultExpectation{Matcher: "contains", Want: "second", Source: toolSource("search", QuantAny, 0)},
			wantPass: true,
		},
		{
			name:     "any: no search result contains 'zzz' fails",
			exp:      ResultExpectation{Matcher: "contains", Want: "zzz", Source: toolSource("search", QuantAny, 0)},
			wantPass: false,
		},
		{
			name:    "every: zero matches is error (tool never called)",
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("delete", QuantEvery, 0)},
			wantErr: true,
		},
		{
			name:    "any: zero matches is error (tool never called)",
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Source: toolSource("delete", QuantAny, 0)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewResult().Compare(context.Background(), core.Evidence{Trace: resultTrace()}, tt.exp)
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
				t.Error("failing verdict must carry at least one Reason naming the span")
			}
		})
	}
}

func TestResultSpanSourceOrdinals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "first call picks earliest start (first-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "first-result", Source: toolSource("search", QuantFirst, 0)},
			wantPass: true,
		},
		{
			name:     "last call picks latest start (second-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "second-result", Source: toolSource("search", QuantLast, 0)},
			wantPass: true,
		},
		{
			name:     "2nd call picks index 2 (second-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "second-result", Source: toolSource("search", QuantNth, 2)},
			wantPass: true,
		},
		{
			name:     "1st call picks index 1 (first-result)",
			exp:      ResultExpectation{Matcher: "exact", Want: "first-result", Source: toolSource("search", QuantNth, 1)},
			wantPass: true,
		},
		{
			name:     "first call mismatch fails (not an error)",
			exp:      ResultExpectation{Matcher: "exact", Want: "second-result", Source: toolSource("search", QuantFirst, 0)},
			wantPass: false,
		},
		{
			name:    "3rd call out of range is error (only 2 matched)",
			exp:     ResultExpectation{Matcher: "exact", Want: "x", Source: toolSource("search", QuantNth, 3)},
			wantErr: true,
		},
		{
			name:    "0th call out of range is error",
			exp:     ResultExpectation{Matcher: "exact", Want: "x", Source: toolSource("search", QuantNth, 0)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewResult().Compare(context.Background(), core.Evidence{Trace: resultTrace()}, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
		})
	}
}

// TestResultSpanSourceMatcherErrorWrapped proves that when the dispatched matcher
// itself errors (here: an invalid regex pattern), resolveSpanSource wraps it with
// span/attr context — the matcher seam's own error has no idea it was run against a
// span attribute, so the comparator must name the span, attribute, and matcher.
func TestResultSpanSourceMatcherErrorWrapped(t *testing.T) {
	t.Parallel()
	_, err := NewResult().Compare(context.Background(), core.Evidence{Trace: resultTrace()},
		ResultExpectation{Matcher: "regex", Want: "(((", Source: toolSource("summarize", QuantOne, 0)})
	if err == nil {
		t.Fatal("expected an error from the invalid regex pattern via a span source")
	}
	for _, want := range []string{"matcher", "regex", "summarize", genai.ToolResult} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q (matcher error must carry span/attr context)", err.Error(), want)
		}
	}
}
