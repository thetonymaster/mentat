package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// ResultExpectation configures the result comparator.
// Matcher selects the matching strategy: exact | contains | regex | json-subset | status | schema.
// Want is the expected value (a string; for status, parsed as int; for schema, a JSON Schema).
// Target selects which Output field value matchers (exact/contains/regex) read:
//   - "" or "answer" → ev.Output.Answer (default)
//   - "status"       → strconv.Itoa(ev.Output.Status)
//   - any other      → error (no silent fallback)
//
// json-subset and schema always read ev.Output.Body; status always reads
// ev.Output.Status. Target is not consulted for those matchers.
type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status | schema
	Want    string
	Target  string      // boundary only: "answer" (default) | "status"; ignored when Source != nil
	Source  *SpanSource // nil => driver Output (default); set => span-attribute source
}

type result struct{ reg *registry.Registry }

// NewResult returns a Comparator that evaluates deterministic matchers resolved from
// reg (the per-engine registry). With a nil ResultExpectation.Source it reads only
// ev.Output (the driver boundary); with Source set it reads ev.Trace via
// resolveSpanSource (a per-span attribute value).
func NewResult(reg *registry.Registry) core.Comparator { return result{reg: reg} }
func (result) Name() string                            { return "result" }

func (r result) Compare(ctx context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ResultExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: expectation must be ResultExpectation, got %T", e)
	}
	if exp.Source != nil {
		return resolveSpanSource(ctx, r.reg, ev, exp)
	}
	m, ok := r.reg.Matcher(exp.Matcher)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	// Bind Want's compiled artifact once, before evaluation (FR-005): authoring
	// errors (bad pattern/schema) surface at expectation construction.
	m, err := compileMatcher(m, exp.Want)
	if err != nil {
		return core.Verdict{}, err
	}
	return m.Match(ctx, ev, exp.Want, exp.Target)
}
