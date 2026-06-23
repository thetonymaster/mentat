package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestHTMLReporter(t *testing.T) {
	var buf bytes.Buffer
	rep := core.RunReport{Total: 1, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "flaky", Pass: false, Cost: 0.0125,
			Reasons:   []string{"rate = 0.50, want >= 0.80"},
			Runs:      []core.RunRecord{{RunID: "abc", Passed: true, LatencyMS: 120}},
			Aggregate: &core.AggregateDetail{Macro: "rate", Op: ">=", Computed: 0.5, Expected: 0.8}},
	}}
	if err := (htmlReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("report: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"<html", "flaky", "rate = 0.50, want &gt;= 0.80", "abc", "0.0125"} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q", want)
		}
	}
}
