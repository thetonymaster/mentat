package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// ResultExpectation configures the result comparator.
// Matcher selects the matching strategy: exact | contains | regex | json-subset | status.
// Want is the expected value (a string; for status, parsed as int).
// Target selects which Output field value matchers (exact/contains/regex) read:
//   - "" or "answer" → ev.Output.Answer (default)
//   - "status"       → strconv.Itoa(ev.Output.Status)
//   - any other      → error (no silent fallback)
//
// json-subset always reads ev.Output.Body; status always reads ev.Output.Status.
// Target is not consulted for those matchers.
type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status
	Want    string
	Target  string // "answer" (default) or "status"
}

type result struct{}

// NewResult returns a Comparator that evaluates driver Output using registered
// deterministic matchers. It reads only ev.Output; it never touches ev.Trace.
func NewResult() core.Comparator { return result{} }
func (result) Name() string      { return "result" }

func (result) Compare(ctx context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ResultExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: expectation must be ResultExpectation, got %T", e)
	}
	m, ok := registry.Matcher(exp.Matcher)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	return m.Match(ctx, ev, exp.Want, exp.Target)
}
