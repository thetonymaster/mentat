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

// Report folds the accumulated scenarios into a RunReport with rollups.
func (c *Collector) Report(started time.Time, dur time.Duration) core.RunReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	rep := core.RunReport{StartedAt: started, Duration: dur, Total: len(c.scenarios)}
	rep.Scenarios = append(rep.Scenarios, c.scenarios...)
	for _, sr := range c.scenarios {
		if sr.Pass {
			rep.Passed++
		} else {
			rep.Failed++
		}
		rep.TotalCost += sr.Cost
	}
	return rep
}
