package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// flatTrace: three execute_tool spans (2 search, 1 summarize) with no parentage —
// sufficient for existence/count assertions, which ignore structure.
func flatTrace() *trace.Trace {
	mk := func(id, tool string) *trace.Span {
		return &trace.Span{ID: id, Name: "execute_tool " + tool,
			Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": tool}}
	}
	s1, s2, s3 := mk("s1", "search"), mk("s2", "search"), mk("s3", "summarize")
	return &trace.Trace{Roots: []*trace.Span{s1, s2, s3}, Spans: []*trace.Span{s1, s2, s3}}
}

func sel(t *testing.T, s string) Selector {
	t.Helper()
	parsed, err := ParseSelector(s)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", s, err)
	}
	return parsed
}

func TestShapeExistence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ShapeExpectation
		wantPass bool
	}{
		{"exists default >=1 passes", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search")}, true},
		{"exists default fails when absent", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=delete")}, false},
		{"at least 2 passes (two search)", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 2}}, true},
		{"at least 3 fails", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 3}}, false},
		{"exactly 2 passes", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 2}}, true},
		{"exactly 1 fails", ShapeExpectation{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 1}}, false},
		{"absent passes when none", ShapeExpectation{Kind: "absent", Subject: sel(t, "gen_ai.tool.name=delete")}, true},
		{"absent fails when present", ShapeExpectation{Kind: "absent", Subject: sel(t, "gen_ai.tool.name=search")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: flatTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestShapeCompareErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ev   core.Evidence
		exp  core.Expectation
	}{
		{"wrong expectation type", core.Evidence{Trace: flatTrace()}, SequenceExpectation{}},
		{"nil trace", core.Evidence{}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b")}},
		{"empty subject", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists"}},
		{"unknown kind", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "bogus", Subject: sel(t, "a=b")}},
		{"unknown count op", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b"), Count: &Count{Op: "<", N: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewShape().Compare(context.Background(), tt.ev, tt.exp); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
