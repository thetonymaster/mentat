//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestOrderflowHappyPasses drives the happy checkout scenario end-to-end over the
// http/baggage path and asserts mentat exits zero (every comparator passes).
// Requires: make harness-up (Tempo + Collector + orderflow containers running).
func TestOrderflowHappyPasses(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, mentatBin, "run", "features/checkout.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("mentat run timed out:\n%s", out)
	}
	if err != nil {
		t.Fatalf("mentat run failed (want pass):\n%s", out)
	}
}
