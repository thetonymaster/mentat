//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestOrderflowBadScenariosAreCaught proves Mentat goes red over the http/baggage
// microservice path. Each feature drives a scenario that violates exactly one
// happy-path assertion, so the corresponding comparator must trip and mentat must
// exit non-zero. Requires: make harness-up.
func TestOrderflowBadScenariosAreCaught(t *testing.T) {
	cases := []struct {
		feature string
		reason  string // substring expected in combined output
	}{
		{"features/meta/orderflow/reorder.feature", "sequence failed"},
		{"features/meta/orderflow/legacy_path.feature", "sequence failed"},
		{"features/meta/orderflow/inventory_out.feature", "sequence failed"},
		{"features/meta/orderflow/payment_decline.feature", "result status"},
		{"features/meta/orderflow/slow.feature", "run latency"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.feature, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", "./cmd/mentat", "run", c.feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("expected FAILURE for %s, but run timed out:\n%s", c.feature, out)
			}
			if err == nil {
				t.Fatalf("expected FAILURE for %s, but mentat passed:\n%s", c.feature, out)
			}
			if !strings.Contains(string(out), c.reason) {
				t.Fatalf("expected reason %q in output for %s:\n%s", c.reason, c.feature, out)
			}
		})
	}
}
