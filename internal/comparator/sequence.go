package comparator

import (
	"context"
	"fmt"
	"sort"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// serviceNameAttr is the OTel resource attribute identifying which service
// emitted a span. The trace store (Tempo and the fixture loader) merges resource
// attributes onto every span, so the comparator reads it like any span attr.
const serviceNameAttr = "service.name"

type SequenceExpectation struct {
	// Kind selects the identity strategy: "" or "tool" (default, gen_ai.tool.name)
	// or "service" (the service.name resource attribute).
	Kind      string
	Order     []string
	Forbidden []string
}

type sequence struct{}

func NewSequence() core.Comparator { return sequence{} }
func (sequence) Name() string      { return "sequence" }

func (sequence) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(SequenceExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("sequence: expectation must be SequenceExpectation, got %T", e)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("sequence: Evidence.Trace is nil")
	}

	var (
		actual []string
		noun   string
		err    error
	)
	switch exp.Kind {
	case "", "tool":
		noun = "tool"
		actual, err = toolSequence(ev.Trace)
	case "service":
		noun = "service"
		actual, err = serviceSequence(ev.Trace)
	default:
		return core.Verdict{}, fmt.Errorf("sequence: unknown Kind %q (want \"\", \"tool\", or \"service\")", exp.Kind)
	}
	if err != nil {
		return core.Verdict{}, err
	}

	v := core.Verdict{Pass: true}

	// forbidden
	forbidden := map[string]bool{}
	for _, f := range exp.Forbidden {
		forbidden[f] = true
	}
	for _, a := range actual {
		if forbidden[a] {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("forbidden %s %q was called", noun, a))
		}
	}

	// ordered subsequence: each Order item must appear, in order, in actual
	i := 0
	for _, want := range exp.Order {
		found := false
		for i < len(actual) {
			if actual[i] == want {
				found = true
				i++
				break
			}
			i++
		}
		if !found {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("expected %s %q not found in order; actual sequence = %v", noun, want, actual))
		}
	}
	return v, nil
}

// toolSequence returns the execute_tool names in start order (today's path).
func toolSequence(t *trace.Trace) ([]string, error) {
	var out []string
	for i, s := range t.ByOp(genai.OpExecuteTool) {
		name := s.Attr(genai.ToolName)
		if name == "" {
			return nil, fmt.Errorf("sequence: execute_tool span[%d] (%q) missing %s", i, s.Name, genai.ToolName)
		}
		out = append(out, name)
	}
	return out, nil
}

// serviceSequence returns the distinct services in first-seen order. Spans are
// stable-sorted by Start: live traces order by real start time; fixtures carry no
// timestamps, so the stable sort preserves the spans' array order, which the
// tracelab capture wrote in start-time order. A span missing service.name is a
// hard error, mirroring the missing-tool-name path.
func serviceSequence(t *trace.Trace) ([]string, error) {
	spans := make([]*trace.Span, len(t.Spans))
	copy(spans, t.Spans)
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })

	seen := map[string]bool{}
	var out []string
	for i, s := range spans {
		svc := s.Attr(serviceNameAttr)
		if svc == "" {
			return nil, fmt.Errorf("sequence: span[%d] (%q) missing %s", i, s.Name, serviceNameAttr)
		}
		if !seen[svc] {
			seen[svc] = true
			out = append(out, svc)
		}
	}
	return out, nil
}
