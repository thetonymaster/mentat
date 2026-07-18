//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMetaLateFlushNeverGreen is the feature-008 (US1) live-harness L3 proof: a
// late-flushing SUT can never produce a green absence verdict. The late-flush
// target force-flushes a decoy batch, idles past the harness stability window,
// then force-flushes a forbidden delete_record span before exiting. The 008 settle
// barrier holds resolution open until the COMPLETE forest is observed, so the
// scenario's `the tool "delete_record" is never called` assertion must FAIL on the
// complete evidence — every single time.
//
// The run is repeated MENTAT_L3_RUNS times (unset → 3 for fast PR CI; the
// release/nightly lane sets 20 to machine-enforce SC-001's threshold). Every
// iteration must exit non-zero AND name delete_record; zero green outcomes are
// tolerated. A set-but-unparsable or < 1 value fails the test loudly rather than
// silently defaulting past bad input (Constitution IV). Requires: make harness-up.
func TestMetaLateFlushNeverGreen(t *testing.T) {
	t.Parallel()

	runs, err := parseL3Runs(os.Getenv("MENTAT_L3_RUNS"))
	if err != nil {
		t.Fatalf("resolve L3 repeat count: %v", err)
	}

	const (
		feature = "features/meta/late_flush_bad.feature"
		reason  = `forbidden tool "delete_record"`
	)
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("run-%d-of-%d", i+1, runs), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, mentatBin, "run", feature)
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("completeness L3: late-flush run %d/%d timed out (the settle barrier must conclude, not hang):\n%s", i+1, runs, out)
			}
			if err == nil {
				t.Fatalf("completeness L3: late-flush run %d/%d passed GREEN against a partial forest — the settle barrier failed:\n%s", i+1, runs, out)
			}
			if !strings.Contains(string(out), reason) {
				t.Fatalf("completeness L3: late-flush run %d/%d failed but not on the forbidden tool (want %q in output):\n%s", i+1, runs, reason, out)
			}
		})
	}
}
