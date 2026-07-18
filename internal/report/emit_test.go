package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestEmitReportsAttemptsEveryTargetOnFailure proves EmitReports does not abandon a
// valid report just because ANOTHER target fails: with a valid json target and a
// junit target whose parent dir is missing, the json file must be written AND the
// returned error must name the junit failure — regardless of map iteration order.
//
// Go randomizes map range order per call, so the loop exercises both orderings. The
// pre-fix code returned on the FIRST failure, so it dropped the json report whenever
// the map yielded junit first; this test therefore goes red without the
// deterministic, attempt-every-target fix (errors.Join over sorted keys).
func TestEmitReportsAttemptsEveryTargetOnFailure(t *testing.T) {
	RegisterBuiltins() // idempotent; ensures json/junit are registered
	rep := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{{Name: "ok", Pass: true}}}
	const iterations = 30
	for i := 0; i < iterations; i++ {
		dir := t.TempDir()
		jsonPath := filepath.Join(dir, "report.json")
		badJUnit := filepath.Join(dir, "no-such-dir", "report.xml") // missing parent dir
		targets := map[string]string{"json": jsonPath, "junit": badJUnit}

		err := EmitReports(rep, targets)
		if err == nil {
			t.Fatalf("iter %d: want an error for the missing-parent junit target, got nil", i)
		}
		if !strings.Contains(err.Error(), "writing junit report") {
			t.Fatalf("iter %d: error must name the junit write failure, got %q", i, err.Error())
		}
		// The valid json report must be written every iteration, independent of which
		// key the map yielded first (order-independence).
		data, rerr := os.ReadFile(jsonPath)
		if rerr != nil {
			t.Fatalf("iter %d: valid json report was not written when junit failed: %v", i, rerr)
		}
		if len(data) == 0 {
			t.Fatalf("iter %d: json report is empty", i)
		}
	}
}
