// Package mentat_test — caller-Config isolation proofs for the library entry point.
//
// mentat.Run takes Config BY VALUE, but Config.Targets is a map, so the map header
// copied into Run still points at the CALLER's backing store. config.Resolve writes
// resolved Targets back (`c.Targets[name] = t`, internal/config/config.go:325), so
// once Resolve was wired into Run (spec 009 T014) a Run silently mutated its caller's
// Config and two concurrent Runs sharing one Config raced on that map. Both regress
// the spec-007 T010/T011 guarantee that Run is reentrant and concurrency-safe.
//
// The harness (bus/busDriver/busStore, writeFile) lives in mentat_run_test.go —
// same package.
package mentat_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat"
)

// snapshotTargets copies the Targets map one level deep: fresh map, Target values
// copied by value. That is exactly deep enough to detect Resolve's writes, which
// replace whole Target values (MaxConcurrency / Budget / Completeness / Extract),
// and reflect.DeepEqual compares unexported fields too (Extract.compiled).
func snapshotTargets(m map[string]mentat.Target) map[string]mentat.Target {
	out := make(map[string]mentat.Target, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// TestRunDoesNotMutateCallerConfig proves Run leaves the caller's Config untouched.
// Each row's target leaves MaxConcurrency, Budget and Completeness at their ZERO
// values — precisely the fields Resolve fills in (0 → 1, {} → {5m,10s}, {} →
// {settle,…}) — so any write-through is visible as a concrete diff, not a subtle one.
// The error row proves the isolation holds on Run's failure path too: Resolve mutates
// the target loop BEFORE validateJudge rejects the even vote count, so a copy made
// only on success would still leak.
func TestRunDoesNotMutateCallerConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		judge   mentat.JudgeConfig
		wantErr bool
	}{
		{
			name: "config Run accepts",
		},
		{
			name:    "config Resolve rejects after the target loop",
			judge:   mentat.JudgeConfig{Votes: 2},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			featPath := writeFile(t, dir, "isolation.feature", `Feature: caller config isolation
  Scenario: drives a target
    Given the agent target "bot"
    When I run scenario "happy"
    Then total tokens are under 5000
`)
			cfg := mentat.Config{
				Store:     "file",
				StorePath: dir,
				Targets: map[string]mentat.Target{
					// Zero MaxConcurrency / Budget / Completeness: every knob Resolve fills.
					"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}},
				},
				Judge: tt.judge,
			}
			before := snapshotTargets(cfg.Targets)

			_, err := mentat.Run(context.Background(), cfg, mentat.WithFeatures(featPath))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Run err=%v, wantErr=%v", err, tt.wantErr)
			}

			if !reflect.DeepEqual(cfg.Targets, before) {
				t.Errorf("Run mutated the caller's cfg.Targets\n before: %+v\n  after: %+v", before, cfg.Targets)
			}
			got := cfg.Targets["bot"]
			if got.MaxConcurrency != 0 {
				t.Errorf("caller's Target.MaxConcurrency = %d, want 0 (untouched)", got.MaxConcurrency)
			}
			if got.Budget != (mentat.RunBudget{}) {
				t.Errorf("caller's Target.Budget = %+v, want zero (untouched)", got.Budget)
			}
			if got.Completeness != (mentat.Completeness{}) {
				t.Errorf("caller's Target.Completeness = %+v, want zero (untouched)", got.Completeness)
			}
		})
	}
}

// TestRunConcurrentSharedConfigNoRace shares ONE Config across two concurrent Runs.
// TestRunConcurrentIndependent cannot catch this: each of its goroutines builds its
// own Config literal, so each gets its own Targets map. Here both Runs resolve the
// SAME map, so a Run that writes through to the caller's Targets is a data race —
// this test's teeth are under `go test -race`.
func TestRunConcurrentSharedConfigNoRace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// One shared Config, zero-valued knobs so Resolve has writes to make.
	cfg := mentat.Config{
		Store: "membus",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "membus", Command: []string{"noop"}},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	before := snapshotTargets(cfg.Targets)

	answers := []string{"shared cfg A", "shared cfg B"}
	results := make([]mentat.Results, len(answers))
	errs := make([]error, len(answers))
	var wg sync.WaitGroup
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := newBus()
			dir := t.TempDir()
			featPath := writeFile(t, dir, "shared.feature", fmt.Sprintf(`Feature: shared caller config
  Scenario: a custom driver writes a trace a custom store serves
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %q
`, answers[i]))
			results[i], errs[i] = mentat.Run(ctx, cfg,
				mentat.WithFeatures(featPath),
				mentat.WithDriver("membus", func(mentat.Config) (mentat.Driver, error) {
					return busDriver{bus: b, answer: answers[i]}, nil
				}),
				mentat.WithStore("membus", func(mentat.Config) (mentat.TraceStore, error) {
					return busStore{bus: b}, nil
				}),
			)
		}()
	}
	wg.Wait()

	for i := range answers {
		assertGreen(t, fmt.Sprintf("shared-config run %d (%q)", i, answers[i]), results[i], errs[i])
	}
	if !reflect.DeepEqual(cfg.Targets, before) {
		t.Errorf("concurrent Runs mutated the shared caller cfg.Targets\n before: %+v\n  after: %+v", before, cfg.Targets)
	}
}
