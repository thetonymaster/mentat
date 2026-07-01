package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
)

// RetriesExpectation asserts a retry ceiling: the named tool's execute_tool spans
// must not exceed Max invocations across the run.
type RetriesExpectation struct {
	Tool string
	Max  int
}

type retries struct{}

func NewRetries() core.Comparator { return retries{} }
func (retries) Name() string      { return "retries" }

func (retries) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(RetriesExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("retries: expectation must be RetriesExpectation, got %T", e)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("retries: Evidence.Trace is nil")
	}

	// Reuse the package-local execute_tool scan so a span missing gen_ai.tool.name
	// is a hard error, never a silent undercount.
	seq, err := toolSequence(ev.Trace)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("retries: %w", err)
	}

	count := 0
	for _, name := range seq {
		if name == exp.Tool {
			count++
		}
	}

	v := core.Verdict{Pass: true}
	if count > exp.Max {
		v.Pass = false
		v.Reasons = append(v.Reasons, fmt.Sprintf("tool %q was called %d times, exceeding the maximum of %d", exp.Tool, count, exp.Max))
	}
	return v, nil
}
