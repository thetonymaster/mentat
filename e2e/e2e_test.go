//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestHappyScenarioPasses drives good scenarios end-to-end through the full
// pipeline and asserts mentat exits zero (every comparator passes).
// Requires: make harness-up (Tempo + Collector running).
func TestHappyScenarioPasses(t *testing.T) {
	tests := []struct {
		name    string
		feature string
	}{
		{"summarizes Q3 revenue within budget", "features/research_agent.feature"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", "./cmd/mentat", "run", tc.feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("mentat run timed out for %s:\n%s", tc.feature, out)
			}
			if err != nil {
				t.Fatalf("mentat run failed (want pass) for %s:\n%s", tc.feature, out)
			}
		})
	}
}
