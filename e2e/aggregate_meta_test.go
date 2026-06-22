//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestAggregateScalarGoesRed proves that the computed-vs-expected aggregate
// message reaches the real mentat binary output. Each case drives a
// known-bad @runs scenario and asserts Mentat exits non-zero with the
// canonical "aggregate false: <macro> = <value>, want <op> <expected>" line.
// Requires: make harness-up.
func TestAggregateScalarGoesRed(t *testing.T) {
	cases := []struct {
		name    string
		feature string
		wants   []string // every substring must appear in the combined output
	}{
		{
			name:    "p95 aggregate trips with computed-vs-expected",
			feature: "features/meta/aggregate_scalar_bad.feature",
			wants:   []string{"p95 =", "want <= 1"},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
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
			outStr := string(out)
			for _, want := range c.wants {
				if !strings.Contains(outStr, want) {
					t.Fatalf("expected %q in output for %s:\n%s", want, c.feature, outStr)
				}
			}
		})
	}
}
