package comparator

import (
	"context"
	"fmt"
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

// treeTrace: root → mid → leaf, plus a sibling "other" under root, in a SECOND root
// "root2 → orphan". Exercises direct-child, descendant, and cross-root (forest) cases.
func treeTrace() *trace.Trace {
	a := func(op string) map[string]string { return map[string]string{"gen_ai.operation.name": op} }
	root := &trace.Span{ID: "root", Name: "invoke_agent", Attrs: a("invoke_agent")}
	mid := &trace.Span{ID: "mid", ParentID: "root", Name: "chat", Attrs: a("chat")}
	leaf := &trace.Span{ID: "leaf", ParentID: "mid", Name: "execute_tool search",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"}}
	other := &trace.Span{ID: "other", ParentID: "root", Name: "execute_tool fetch",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "fetch"}}
	root2 := &trace.Span{ID: "root2", Name: "invoke_agent", Attrs: a("invoke_agent")}
	orphan := &trace.Span{ID: "orphan", ParentID: "root2", Name: "execute_tool pay",
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "pay"}}
	return &trace.Trace{
		Roots: []*trace.Span{root, root2},
		Spans: []*trace.Span{root, mid, leaf, other, root2, orphan},
	}
}

func TestShapeContainment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ShapeExpectation
		wantPass bool
	}{
		{"direct child holds", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=chat")}, true},
		{"direct child fails for grandchild", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=invoke_agent")}, false},
		{"descendant holds (grandchild)", ShapeExpectation{Kind: "containment", Relation: "descendant",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=invoke_agent")}, true},
		{"descendant fails when unrelated", ShapeExpectation{Kind: "containment", Relation: "descendant",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.tool.name=fetch")}, false},
		{"cross-root child fails (different roots)", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=pay"), Parent: sel(t, "gen_ai.operation.name=chat")}, false},
		{"no matching parent fails", ShapeExpectation{Kind: "containment", Relation: "child",
			Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.tool.name=nonexistent")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: treeTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestShapeContainmentValidation(t *testing.T) {
	t.Parallel()
	tests := []ShapeExpectation{
		{Kind: "containment", Subject: sel(t, "a=b"), Relation: "child"},                        // empty Parent
		{Kind: "containment", Subject: sel(t, "a=b"), Parent: sel(t, "c=d"), Relation: "uncle"}, // bad Relation
	}
	for i, exp := range tests {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			t.Parallel()
			if _, err := NewShape().Compare(context.Background(), core.Evidence{Trace: treeTrace()}, exp); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
