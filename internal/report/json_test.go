package report

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestJSONReporter(t *testing.T) {
	var buf bytes.Buffer
	rep := core.RunReport{
		Total:     1,
		Passed:    0,
		Failed:    1,
		TotalCost: 0.01,
		Scenarios: []core.ScenarioResult{
			{Name: "s", Pass: false, Reasons: []string{"rate = 0.50, want >= 0.80"}, Cost: 0.01},
		},
	}
	if err := (jsonReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("report: %v", err)
	}
	var round core.RunReport
	if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if round.Total != 1 {
		t.Errorf("round-trip Total = %d, want 1", round.Total)
	}
	if round.Passed != 0 {
		t.Errorf("round-trip Passed = %d, want 0", round.Passed)
	}
	if round.Failed != 1 {
		t.Errorf("round-trip Failed = %d, want 1", round.Failed)
	}
	if math.Abs(round.TotalCost-0.01) >= 1e-9 {
		t.Errorf("round-trip TotalCost = %v, want ~0.01", round.TotalCost)
	}
	if len(round.Scenarios) == 0 {
		t.Fatalf("round-trip Scenarios is empty")
	}
	if round.Scenarios[0].Name != "s" {
		t.Errorf("round-trip Scenarios[0].Name = %q, want %q", round.Scenarios[0].Name, "s")
	}
	if len(round.Scenarios[0].Reasons) == 0 || round.Scenarios[0].Reasons[0] != "rate = 0.50, want >= 0.80" {
		t.Errorf("round-trip Scenarios[0].Reasons = %v, want [rate = 0.50, want >= 0.80]", round.Scenarios[0].Reasons)
	}
	if math.Abs(round.Scenarios[0].Cost-0.01) >= 1e-9 {
		t.Errorf("round-trip Scenarios[0].Cost = %v, want ~0.01", round.Scenarios[0].Cost)
	}
}
