package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
)

type SequenceExpectation struct {
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
	var actual []string
	for _, s := range ev.Trace.ByOp(genai.OpExecuteTool) {
		actual = append(actual, s.Attr(genai.ToolName))
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
			v.Reasons = append(v.Reasons, fmt.Sprintf("forbidden tool %q was called", a))
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
			v.Reasons = append(v.Reasons, fmt.Sprintf("expected tool %q not found in order; actual sequence = %v", want, actual))
		}
	}
	return v, nil
}
