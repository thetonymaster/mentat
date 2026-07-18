// Package mentat_test — SC-004 hermetic stdout golden for the public entry point.
// This proves mentat.Run's pretty-format stdout is byte-stable WITHOUT Tempo or a
// network: a deterministic in-memory driver+store pair (the bus harness in
// mentat_run_test.go) serves the trace keyed by the injected run id, so the default
// UUID correlator resolves the scenario GREEN. It complements e2e/golden_test.go
// (which pins the SAME transform against the live harness): both normalize only
// godog's trailing total-duration summary line, the sole nondeterministic token.
package mentat_test

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/thetonymaster/mentat"
)

// goldenHermeticPath is the committed fixture and the feature it renders. The feature
// is referenced by a FIXED RELATIVE path so godog's pretty scenario-line comment
// (# <path>:<line>) is byte-stable across machines — a t.TempDir() path would embed a
// random absolute prefix and defeat the golden.
const (
	goldenHermeticPath  = "testdata/golden-hermetic.txt"
	goldenFeaturePath   = "testdata/golden-hermetic.feature"
	goldenAnswer        = "golden ok"
	goldenUpdateEnv     = "MENTAT_UPDATE_GOLDEN"
	goldenRegistryName  = "golden"
	goldenScenarioNamed = "a custom driver and store drive a scenario green"
)

// godogGoldenDurationLine matches godog's pretty-format trailing total-duration summary
// line — a Go time.Duration on its own line, e.g. "2.7s" or "1m2.3s". That total is the
// only nondeterministic token in a healthy run's stdout, so normalizeGoldenStdout
// collapses it to a fixed placeholder before comparison. Anchored per-line under (?m).
// Duplicated (not imported) from e2e/golden_test.go: the root package cannot import the
// e2e package (build-tagged), and keeping the two in lockstep is exactly what the paired
// goldens guard.
var godogGoldenDurationLine = regexp.MustCompile(`(?m)^(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ns|µs|us|ms|s)$`)

// normalizeGoldenStdout replaces the nondeterministic total-duration summary line with a
// fixed placeholder so the run's stdout is byte-stable across invocations.
func normalizeGoldenStdout(b []byte) string {
	return godogGoldenDurationLine.ReplaceAllString(string(b), "<DURATION>")
}

// TestGoldenHermeticStdout is the SC-004 hermetic proof: mentat.Run's pretty stdout is
// byte-stable. The bus driver+store serve a trace keyed by the injected run id so the
// scenario passes GREEN, its stdout is captured to a buffer, normalized, and compared to
// the committed golden. Regenerate with MENTAT_UPDATE_GOLDEN=1 only when a stdout change
// is intended. Serial by convention (mirrors the sibling Run tests) — Run composes a
// full engine.
func TestGoldenHermeticStdout(t *testing.T) {
	b := newBus()
	var buf bytes.Buffer
	cfg := mentat.Config{
		Store: goldenRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: goldenRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}

	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(goldenFeaturePath),
		mentat.WithConcurrency(1),
		mentat.WithOutput(&buf),
		mentat.WithDriver(goldenRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: goldenAnswer}, nil
		}),
		mentat.WithStore(goldenRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run returned a harness error (the hermetic golden run must be green): %v", err)
	}
	// The golden must reflect a PASSING run; guard the tally before trusting the bytes.
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("golden run is not green (passed=%d failed=%d); scenarios=%+v", res.Passed, res.Failed, res.Scenarios)
	}

	got := normalizeGoldenStdout(buf.Bytes())

	if os.Getenv(goldenUpdateEnv) == "1" {
		if err := os.WriteFile(goldenHermeticPath, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden %q: %v", goldenHermeticPath, err)
		}
		t.Logf("wrote golden %q (%d bytes)", goldenHermeticPath, len(got))
		return
	}

	want, err := os.ReadFile(goldenHermeticPath)
	if err != nil {
		t.Fatalf("read golden %q (regenerate with %s=1 only if this is a new/intended fixture): %v", goldenHermeticPath, goldenUpdateEnv, err)
	}
	if got != string(want) {
		t.Fatalf("hermetic stdout drifted from golden (regenerate %q with %s=1 only if this change was intended)\n--- want (golden) ---\n%q\n--- got (normalized) ---\n%q", goldenHermeticPath, goldenUpdateEnv, string(want), got)
	}
}
