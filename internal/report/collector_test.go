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

// TestCollector_JudgeTotal proves the collector folds each scenario's judge usage
// into a suite JudgeTotal (US6 / judge-ledger contract): calls, input and output
// tokens AND any already-filled CostUsd sum field-wise across scenarios that made
// judge calls, and the total is nil when NO scenario made a judge call — absence of
// usage is not a fabricated all-zero total (FR-006, "no fabricated zeros"). Per-scenario
// CostUsd is filled by Price before the report is folded; the collector aggregates it
// into JudgeTotal.CostUsd rather than recomputing.
func TestCollector_JudgeTotal(t *testing.T) {
	tests := []struct {
		name      string
		appends   []core.ScenarioResult
		wantNil   bool
		wantCalls int
		wantIn    int64
		wantOut   int64
		wantCost  float64
	}{
		{
			name: "no judge calls yields a nil total (no fabricated zeros)",
			appends: []core.ScenarioResult{
				{Name: "a", Pass: true, Cost: 0.01},
				{Name: "b", Pass: false},
			},
			wantNil: true,
		},
		{
			name: "sums judge usage across the scenarios that called the judge",
			appends: []core.ScenarioResult{
				{Name: "a", Pass: true, Judge: &core.JudgeUsage{Calls: 3, InputTokens: 1250, OutputTokens: 90, Model: "judge-model", CostUsd: 0.0125}},
				{Name: "b", Pass: true}, // made no judge call — contributes nothing
				{Name: "c", Pass: true, Judge: &core.JudgeUsage{Calls: 9, InputTokens: 3750, OutputTokens: 270, Model: "judge-model", CostUsd: 0.0375}},
			},
			wantCalls: 12,
			wantIn:    5000,
			wantOut:   360,
			wantCost:  0.05,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCollector()
			for _, sr := range tt.appends {
				c.Append(sr)
			}
			rep := c.Report(time.Now(), time.Second, false)
			if tt.wantNil {
				if rep.JudgeTotal != nil {
					t.Fatalf("JudgeTotal = %+v, want nil (no judge calls => no fabricated total)", rep.JudgeTotal)
				}
				return
			}
			if rep.JudgeTotal == nil {
				t.Fatal("JudgeTotal is nil, want the summed judge usage")
			}
			if rep.JudgeTotal.Calls != tt.wantCalls {
				t.Errorf("JudgeTotal.Calls = %d, want %d", rep.JudgeTotal.Calls, tt.wantCalls)
			}
			if rep.JudgeTotal.InputTokens != tt.wantIn {
				t.Errorf("JudgeTotal.InputTokens = %d, want %d", rep.JudgeTotal.InputTokens, tt.wantIn)
			}
			if rep.JudgeTotal.OutputTokens != tt.wantOut {
				t.Errorf("JudgeTotal.OutputTokens = %d, want %d", rep.JudgeTotal.OutputTokens, tt.wantOut)
			}
			if math.Abs(rep.JudgeTotal.CostUsd-tt.wantCost) >= 1e-9 {
				t.Errorf("JudgeTotal.CostUsd = %v, want ~%v", rep.JudgeTotal.CostUsd, tt.wantCost)
			}
			// The suite total is not attributed to one model (the contract's judgeTotal
			// carries no model key), so the collector leaves Model empty.
			if rep.JudgeTotal.Model != "" {
				t.Errorf("JudgeTotal.Model = %q, want empty (total is not per-model)", rep.JudgeTotal.Model)
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
