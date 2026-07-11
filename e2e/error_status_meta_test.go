//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestErrorStatusL3 is the F5 live-harness L3 meta-test for audit finding A1:
// a span carrying the canonical trace.StatusError must trip the error budget on
// live Tempo. The "error" scenario drives a fully happy order flow (201,
// {"status":"confirmed"}) whose only deviation is one errored child span on the
// terminal notify leaf. So the ONLY assertion that can go red is
// `Then no span has status "ERROR"` (budgets, MaxErrors: 0). Pre-A1-fix the
// error count matched the literal "ERROR" (never equal to Tempo's normalized
// "Error"), yielding count 0 and a silent false green; this test pins that dead.
// Requires: make harness-up.
func TestErrorStatusL3(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, mentatBin, "run", "features/meta/error_status.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("expected FAILURE for error_status.feature, but run timed out:\n%s", out)
	}
	if err == nil {
		t.Fatalf("expected FAILURE for error_status.feature, but mentat passed:\n%s", out)
	}
	if want := "error spans exceed budget"; !strings.Contains(string(out), want) {
		t.Fatalf("expected %q in output for error_status.feature:\n%s", want, out)
	}
}
