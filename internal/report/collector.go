package report

import (
	"sync"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// Collector accumulates per-scenario results across a run. Append is safe for the
// concurrent scenarios godog may run (the -concurrency flag).
type Collector struct {
	mu        sync.Mutex
	scenarios []core.ScenarioResult
}

func NewCollector() *Collector { return &Collector{} }

func (c *Collector) Append(sr core.ScenarioResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scenarios = append(c.scenarios, sr)
}

// Report folds the accumulated scenarios into a RunReport with rollups. interrupted
// records whether a signal cancelled the run before completion (feature 003); it
// surfaces as RunReport.Interrupted so every emitted format carries the marker.
func (c *Collector) Report(started time.Time, dur time.Duration, interrupted bool) core.RunReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	rep := core.RunReport{StartedAt: started, Duration: dur, Total: len(c.scenarios), Interrupted: interrupted}
	rep.Scenarios = append(rep.Scenarios, c.scenarios...)
	var judgeTotal *core.JudgeUsage
	for _, sr := range c.scenarios {
		if sr.Pass {
			rep.Passed++
		} else {
			rep.Failed++
		}
		rep.TotalCost += sr.Cost
		// Fold each scenario's judge usage into the suite total (US6). The total stays
		// nil until a scenario actually made a judge call, so a run with no semantic
		// checks carries no fabricated all-zero total (FR-006). Model is left empty —
		// the total is not attributed to one model. CostUsd is summed here from the
		// per-scenario cost (which report.Price fills before rendering).
		if sr.Judge != nil {
			if judgeTotal == nil {
				judgeTotal = &core.JudgeUsage{}
			}
			judgeTotal.Calls += sr.Judge.Calls
			judgeTotal.InputTokens += sr.Judge.InputTokens
			judgeTotal.OutputTokens += sr.Judge.OutputTokens
			judgeTotal.CostUsd += sr.Judge.CostUsd
		}
	}
	rep.JudgeTotal = judgeTotal
	return rep
}
