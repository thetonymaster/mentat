package report

import (
	"fmt"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Derive projects a scenario's Verdict + per-run Evidence into a ScenarioResult.
// Cost and sequence are derived from the Evidence forest (Evidence-only, invariant #1).
func Derive(name string, tags []string, v core.Verdict, evs []core.Evidence, pricing core.Pricing) (core.ScenarioResult, error) {
	sr := core.ScenarioResult{
		Name:      name,
		Tags:      tags,
		Pass:      v.Pass,
		Reasons:   v.Reasons,
		Aggregate: v.Detail,
	}
	for _, ev := range evs {
		cost, err := comparator.CostOrZero(ev.Trace, pricing)
		if err != nil {
			return core.ScenarioResult{}, fmt.Errorf("report.Derive: run %q: %w", ev.RunID, err)
		}
		rec := core.RunRecord{
			RunID:       ev.RunID,
			Passed:      !ev.Failed,
			FailureKind: ev.FailureKind,
			Cost:        cost,
		}
		if ev.Trace != nil {
			rec.LatencyMS = ev.Trace.Envelope().Milliseconds()
		}
		sr.Runs = append(sr.Runs, rec)
		sr.Cost += cost
	}
	if len(evs) > 0 && evs[0].Trace != nil {
		seq, err := sequence(evs[0].Trace)
		if err != nil {
			return core.ScenarioResult{}, fmt.Errorf("report.Derive: sequence: %w", err)
		}
		sr.Sequence = seq
	}
	return sr, nil
}

// sequence returns the tool-call sequence (agents) or, if none, the service-hop
// sequence (microservices) for the representative run.
func sequence(tr *trace.Trace) ([]string, error) {
	tools, err := comparator.ToolSequence(tr)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		return tools, nil
	}
	return comparator.ServiceSequence(tr)
}
