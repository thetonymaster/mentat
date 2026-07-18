//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// strictIngestionQualifier is a stable prefix of the canonical ingestion-window
// qualifier (contracts §3). FR-009 forbids that qualifier from appearing for ANY
// strict-mode run — the declared span count makes completeness exact — so the
// sentinel-good report and console output must NOT contain this text.
const strictIngestionQualifier = "trace-completeness: bounded by ingestion window"

// TestStrictSentinelResolution is the feature-008 (US3, T026) live-harness proof of
// strict-mode span-count completeness (quickstart §3). All three targets drive the
// researchbot sentinel scenarios and declare `completeness: { mode: strict }` in
// mentat.yaml:
//
//   - sentinel-good declares exactly the spans it emits → strict resolution concludes
//     on the complete forest, verdicts pass (exit 0), and NO ingestion-window
//     qualifier appears in the report or console output (FR-009).
//   - sentinel-short declares MORE spans than it emits (6 vs 4) → strict resolution
//     hard-errors at the timeout naming the declared and observed counts. This is a
//     RESOLUTION error, not a comparator verdict — nothing was judged.
//   - sentinel-dup stamps the sentinel on two spans → strict resolution hard-errors
//     immediately on the ambiguous duplicate declaration.
//
// Each subtest execs the prebuilt mentatBin (never `go run`), and every subtest calls
// t.Parallel() so the per-scenario Tempo-ingestion waits overlap. Requires:
// make harness-up (Tempo + Collector).
func TestStrictSentinelResolution(t *testing.T) {
	t.Parallel()

	t.Run("sentinel-good passes with no ingestion-window qualifier", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		report := filepath.Join(dir, "report.json")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// The report flag MUST precede the feature path (flag package stops at the first
		// positional). cmd.Dir = ".." resolves mentat.yaml + the feature from repo root.
		cmd := exec.CommandContext(ctx, mentatBin, "run", "--report-json", report, "features/strict_completeness.feature")
		cmd.Dir = ".."
		out, err := cmd.CombinedOutput()

		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("strict good: run timed out (strict resolution must conclude, not hang):\n%s", out)
		}
		// The well-formed sentinel forest reaches its declared count, so verdicts pass;
		// a non-zero exit is a genuine failure, not the expected outcome.
		if err != nil {
			t.Fatalf("strict good: expected exit 0 for a forest matching its declared count, got %v:\n%s", err, out)
		}

		data, rerr := os.ReadFile(report)
		if rerr != nil {
			t.Fatalf("strict good: report not written: %v\ncombined output:\n%s", rerr, out)
		}
		// FR-009: a strict run's completeness is exact, so the ingestion-window
		// qualifier must appear NOWHERE — not in the report, not on the console.
		if strings.Contains(string(data), strictIngestionQualifier) {
			t.Fatalf("strict good: report carried the ingestion-window qualifier (FR-009 forbids it for strict mode):\n%s", data)
		}
		if strings.Contains(string(out), strictIngestionQualifier) {
			t.Fatalf("strict good: console output carried the ingestion-window qualifier (FR-009 forbids it for strict mode):\n%s", out)
		}
	})

	t.Run("sentinel-short hard-errors naming declared and observed counts", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, mentatBin, "run", "features/meta/strict_short_bad.feature")
		cmd.Dir = ".."
		out, err := cmd.CombinedOutput()

		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("strict short: run timed out (strict resolution must hard-error, not hang):\n%s", out)
		}
		if err == nil {
			t.Fatalf("strict short: expected a non-zero exit (declared 6 > observed 4), but mentat passed:\n%s", out)
		}
		// A RESOLUTION error (not a comparator verdict): the count-short message names
		// the observed and declared counts. researchbot's sentinel-short emits 4 spans
		// and declares 6, so the error reads "strict mode: 4 of 6 declared spans …".
		for _, want := range []string{"strict mode", "4 of 6 declared spans"} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("strict short: expected %q in output (a resolution error naming the declared/observed counts):\n%s", want, out)
			}
		}
	})

	t.Run("sentinel-dup hard-errors on the ambiguous declaration", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, mentatBin, "run", "features/meta/strict_dup_bad.feature")
		cmd.Dir = ".."
		out, err := cmd.CombinedOutput()

		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("strict dup: run timed out (strict resolution must hard-error, not hang):\n%s", out)
		}
		if err == nil {
			t.Fatalf("strict dup: expected a non-zero exit (duplicate sentinel), but mentat passed:\n%s", out)
		}
		// The ambiguous-declaration message names the count of sentinel spans found.
		for _, want := range []string{"strict mode", "sentinel spans found (want exactly 1)"} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("strict dup: expected %q in output (a resolution error naming the ambiguity):\n%s", want, out)
			}
		}
	})
}
