package comparator

import (
	"context"
	"strings"
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

// emptyIDTrace has a span with an empty ID — invalid for structural (ID-based)
// checks, where "" == "" would false-match a root's empty ParentID.
func emptyIDTrace() *trace.Trace {
	a := &trace.Span{ID: "", Name: "x", Attrs: map[string]string{"k": "v"}}
	b := &trace.Span{ID: "b", Name: "y", Attrs: map[string]string{"k": "v"}}
	return &trace.Trace{Roots: []*trace.Span{b}, Spans: []*trace.Span{a, b}}
}

// dupIDTrace has two spans sharing an ID — byIDIndex would silently overwrite and
// ancestry walks would be corrupted.
func dupIDTrace() *trace.Trace {
	a := &trace.Span{ID: "dup", Name: "x", Attrs: map[string]string{"k": "v"}}
	b := &trace.Span{ID: "dup", Name: "y", Attrs: map[string]string{"k": "v"}}
	return &trace.Trace{Roots: []*trace.Span{a, b}, Spans: []*trace.Span{a, b}}
}

func TestShapeCompareErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ev      core.Evidence
		exp     core.Expectation
		wantErr string
	}{
		{"wrong expectation type", core.Evidence{Trace: flatTrace()}, SequenceExpectation{}, "expectation must be ShapeExpectation"},
		{"nil trace", core.Evidence{}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b")}, "Evidence.Trace is nil"},
		{"empty subject", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists"}, "Subject selector is empty"},
		{"unknown kind", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "bogus", Subject: sel(t, "a=b")}, "unknown Kind"},
		{"unknown count op", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b"), Count: &Count{Op: "<", N: 1}}, "unknown count op"},
		{"negative count N", core.Evidence{Trace: flatTrace()}, ShapeExpectation{Kind: "exists", Subject: sel(t, "a=b"), Count: &Count{Op: ">=", N: -1}}, "count N must be >= 0"},
		{"containment empty span ID errors", core.Evidence{Trace: emptyIDTrace()}, ShapeExpectation{Kind: "containment", Relation: "child", Subject: sel(t, "k=v"), Parent: sel(t, "k=v")}, "empty ID"},
		{"containment duplicate span ID errors", core.Evidence{Trace: dupIDTrace()}, ShapeExpectation{Kind: "containment", Relation: "child", Subject: sel(t, "k=v"), Parent: sel(t, "k=v")}, "duplicate span ID"},
		{"fanout empty span ID errors", core.Evidence{Trace: emptyIDTrace()}, ShapeExpectation{Kind: "fanout", Subject: sel(t, "k=v"), Parent: sel(t, "k=v"), Count: &Count{Op: ">=", N: 1}}, "empty ID"},
		{"fanout duplicate span ID errors", core.Evidence{Trace: dupIDTrace()}, ShapeExpectation{Kind: "fanout", Subject: sel(t, "k=v"), Parent: sel(t, "k=v"), Count: &Count{Op: ">=", N: 1}}, "duplicate span ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewShape().Compare(context.Background(), tt.ev, tt.exp)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
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
	tests := []struct {
		name    string
		exp     ShapeExpectation
		wantErr string
	}{
		{
			name:    "empty Parent",
			exp:     ShapeExpectation{Kind: "containment", Subject: sel(t, "a=b"), Relation: "child"},
			wantErr: "containment requires a Parent selector",
		},
		{
			name:    "bad Relation",
			exp:     ShapeExpectation{Kind: "containment", Subject: sel(t, "a=b"), Parent: sel(t, "c=d"), Relation: "uncle"},
			wantErr: "containment Relation must be",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewShape().Compare(context.Background(), core.Evidence{Trace: treeTrace()}, tt.exp)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// fanoutTrace: chatA has 3 search children; chatB has 1 search child. Used to prove
// the existential-over-parent reading (any one matching parent satisfying Count passes).
func fanoutTrace() *trace.Trace {
	tool := func(id, parent, name string) *trace.Span {
		return &trace.Span{ID: id, ParentID: parent, Name: "execute_tool " + name,
			Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name}}
	}
	chat := func(id string) *trace.Span {
		return &trace.Span{ID: id, Name: "chat", Attrs: map[string]string{"gen_ai.operation.name": "chat"}}
	}
	chatA, chatB := chat("chatA"), chat("chatB")
	return &trace.Trace{
		Roots: []*trace.Span{chatA, chatB},
		Spans: []*trace.Span{
			chatA, chatB,
			tool("a1", "chatA", "search"), tool("a2", "chatA", "search"), tool("a3", "chatA", "search"),
			tool("b1", "chatB", "search"),
		},
	}
}

func TestShapeFanout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		exp      ShapeExpectation
		wantPass bool
	}{
		{"at least 3 passes (chatA)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 3}}, true},
		{"at least 4 fails (max is 3)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 4}}, false},
		{"exactly 3 passes (chatA)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 3}}, true},
		{"exactly 2 fails (no parent has exactly 2)", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=chat"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{"==", 2}}, false},
		{"no matching parent fails", ShapeExpectation{Kind: "fanout", Relation: "child",
			Parent: sel(t, "gen_ai.operation.name=nope"), Subject: sel(t, "gen_ai.tool.name=search"), Count: &Count{">=", 1}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: fanoutTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

func TestShapeName(t *testing.T) {
	t.Parallel()
	if got := NewShape().Name(); got != "shape" {
		t.Errorf("Name() = %q, want %q", got, "shape")
	}
}

func TestShapeFanoutValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		exp     ShapeExpectation
		wantErr string
	}{
		{
			name:    "empty Parent",
			exp:     ShapeExpectation{Kind: "fanout", Subject: sel(t, "a=b"), Count: &Count{">=", 1}},
			wantErr: "fanout requires a Parent selector",
		},
		{
			name:    "nil Count",
			exp:     ShapeExpectation{Kind: "fanout", Subject: sel(t, "a=b"), Parent: sel(t, "c=d")},
			wantErr: "fanout requires a Count",
		},
		{
			name:    "non-child relation",
			exp:     ShapeExpectation{Kind: "fanout", Subject: sel(t, "a=b"), Parent: sel(t, "c=d"), Relation: "descendant", Count: &Count{Op: ">=", N: 1}},
			wantErr: "fanout supports only direct children",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewShape().Compare(context.Background(), core.Evidence{Trace: fanoutTrace()}, tt.exp)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}
