//go:build e2e

package e2e

import (
	"os/exec"
	"testing"
)

func TestHappyScenarioPasses(t *testing.T) {
	// Requires: make harness-up (Tempo + Collector running).
	cmd := exec.Command("go", "run", "./cmd/mentat", "run", "features/research_agent.feature")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mentat run failed (want pass):\n%s", out)
	}
}
