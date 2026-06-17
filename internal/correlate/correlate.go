package correlate

import (
	"context"
	"fmt"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// PollConfig controls the stable-poll behaviour of Resolve.
type PollConfig struct {
	Interval  time.Duration
	StableFor int // consecutive stable iterations required
	Timeout   time.Duration
}

type correlator struct {
	idFn func() string
	poll PollConfig
}

// New returns a core.Correlator that uses idFn to generate run IDs and poll
// according to the given PollConfig.
func New(idFn func() string, poll PollConfig) core.Correlator {
	return &correlator{idFn: idFn, poll: poll}
}

// Inject sets spec.RunID and spec.Tags["test.run.id"] to a fresh run ID and
// returns it (spec §5 — tag-first correlation).
func (c *correlator) Inject(_ context.Context, spec *core.RunSpec) string {
	id := c.idFn()
	spec.RunID = id
	if spec.Tags == nil {
		spec.Tags = map[string]string{}
	}
	spec.Tags["test.run.id"] = id
	return id
}

// Resolve queries the store for all traces tagged runID, fetches and merges them
// into one forest, and polls until the merged span count is stable for StableFor
// consecutive iterations. Zero traces within Timeout is a hard error (invariant §4).
func (c *correlator) Resolve(ctx context.Context, store core.TraceStore, runID string) (*trace.Trace, error) {
	deadline := time.Now().Add(c.poll.Timeout)
	lastCount, stable := -1, 0
	var merged *trace.Trace

	for {
		refs, err := store.Query(ctx, core.TraceQuery{Tag: "test.run.id", Value: runID})
		if err != nil {
			return nil, fmt.Errorf("correlate: query: %w", err)
		}

		m := &trace.Trace{RunID: runID}
		for _, ref := range refs {
			tr, err := store.GetByID(ctx, ref.TraceID)
			if err != nil {
				return nil, fmt.Errorf("correlate: get %s: %w", ref.TraceID, err)
			}
			m.Roots = append(m.Roots, tr.Roots...)
			m.Spans = append(m.Spans, tr.Spans...)
		}
		merged = m

		if len(m.Spans) > 0 && len(m.Spans) == lastCount {
			stable++
			if stable >= c.poll.StableFor {
				return merged, nil
			}
		} else {
			stable = 0
		}
		lastCount = len(m.Spans)

		if time.Now().After(deadline) {
			if len(m.Spans) == 0 {
				return nil, fmt.Errorf("correlate: no trace for run %q within %v (0 spans seen)", runID, c.poll.Timeout)
			}
			return merged, nil // deadline reached but spans present — return best effort
		}
		time.Sleep(c.poll.Interval)
	}
}
