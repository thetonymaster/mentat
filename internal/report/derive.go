package report

import (
	"fmt"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// Derive projects a scenario's Verdict + per-run Evidence into a ScenarioResult.
// Cost (per run, plus the scenario total) is derived from every run's Evidence
// forest (Evidence-only, invariant #1). The tool/service Sequence is derived from
// the representative run — evs[0] when it has a non-nil Trace — so callers must pass
// evs in run order (run[0] first); for @runs(N) scenarios only the first run's
// sequence is reported.
//
// Derive is an observer: it never fails a scenario (audit A8 / research R5).
// Verdicts come only from step results. When a derivation cannot be completed
// (e.g. a span missing service.name, or a malformed cost attribute) Derive keeps
// the best-effort detail — an empty sequence, cost 0 for that run — and records a
// human-readable DerivationNote instead of returning an error. The degradation
// stays visible in the JSON and HTML report (no silent fallback, constitution IV).
func Derive(name, featureFile string, tags []string, v core.Verdict, evs []core.Evidence, pricing core.Pricing) core.ScenarioResult {
	sr := core.ScenarioResult{
		Name:        name,
		FeatureFile: featureFile,
		Tags:        tags,
		Pass:        v.Pass,
		Reasons:     v.Reasons,
		Aggregate:   v.Detail,
	}
	// Carry the judge-token ledger through unchanged (US6). Derive is an observer, so
	// it does NOT price it here (an unknown-model pricing error would fail a scenario,
	// violating A8); cost is filled later by report.Price at the render boundary. A
	// copy avoids aliasing the scenario world's accumulator into the collected report.
	if v.Judge != nil {
		j := *v.Judge
		sr.Judge = &j
	}
	var notes []string
	for _, ev := range evs {
		cost, err := comparator.CostOrZero(ev.Trace, pricing)
		if err != nil {
			notes = append(notes, fmt.Sprintf("cost unavailable for run %q: %v", ev.RunID, err))
			cost = 0
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
			notes = append(notes, fmt.Sprintf("sequence unavailable for run %q: %v", evs[0].RunID, err))
		} else {
			sr.Sequence = seq
		}
	}
	if len(notes) > 0 {
		sr.DerivationNote = strings.Join(notes, "; ")
	}
	return sr
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
