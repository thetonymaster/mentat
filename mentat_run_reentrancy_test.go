// Package mentat_test — reentrancy & cancellation proofs for the library entry
// point (spec 007 US2, tasks T010/T011). These exercise mentat.Run through the
// PUBLIC facade only and assert the property the MVP could not yet promise: a Run
// owns its seam registrations, so runs neither leak into one another (sequential)
// nor race a shared registry (concurrent).
//
// The harness (bus/busDriver/busStore, echoTarget, writeFile) lives in
// mentat_run_test.go — same package.
package mentat_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat"
)

// greenMembusRun performs one fully-hermetic green run: a custom driver+store pair
// (registered under regName) writes and serves a trace keyed by the injected run
// id, so the default UUID correlator resolves it and the scenario passes. answer is
// both the driver Output and the string the scenario asserts, so a run that somehow
// resolved a *different* run's driver would fail the "result contains" step — making
// cross-run contamination observable, not silent.
func greenMembusRun(t *testing.T, ctx context.Context, regName, answer string) (mentat.Results, error) {
	t.Helper()
	b := newBus()
	dir := t.TempDir()
	feature := fmt.Sprintf(`Feature: reentrant custom driver and store
  Scenario: a custom driver writes a trace a custom store serves
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %q
`, answer)
	featPath := writeFile(t, dir, "membus.feature", feature)

	cfg := mentat.Config{
		Store: regName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: regName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	return mentat.Run(ctx, cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver(regName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: answer}, nil
		}),
		mentat.WithStore(regName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
}

func assertGreen(t *testing.T, label string, res mentat.Results, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: Run returned a harness error (a green custom run must not): %v", label, err)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("%s: tally passed=%d failed=%d, want 1/0; scenarios=%+v", label, res.Passed, res.Failed, res.Scenarios)
	}
	if len(res.Scenarios) != 1 || !res.Scenarios[0].Pass {
		t.Fatalf("%s: scenario did not pass green: %+v", label, res.Scenarios)
	}
}

// TestRunSequentialReuseSameName proves US2 acceptance #2: a custom registration
// does NOT leak into a subsequent Run. Two sequential Runs reuse the SAME custom
// name ("membus"); each must compose and go green independently. On the pre-fix
// package-global registry the first Run's registration persists, so the second Run
// sees "membus" already registered and fails with a false collision — the red this
// test pins.
func TestRunSequentialReuseSameName(t *testing.T) {
	ctx := context.Background()
	res1, err1 := greenMembusRun(t, ctx, "membus", "membus one")
	assertGreen(t, "first run", res1, err1)

	res2, err2 := greenMembusRun(t, ctx, "membus", "membus two")
	assertGreen(t, "second run (same custom name)", res2, err2)
}

// TestRunConcurrentIndependent proves US2 acceptance: two Runs execute concurrently
// without sharing registration state. Each goroutine registers the same custom name
// against its OWN bus and asserts its OWN answer, so a shared driver/store instance
// (pre-fix, last-writer-wins in the global map) surfaces as a wrong-answer failure —
// and the concurrent Reopen/register/Seal/read toggles trip the race detector. The
// test's teeth are under `go test -race`.
func TestRunConcurrentIndependent(t *testing.T) {
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]mentat.Results, 2)
	errs := make([]error, 2)
	answers := []string{"concurrent A", "concurrent B"}
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = greenMembusRun(t, ctx, "membus", answers[i])
		}()
	}
	wg.Wait()
	for i := range answers {
		assertGreen(t, fmt.Sprintf("concurrent run %d (%q)", i, answers[i]), results[i], errs[i])
	}
}

// cancelDriver cancels the run context from inside Run, simulating a mid-suite
// interruption (Ctrl-C / deadline). It writes no trace and returns the cancellation
// error, so the scenario cannot go green AND ctx.Err() is set when the suite returns.
type cancelDriver struct{ cancel context.CancelFunc }

func (d cancelDriver) Run(ctx context.Context, spec mentat.RunSpec) (mentat.RunResult, error) {
	d.cancel()
	return mentat.RunResult{RunID: spec.RunID}, ctx.Err()
}

// TestRunCancellationSetsInterrupted proves ctx cancellation mid-suite surfaces as
// Results.Interrupted (feature-003 semantics, T010) — NOT as a Run harness error. A
// driver cancels the run context; the scenario cannot pass and Run reports the
// interruption structurally in Results.
func TestRunCancellationSetsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	feature := `Feature: cancellation
  Scenario: a cancelled run reports interrupted
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "never"
`
	featPath := writeFile(t, dir, "cancel.feature", feature)
	cfg := mentat.Config{
		Store: "membus",
		Targets: map[string]mentat.Target{
			"bot": {Adapter: "membus", Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	b := newBus()
	res, err := mentat.Run(ctx, cfg,
		mentat.WithFeatures(featPath),
		mentat.WithDriver("membus", func(mentat.Config) (mentat.Driver, error) {
			return cancelDriver{cancel: cancel}, nil
		}),
		mentat.WithStore("membus", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
	if err != nil {
		t.Fatalf("cancellation is not a harness error; Run must reflect it in Results, not err: %v", err)
	}
	if !res.Interrupted {
		t.Fatalf("want Results.Interrupted=true after ctx cancellation, got %+v", res)
	}
	if res.Passed != 0 {
		t.Fatalf("a cancelled scenario must not pass; got passed=%d", res.Passed)
	}
}
