package comparator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Quant resolves which of N spans matching a SpanSource selector supplies the value.
type Quant int

const (
	QuantOne   Quant = iota // bare: exactly one match (else hard error)
	QuantFirst              // first by start order
	QuantLast               // last by start order
	QuantNth                // Index-th by start order (1-based)
	QuantEvery              // all matches must satisfy (AND)
	QuantAny                // >=1 match satisfies (OR)
)

// SpanSource selects a span-attribute result value for the result comparator.
// The tool convenience form sets Selector = {gen_ai.tool.name = X} and
// Attr = genai.ToolResult; the general form sets a parsed selector + named attr.
type SpanSource struct {
	Selector Selector
	Attr     string
	Quant    Quant
	Index    int // QuantNth only, 1-based
}

// resolveSpanSource evaluates a result expectation against a span-attribute source.
// It selects spans, extracts the attribute, synthesizes a derived Evidence whose
// Output carries the value, and dispatches to the unchanged matcher; quantifiers
// combine per-span verdicts. Every author/trace defect is a hard error (invariant #4).
func resolveSpanSource(ctx context.Context, reg *registry.Registry, ev core.Evidence, exp ResultExpectation) (core.Verdict, error) {
	src := exp.Source
	if exp.Matcher == "status" {
		return core.Verdict{}, fmt.Errorf("result: status matcher is boundary-only; not valid with a span source")
	}
	m, ok := reg.Matcher(exp.Matcher)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	// Compile Want once per expectation, before any span is selected or
	// evaluated (FR-005, audit C6): all quantified spans reuse the compiled
	// matcher, and authoring errors surface at construction, never mid-span.
	m, err := compileMatcher(m, exp.Want)
	if err != nil {
		return core.Verdict{}, err
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("result: Evidence.Trace is nil (span source %s)", src.Selector)
	}
	spans := matchingSpans(ev.Trace, src.Selector)
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	if len(spans) == 0 {
		return core.Verdict{}, fmt.Errorf("result: selector %s matched no spans", src.Selector)
	}
	targets, err := src.selectSpans(spans)
	if err != nil {
		return core.Verdict{}, err
	}
	return src.evaluate(ctx, m, ev, exp.Want, targets)
}

// selectSpans applies the Quant to the start-ordered, non-empty match list.
func (s SpanSource) selectSpans(spans []*trace.Span) ([]*trace.Span, error) {
	switch s.Quant {
	case QuantOne:
		if len(spans) != 1 {
			return nil, fmt.Errorf("result: selector %s matched %d spans; use first/last/Nth, or every/any", s.Selector, len(spans))
		}
		return spans, nil
	case QuantFirst:
		return spans[:1], nil
	case QuantLast:
		return spans[len(spans)-1:], nil
	case QuantNth:
		if s.Index < 1 || s.Index > len(spans) {
			return nil, fmt.Errorf("result: span #%d of selector %s out of range (%d matched)", s.Index, s.Selector, len(spans))
		}
		return spans[s.Index-1 : s.Index], nil
	case QuantEvery, QuantAny:
		return spans, nil
	default:
		return nil, fmt.Errorf("result: unknown quant %d", s.Quant)
	}
}

// extract reads the source attribute from sp. Reserved span.* keys read intrinsics
// (always present); any other key is an attribute lookup whose ABSENCE is a hard
// error (extraction semantics, unlike the selector's filter semantics).
func (s SpanSource) extract(sp *trace.Span) (string, error) {
	if reservedKey(s.Attr) {
		return spanValue(sp, s.Attr), nil
	}
	val, ok := sp.Attrs[s.Attr]
	if !ok {
		return "", fmt.Errorf("result: span %q (selector %s) has no attribute %q", sp.Name, s.Selector, s.Attr)
	}
	return val, nil
}

// evaluate matches each target span's attribute (via a synthesized Evidence) and
// combines per the Quant: One/First/Last/Nth → the single verdict; Every → AND;
// Any → OR.
func (s SpanSource) evaluate(ctx context.Context, m core.Matcher, ev core.Evidence, want string, targets []*trace.Span) (core.Verdict, error) {
	type spanVerdict struct {
		v  core.Verdict
		sp *trace.Span
	}
	results := make([]spanVerdict, 0, len(targets))
	for _, sp := range targets {
		val, err := s.extract(sp)
		if err != nil {
			return core.Verdict{}, err
		}
		derived := ev
		derived.Output = core.Output{Answer: val, Body: []byte(val)}
		v, err := m.Match(ctx, derived, want, "answer")
		if err != nil {
			return core.Verdict{}, fmt.Errorf("result: matcher %q failed on span %q attr %q: %w", m.Name(), sp.Name, s.Attr, err)
		}
		results = append(results, spanVerdict{v, sp})
	}
	switch s.Quant {
	case QuantEvery:
		var reasons []string
		for _, r := range results {
			if !r.v.Pass {
				reasons = append(reasons, s.reason(r.sp, r.v))
			}
		}
		if len(reasons) > 0 {
			return core.Verdict{Pass: false, Reasons: reasons}, nil
		}
		return core.Verdict{Pass: true}, nil
	case QuantAny:
		for _, r := range results {
			if r.v.Pass {
				return core.Verdict{Pass: true}, nil
			}
		}
		reasons := make([]string, 0, len(results))
		for _, r := range results {
			reasons = append(reasons, s.reason(r.sp, r.v))
		}
		return core.Verdict{Pass: false, Reasons: reasons}, nil
	case QuantOne, QuantFirst, QuantLast, QuantNth: // exactly one target
		r := results[0]
		if r.v.Pass {
			return core.Verdict{Pass: true}, nil
		}
		return core.Verdict{Pass: false, Reasons: []string{s.reason(r.sp, r.v)}}, nil
	default:
		// unreachable: selectSpans rejects unknown Quant values before evaluate runs.
		return core.Verdict{}, fmt.Errorf("result: evaluate: unhandled quant %d", s.Quant)
	}
}

// reason renders a failing per-span verdict, prefixed with the span identity so a
// multi-span (every/any) failure names which span tripped.
func (s SpanSource) reason(sp *trace.Span, v core.Verdict) string {
	return fmt.Sprintf("span %q attr %q: %s", sp.Name, s.Attr, strings.Join(v.Reasons, "; "))
}
