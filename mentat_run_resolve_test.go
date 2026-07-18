package mentat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat"
)

// resolveFeature is a minimal, well-formed suite: it drives target "bot" and then
// asserts a budget. It exists so an un-resolved Config gets all the way to godog
// and DRIVES the SUT — which is exactly what the sentinel below detects.
const resolveFeature = `Feature: resolve
  Scenario: s
    Given the agent target "bot"
    When I run scenario "x"
    Then total tokens are under 5000
`

// sentinelTarget is a shell SUT that writes a sentinel file when driven, so a test
// can prove the SUT was NOT driven (config resolution aborted the run first)
// rather than merely that some error came back.
func sentinelTarget(sentinel string, mutate func(*mentat.Target)) map[string]mentat.Target {
	t := mentat.Target{
		Adapter:        "shell",
		Command:        []string{"sh", "-c", "echo hi > " + sentinel},
		MaxConcurrency: 1,
	}
	if mutate != nil {
		mutate(&t)
	}
	return map[string]mentat.Target{"bot": t}
}

// TestRunResolvesConfigBeforeDriving proves mentat.Run applies config.Resolve to a
// CODE-BUILT Config before any composition call, so the library path inherits every
// hard error the YAML path raises at Load (contracts/config-resolve.md, Law 4;
// FR-008..FR-010). Each row is a Resolve-only rule that neither BuildStore,
// BuildCorrelator nor Build re-checks: without resolution the run either fails with
// an unrelated downstream message (storePath) or silently proceeds to drive the SUT
// with a config that should never have been accepted (completeness/pricing).
//
// The sentinel is the crux of "before driving anything": a Resolve failure must
// abort before the shell command ever runs.
func TestRunResolvesConfigBeforeDriving(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// targets receives the sentinel path so every row drives the same SUT.
		cfg      func(dir, sentinel string) mentat.Config
		wantSubs []string
	}{
		{
			// inventory #2 — file store requires storePath. Unresolved, this surfaces
			// as a raw os.ReadDir("") failure from the file store instead of the
			// descriptive, field-named error a YAML author gets.
			name: "file store without storePath",
			cfg: func(dir, sentinel string) mentat.Config {
				return mentat.Config{Store: "file", Targets: sentinelTarget(sentinel, nil)}
			},
			wantSubs: []string{"resolving config", `storePath is required when store is "file"`},
		},
		{
			// inventory #10 — completeness mode is validated only in Resolve.
			name: "unknown completeness mode",
			cfg: func(dir, sentinel string) mentat.Config {
				return mentat.Config{Store: "file", StorePath: dir, Targets: sentinelTarget(sentinel, func(tg *mentat.Target) {
					tg.Completeness = mentat.Completeness{Mode: "bogus"}
				})}
			},
			wantSubs: []string{"resolving config", `target "bot"`, `completeness.mode must be "settle" or "strict"`},
		},
		{
			// inventory #11 — validatePricing is Resolve-only; the engine converts
			// pricing without re-checking it, so a negative rate would reach costSum.
			name: "negative pricing rate",
			cfg: func(dir, sentinel string) mentat.Config {
				return mentat.Config{
					Store:     "file",
					StorePath: dir,
					Targets:   sentinelTarget(sentinel, nil),
					Pricing:   mentat.Pricing{"m": {InputPerMTok: -1}},
				}
			},
			wantSubs: []string{"resolving config", `pricing "m"`, "must be finite and >= 0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			featPath := writeFile(t, dir, "resolve.feature", resolveFeature)
			sentinel := filepath.Join(dir, "drove.txt")

			res, err := mentat.Run(context.Background(), tt.cfg(dir, sentinel), mentat.WithFeatures(featPath))
			if err == nil {
				t.Fatalf("Run with an unresolvable config must error, got results %+v", res)
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error should contain %q, got %q", sub, err.Error())
				}
			}
			if len(res.Scenarios) != 0 || res.Passed != 0 || res.Failed != 0 {
				t.Fatalf("a resolution failure must return empty Results, got %+v", res)
			}
			if _, statErr := os.Stat(sentinel); statErr == nil {
				t.Fatalf("SUT was driven despite an unresolvable config (sentinel %q exists)", sentinel)
			}
		})
	}
}
