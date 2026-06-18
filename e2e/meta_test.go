//go:build e2e

package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// TestBadScenariosAreCaught proves Mentat goes red on deliberately bad scenarios.
// Each feature drives a scenario that violates exactly one assertion, so the
// corresponding comparator must trip and mentat must exit non-zero.
func TestBadScenariosAreCaught(t *testing.T) {
	cases := []struct {
		feature string
		reason  string // substring expected in combined output
	}{
		{"features/meta/wrong_order.feature", "sequence failed"},
		{"features/meta/over_budget.feature", "tokens"},
		{"features/meta/forbidden.feature", "forbidden tool"},
		{"features/meta/bad_answer.feature", "result contains"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.feature, func(t *testing.T) {
			cmd := exec.Command("go", "run", "./cmd/mentat", "run", c.feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected FAILURE for %s, but mentat passed:\n%s", c.feature, out)
			}
			if !strings.Contains(string(out), c.reason) {
				t.Fatalf("expected reason %q in output for %s:\n%s", c.reason, c.feature, out)
			}
		})
	}
}
