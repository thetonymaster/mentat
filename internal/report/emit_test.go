package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestEmitReportsAttemptsEveryTargetOnFailure proves EmitReports does not abandon a
// valid report just because ANOTHER target fails, and that it collects every failure.
//
// The proof is deterministic (no map-order gamble): EmitReports sorts target names,
// so with the built-in reporters the attempt order is fixed html < json < junit. The
// first case makes html (attempted first) fail and junit (attempted second) succeed —
// the pre-fix return-on-first-error code would return before junit is written, so the
// "junit report exists and is non-empty" assertion fails on the old code. The second
// case makes BOTH html and junit fail — the pre-fix code surfaces only the first
// error, so the "error contains the junit failure" assertion fails on the old code.
func TestEmitReportsAttemptsEveryTargetOnFailure(t *testing.T) {
	t.Parallel()
	RegisterBuiltins() // idempotent; ensures html/json/junit are registered
	rep := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{{Name: "ok", Pass: true}}}

	tests := []struct {
		name string
		// build populates targets under dir and returns the map plus the path that
		// must be written non-empty ("" when every target is expected to fail).
		build           func(dir string) (targets map[string]string, wantWritten string)
		wantErrContains []string
	}{
		{
			name: "valid junit survives an earlier html failure",
			build: func(dir string) (map[string]string, string) {
				junitPath := filepath.Join(dir, "report.xml")
				// Missing parent dir => html (sorted first) fails; junit still runs.
				badHTML := filepath.Join(dir, "no-such-dir", "report.html")
				return map[string]string{"html": badHTML, "junit": junitPath}, junitPath
			},
			wantErrContains: []string{"writing html report"},
		},
		{
			name: "every failure is joined",
			build: func(dir string) (map[string]string, string) {
				badHTML := filepath.Join(dir, "no-html-dir", "report.html")
				badJUnit := filepath.Join(dir, "no-junit-dir", "report.xml")
				return map[string]string{"html": badHTML, "junit": badJUnit}, ""
			},
			wantErrContains: []string{"writing html report", "writing junit report"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			targets, wantWritten := tt.build(dir)

			err := EmitReports(rep, targets)
			if err == nil {
				t.Fatalf("want an error for the failing target(s), got nil")
			}
			for _, sub := range tt.wantErrContains {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error must contain %q, got %q", sub, err.Error())
				}
			}
			if wantWritten != "" {
				data, rerr := os.ReadFile(wantWritten)
				if rerr != nil {
					t.Fatalf("valid report %q was not written despite an earlier target failing: %v", wantWritten, rerr)
				}
				if len(data) == 0 {
					t.Fatalf("valid report %q is empty", wantWritten)
				}
			}
		})
	}
}
