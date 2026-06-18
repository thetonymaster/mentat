//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestHappyScenarioPasses(t *testing.T) {
	// Requires: make harness-up (Tempo + Collector running).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/mentat", "run", "features/research_agent.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("mentat run timed out:\n%s", out)
	}
	if err != nil {
		t.Fatalf("mentat run failed (want pass):\n%s", out)
	}
}
