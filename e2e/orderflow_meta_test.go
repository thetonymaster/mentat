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
	t.Parallel()
	cases := []struct {
		name    string
		feature string // input: the bad-scenario feature mentat drives
		want    string // substring expected in mentat's combined output when it goes red
	}{
		{"reorder trips sequence(service)", "features/meta/orderflow/reorder.feature", "sequence failed"},
		{"legacy_path trips sequence(service)", "features/meta/orderflow/legacy_path.feature", "sequence failed"},
		{"inventory_out trips sequence(service)", "features/meta/orderflow/inventory_out.feature", "sequence failed"},
		{"payment_decline trips result(status)", "features/meta/orderflow/payment_decline.feature", "result status"},
		{"slow trips latency budget", "features/meta/orderflow/slow.feature", "run latency"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, mentatBin, "run", c.feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("expected FAILURE for %s, but run timed out:\n%s", c.feature, out)
			}
			if err == nil {
				t.Fatalf("expected FAILURE for %s, but mentat passed:\n%s", c.feature, out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("expected %q in output for %s:\n%s", c.want, c.feature, out)
			}
		})
	}
}
