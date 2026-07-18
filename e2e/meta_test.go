//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestBadScenariosAreCaught proves Mentat goes red on deliberately bad scenarios.
// Each feature drives a scenario that violates exactly one assertion, so the
// corresponding comparator must trip and mentat must exit non-zero.
func TestBadScenariosAreCaught(t *testing.T) {
	t.Parallel()
	cases := []struct {
		feature string
		reason  string // substring expected in combined output
	}{
		{"features/meta/wrong_order.feature", "sequence failed"},
		{"features/meta/over_budget.feature", "exceed budget"},
		{"features/meta/forbidden.feature", "forbidden tool"},
		{"features/meta/bad_answer.feature", "result contains"},
		{"features/meta/bad_shape.feature", "shape failed"},
		{"features/meta/bad_expectation.feature", "shape failed"},
		{"features/meta/bad_result_span.feature", "result contains"},
		// Feature 008 (US1): the late-flushing SUT's forbidden delete_record span is
		// force-flushed after the stability window; the settle barrier must still
		// catch it. TestMetaLateFlushNeverGreen is the SC-001 repeat gate — this row
		// keeps late-flush in the single-run agent-path catalog.
		{"features/meta/late_flush_bad.feature", `forbidden tool "delete_record"`},
		// Feature 008 (US3): strict-mode sentinel targets hard-error at RESOLUTION (not
		// via a comparator verdict) when the declared span count is unmet. sentinel-short
		// declares more than it emits (count-short error); sentinel-dup declares twice
		// (ambiguous-declaration error). Both surface a "strict mode:" resolution error,
		// so mentat exits non-zero. TestStrictSentinelResolution is the richer proof
		// (it asserts the specific declared/observed counts and the ambiguity message).
		{"features/meta/strict_short_bad.feature", "declared spans"},
		{"features/meta/strict_dup_bad.feature", "sentinel spans found (want exactly 1)"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.feature, func(t *testing.T) {
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
			if !strings.Contains(string(out), c.reason) {
				t.Fatalf("expected reason %q in output for %s:\n%s", c.reason, c.feature, out)
			}
		})
	}
}
