package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Count is a cardinality constraint. Op is ">=" or "==".
type Count struct {
	Op string
	N  int
}

// ok reports whether n satisfies the constraint. A nil Count means "at least 1".
func (c *Count) ok(n int) bool {
	if c == nil {
		return n >= 1
	}
	switch c.Op {
	case ">=":
		return n >= c.N
	case "==":
		return n == c.N
	default:
		return false // unreachable: Compare validates Op
	}
}

// describe renders the constraint for verdict reasons.
func (c *Count) describe() string {
	if c == nil {
		return "at least 1"
	}
	if c.Op == "==" {
		return fmt.Sprintf("exactly %d", c.N)
	}
	return fmt.Sprintf("at least %d", c.N)
}

// ShapeExpectation is one structural assertion. Each Gherkin step builds exactly one.
type ShapeExpectation struct {
	Kind     string   // "exists" | "absent" | "containment" | "fanout"
	Subject  Selector // the span being asserted about (the matched span / the child)
	Parent   Selector // containment & fanout: the container span; empty otherwise
	Relation string   // containment: "child" | "descendant"
	Count    *Count   // exists & fanout cardinality; nil ⇒ "at least 1"
}

type shape struct{}

// NewShape returns the structural ("shape") comparator. It reads Evidence.Trace only.
func NewShape() core.Comparator { return shape{} }
func (shape) Name() string      { return "shape" }

func (shape) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ShapeExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("shape: expectation must be ShapeExpectation, got %T", e)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("shape: Evidence.Trace is nil")
	}
	if len(exp.Subject) == 0 {
		return core.Verdict{}, fmt.Errorf("shape: Subject selector is empty")
	}
	if exp.Count != nil && exp.Count.Op != ">=" && exp.Count.Op != "==" {
		return core.Verdict{}, fmt.Errorf("shape: unknown count op %q (want \">=\" or \"==\")", exp.Count.Op)
	}
	if exp.Count != nil && exp.Count.N < 0 {
		return core.Verdict{}, fmt.Errorf("shape: count N must be >= 0, got %d", exp.Count.N)
	}
	switch exp.Kind {
	case "exists":
		return shapeExists(ev.Trace, exp), nil
	case "absent":
		return shapeAbsent(ev.Trace, exp), nil
	case "containment":
		if err := validateShapeTraceIDs(ev.Trace); err != nil {
			return core.Verdict{}, fmt.Errorf("shape: containment requires valid span IDs: %w", err)
		}
		if len(exp.Parent) == 0 {
			return core.Verdict{}, fmt.Errorf("shape: containment requires a Parent selector")
		}
		if exp.Relation != "child" && exp.Relation != "descendant" {
			return core.Verdict{}, fmt.Errorf("shape: containment Relation must be \"child\" or \"descendant\", got %q", exp.Relation)
		}
		return shapeContainment(ev.Trace, exp), nil
	case "fanout":
		if err := validateShapeTraceIDs(ev.Trace); err != nil {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires valid span IDs: %w", err)
		}
		if len(exp.Parent) == 0 {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires a Parent selector")
		}
		if exp.Count == nil {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires a Count")
		}
		if exp.Relation != "" && exp.Relation != "child" {
			return core.Verdict{}, fmt.Errorf("shape: fanout supports only direct children (v1); Relation %q not allowed", exp.Relation)
		}
		return shapeFanout(ev.Trace, exp), nil
	default:
		return core.Verdict{}, fmt.Errorf("shape: unknown Kind %q", exp.Kind)
	}
}

// matchingSpans returns every span in the forest satisfying sel.
func matchingSpans(tr *trace.Trace, sel Selector) []*trace.Span {
	var out []*trace.Span
	for _, s := range tr.Spans {
		if sel.matchSpan(s) {
			out = append(out, s)
		}
	}
	return out
}

func shapeExists(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	n := len(matchingSpans(tr, exp.Subject))
	if exp.Count.ok(n) {
		return core.Verdict{Pass: true}
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("expected %s spans matching %s, found %d", exp.Count.describe(), exp.Subject, n),
	}}
}

func shapeAbsent(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	n := len(matchingSpans(tr, exp.Subject))
	if n == 0 {
		return core.Verdict{Pass: true}
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("forbidden span matching %s was present (%d occurrence(s))", exp.Subject, n),
	}}
}

// validateShapeTraceIDs guards the ID-based structural checks (containment, fanout):
// an empty ID would let "" == "" false-match a root's empty ParentID, and a duplicate
// ID would make byIDIndex silently overwrite and corrupt ancestry walks. Per the
// no-silent-fallbacks rule, a degenerate trace is a hard error, not a guessed verdict.
func validateShapeTraceIDs(tr *trace.Trace) error {
	seen := make(map[string]struct{}, len(tr.Spans))
	for i, s := range tr.Spans {
		if s.ID == "" {
			return fmt.Errorf("span[%d] (%q) has empty ID", i, s.Name)
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("duplicate span ID %q", s.ID)
		}
		seen[s.ID] = struct{}{}
	}
	return nil
}

// byIDIndex maps span ID → span for ancestry walks. In a Tempo-sourced trace IDs are
// unique; structural assertions are only meaningful when IDs are populated.
func byIDIndex(tr *trace.Trace) map[string]*trace.Span {
	byID := make(map[string]*trace.Span, len(tr.Spans))
	for _, s := range tr.Spans {
		byID[s.ID] = s
	}
	return byID
}

// isAncestor reports whether the span with ancestorID lies on child's parent chain.
// The walk is bounded by the span count to stay safe on malformed (cyclic) traces.
func isAncestor(byID map[string]*trace.Span, ancestorID string, child *trace.Span) bool {
	cur := child
	for steps := 0; cur != nil && cur.ParentID != "" && steps < len(byID); steps++ {
		if cur.ParentID == ancestorID {
			return true
		}
		cur = byID[cur.ParentID]
	}
	return false
}

func shapeFanout(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	parents := matchingSpans(tr, exp.Parent)
	best := 0
	for _, p := range parents {
		cnt := 0
		for _, s := range tr.Spans {
			if s.ParentID == p.ID && exp.Subject.matchSpan(s) {
				cnt++
			}
		}
		if exp.Count.ok(cnt) {
			return core.Verdict{Pass: true}
		}
		if cnt > best {
			best = cnt
		}
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("expected a span matching %s with %s children matching %s; best matching parent had %d",
			exp.Parent, exp.Count.describe(), exp.Subject, best),
	}}
}

func shapeContainment(tr *trace.Trace, exp ShapeExpectation) core.Verdict {
	byID := byIDIndex(tr)
	children := matchingSpans(tr, exp.Subject)
	parents := matchingSpans(tr, exp.Parent)
	for _, c := range children {
		for _, p := range parents {
			if exp.Relation == "child" && c.ParentID == p.ID {
				return core.Verdict{Pass: true}
			}
			if exp.Relation == "descendant" && isAncestor(byID, p.ID, c) {
				return core.Verdict{Pass: true}
			}
		}
	}
	rel := "a child"
	if exp.Relation == "descendant" {
		rel = "a descendant"
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("no span matching %s is %s of a span matching %s", exp.Subject, rel, exp.Parent),
	}}
}
