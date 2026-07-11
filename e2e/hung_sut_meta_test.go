//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHungSUTL3 is the feature-003 (US1) live-harness L3 proof: a SUT that never
// exits must be bounded by its per-target run_timeout (2s for hung-agent in
// mentat.yaml), fail the scenario with a phase-attributed run-timeout error well
// within budget + kill grace, and leave no surviving process from its tree.
// Requires: make harness-up.
func TestHungSUTL3(t *testing.T) {
	t.Parallel()

	// Safety net far above budget (2s) + grace (10s): if mentat itself hangs, the
	// drive was never bounded — the exact bug this proves dead.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, mentatBin, "run", "features/meta/hung_sut.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("hung_sut run itself hung (drive not bounded) after %v:\n%s", elapsed, out)
	}
	if err == nil {
		t.Fatalf("expected FAILURE for a hung SUT, but mentat passed:\n%s", out)
	}
	// The run-budget bound (2s) must fire — not the 30s poll timeout. A fast fail
	// discriminates: an unbounded drive would fall through to poll timeout (~30s).
	if elapsed > 20*time.Second {
		t.Fatalf("hung SUT took %v to fail; the run budget did not bound it:\n%s", elapsed, out)
	}
	if want := "run timeout"; !strings.Contains(string(out), want) {
		t.Fatalf("expected %q (phase-attributed timeout) in output:\n%s", want, out)
	}
	// SC-001 / FR-002: no process from the SUT tree survives the run.
	if survivors := waitNoSurvivors(t, "987654", 10*time.Second); survivors != "" {
		t.Fatalf("SUT process tree survived the run (pgrep 987654):\n%s", survivors)
	}
}

// waitNoSurvivors polls pgrep for the marker until none remain or timeout elapses,
// returning the last non-empty match list ("" means the tree was fully reaped).
func waitNoSurvivors(t *testing.T, marker string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out, _ := exec.Command("pgrep", "-fl", marker).CombinedOutput()
		last := strings.TrimSpace(string(out))
		if last == "" {
			return ""
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
}
