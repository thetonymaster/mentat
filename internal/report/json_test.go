package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestJSONReporter(t *testing.T) {
	var buf bytes.Buffer
	rep := core.RunReport{Total: 1, Passed: 0, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "s", Pass: false, Reasons: []string{"rate = 0.50, want >= 0.80"}, Cost: 0.01},
	}}
	if err := (jsonReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("report: %v", err)
	}
	var round core.RunReport
	if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if round.Failed != 1 || round.Scenarios[0].Name != "s" {
		t.Errorf("round-trip lost data: %+v", round)
	}
}
