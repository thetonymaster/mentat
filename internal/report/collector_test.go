package report

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestCollector(t *testing.T) {
	c := NewCollector()
	c.Append(core.ScenarioResult{Name: "a", Pass: true, Cost: 0.01})
	c.Append(core.ScenarioResult{Name: "b", Pass: false, Cost: 0.02})
	rep := c.Report(time.Unix(0, 0), 5*time.Second, false)
	if rep.Total != 2 || rep.Passed != 1 || rep.Failed != 1 {
		t.Errorf("totals = %+v", rep)
	}
	if math.Abs(rep.TotalCost-0.03) >= 1e-9 {
		t.Errorf("total cost = %v, want ~0.03", rep.TotalCost)
	}
}

func TestCollector_Scenarios(t *testing.T) {
	tests := []struct {
		name       string
		appends    []core.ScenarioResult
		wantTotal  int
		wantPassed int
		wantFailed int
		wantCost   float64
	}{
		{
			name:       "empty_collector",
			appends:    nil,
			wantTotal:  0,
			wantPassed: 0,
			wantFailed: 0,
			wantCost:   0,
		},
		{
			name: "all_passed",
			appends: []core.ScenarioResult{
				{Name: "x", Pass: true, Cost: 0.01},
				{Name: "y", Pass: true, Cost: 0.02},
			},
			wantTotal:  2,
			wantPassed: 2,
			wantFailed: 0,
			wantCost:   0.03,
		},
		{
			name: "all_failed",
			appends: []core.ScenarioResult{
				{Name: "p", Pass: false, Cost: 0.00},
				{Name: "q", Pass: false, Cost: 0.00},
			},
			wantTotal:  2,
			wantPassed: 0,
			wantFailed: 2,
			wantCost:   0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			c := NewCollector()
			for _, sr := range tt.appends {
				c.Append(sr)
			}
			rep := c.Report(time.Now(), time.Second, false)
			if rep.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", rep.Total, tt.wantTotal)
			}
			if rep.Passed != tt.wantPassed {
				t.Errorf("Passed = %d, want %d", rep.Passed, tt.wantPassed)
			}
			if rep.Failed != tt.wantFailed {
				t.Errorf("Failed = %d, want %d", rep.Failed, tt.wantFailed)
			}
			if math.Abs(rep.TotalCost-tt.wantCost) >= 1e-9 {
				t.Errorf("TotalCost = %v, want ~%v", rep.TotalCost, tt.wantCost)
			}
			if len(rep.Scenarios) != tt.wantTotal {
				t.Errorf("len(Scenarios) = %d, want %d", len(rep.Scenarios), tt.wantTotal)
			}
		})
	}
}

func TestCollector_ConcurrentAppend(t *testing.T) {
	c := NewCollector()
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			c.Append(core.ScenarioResult{Pass: true, Cost: 0.001})
		}()
	}
	wg.Wait()
	rep := c.Report(time.Now(), time.Second, false)
	if rep.Total != n {
		t.Errorf("concurrent Total = %d, want %d", rep.Total, n)
	}
	if rep.Passed != n {
		t.Errorf("concurrent Passed = %d, want %d", rep.Passed, n)
	}
}

func TestCollector_ReportMetadata(t *testing.T) {
	c := NewCollector()
	started := time.Unix(1000, 0)
	dur := 42 * time.Second
	rep := c.Report(started, dur, false)
	if !rep.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", rep.StartedAt, started)
	}
	if rep.Duration != dur {
		t.Errorf("Duration = %v, want %v", rep.Duration, dur)
	}
}
