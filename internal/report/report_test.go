package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestReportRendersDerivationNote proves the audit-A8 note stays visible in the
// rendered artifacts (research R5 / constitution IV: surfaced, not swallowed). A
// scenario carrying a DerivationNote must show it in BOTH the JSON and the HTML
// report; a clean scenario must not clutter either output with it.
func TestReportRendersDerivationNote(t *testing.T) {
	t.Parallel()

	// A slice of the note that survives HTML escaping (no quotes/angle brackets),
	// so one substring works for both renderers.
	const noteFragment = "missing service.name"
	const note = `sequence unavailable for run "r1": sequence: span[0] ("fetch") ` + noteFragment

	withNote := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{
		{Name: "degraded", Pass: true, DerivationNote: note},
	}}
	clean := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{
		{Name: "healthy", Pass: true},
	}}

	tests := []struct {
		name     string
		reporter core.Reporter
	}{
		{"json", jsonReporter{}},
		{"html", htmlReporter{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var withBuf bytes.Buffer
			if err := tt.reporter.Report(withNote, &withBuf); err != nil {
				t.Fatalf("Report(withNote): %v", err)
			}
			if !strings.Contains(withBuf.String(), noteFragment) {
				t.Errorf("%s output does not render DerivationNote; want it to contain %q, got:\n%s",
					tt.name, noteFragment, withBuf.String())
			}

			var cleanBuf bytes.Buffer
			if err := tt.reporter.Report(clean, &cleanBuf); err != nil {
				t.Fatalf("Report(clean): %v", err)
			}
			if strings.Contains(cleanBuf.String(), noteFragment) {
				t.Errorf("%s output rendered a note for a clean scenario; got:\n%s", tt.name, cleanBuf.String())
			}
		})
	}
}
