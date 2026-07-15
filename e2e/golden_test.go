//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// godogDurationLine matches godog's pretty-format trailing total-duration summary
// line — a Go time.Duration string on its own line, e.g. "2.728548125s" or
// "1m2.3s". That total is the only nondeterministic token in a healthy run's
// stdout (run-ids live in the trace layer, not stdout; per-step durations are not
// printed by the pretty formatter), so normalizeStdout collapses it to a stable
// placeholder before any byte comparison. Anchored per-line under (?m) so it can
// only ever match the standalone summary line, never in-line text.
var godogDurationLine = regexp.MustCompile(`(?m)^(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ns|µs|us|ms|s)$`)

// normalizeStdout replaces the nondeterministic total-duration summary line with a
// fixed placeholder so a live run's stdout is byte-stable across invocations. This
// is the EXACT transform used to produce cmd/mentat/testdata/golden-green.txt; the
// golden comparison below is the cross-check that the two stay in sync.
func normalizeStdout(b []byte) string {
	return godogDurationLine.ReplaceAllString(string(b), "<DURATION>")
}

// TestGoldenStdoutSilentByDefault is the SC-005 regression tripwire: it proves the
// new -v/-vv narration feature never leaks onto stdout. All narration goes to
// stderr; stdout stays godog/report territory.
//
// Two independent proofs run against the live harness (needs make harness-up):
//
//  1. A default-verbosity run's normalized stdout must equal the committed golden
//     (cmd/mentat/testdata/golden-green.txt) — stdout is byte-stable modulo the
//     duration line.
//  2. The robust core assertion, independent of the stored golden: the SAME
//     scenario rerun with -vv must produce byte-identical (normalized) stdout,
//     while its stderr is non-empty and carries resolve.poll narration. That
//     directly shows the extra verbosity landed on stderr, not stdout.
func TestGoldenStdoutSilentByDefault(t *testing.T) {
	t.Parallel()

	golden, err := os.ReadFile("../cmd/mentat/testdata/golden-green.txt")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	// Proof 1: default-verbosity stdout matches the committed golden.
	defOut, defErr := runResearchAgent(t) // no verbosity flags
	gotDefault := normalizeStdout(defOut)
	if gotDefault != string(golden) {
		t.Fatalf("default-verbosity stdout drifted from golden (regenerate cmd/mentat/testdata/golden-green.txt only if this change was intended)\n--- want (golden) ---\n%q\n--- got (normalized) ---\n%q", string(golden), gotDefault)
	}
	// Silent by default: no verbosity flag => stderr stays empty (narration is fully
	// gated behind -v/-vv, so a healthy run says nothing on stderr).
	if len(defErr) != 0 {
		t.Fatalf("default-verbosity run wrote to stderr (expected silence):\n%s", defErr)
	}

	// Proof 2: -vv must not perturb stdout by a single byte (after normalization),
	// yet must narrate on stderr.
	vvOut, vvErr := runResearchAgent(t, "-vv")
	gotVV := normalizeStdout(vvOut)
	if gotVV != gotDefault {
		t.Fatalf("-vv changed stdout — narration leaked out of stderr\n--- default (normalized) ---\n%q\n--- -vv (normalized) ---\n%q", gotDefault, gotVV)
	}
	if len(vvErr) == 0 {
		t.Fatalf("-vv produced no stderr; narration did not happen (or went to stdout)")
	}
	if !strings.Contains(string(vvErr), "resolve.poll") {
		t.Fatalf("-vv stderr missing resolve.poll narration (narration did not really run):\n%s", vvErr)
	}
}

// runResearchAgent execs the prebuilt mentatBin (from TestMain — never `go run`,
// which recompiles per invocation and serializes parallel subtests on the cold
// build) as `mentat run [flags] features/research_agent.feature`, captures stdout
// and stderr into SEPARATE buffers (never CombinedOutput — the whole point is to
// keep the two streams apart), asserts a zero exit, and returns both. Flags MUST
// precede the feature path: cmd/mentat parses os.Args[2:] with the flag package,
// which stops at the first non-flag positional. cmd.Dir = ".." (repo root) matches
// the build dir in TestMain and how the golden was captured.
func runResearchAgent(t *testing.T, flags ...string) (stdout, stderr []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	args := append([]string{"run"}, flags...)
	args = append(args, "features/research_agent.feature")
	cmd := exec.CommandContext(ctx, mentatBin, args...)
	cmd.Dir = ".."

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("mentat run timed out (flags %v):\nstdout:\n%s\nstderr:\n%s", flags, outBuf.Bytes(), errBuf.Bytes())
	}
	if err != nil {
		t.Fatalf("mentat run failed (want pass, flags %v): %v\nstdout:\n%s\nstderr:\n%s", flags, err, outBuf.Bytes(), errBuf.Bytes())
	}
	return outBuf.Bytes(), errBuf.Bytes()
}
