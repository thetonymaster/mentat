package mentat_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat"
)

// Feature 010 US1: a caller-supplied Reporter is registrable and is invoked with the
// run's results. Before this feature the seam could not even be DECLARED from outside
// the module (its parameter type had no facade name), and there was nowhere to plug one
// in if it could.
//
// These tests live in package mentat_test and import only the facade, so they exercise
// exactly what an extension author can reach.

// recordingReporter captures what it was handed, so a test can assert the reporter saw
// the run's real outcome rather than merely that a file appeared.
type recordingReporter struct {
	mu       sync.Mutex
	calls    int
	seen     mentat.Results
	failWith error
}

func (r *recordingReporter) Report(res mentat.Results, w io.Writer) error {
	r.mu.Lock()
	r.calls++
	r.seen = res
	r.mu.Unlock()
	if r.failWith != nil {
		return r.failWith
	}
	_, err := fmt.Fprintf(w, "custom: %d/%d passed\n", res.Passed, res.Total)
	return err
}

func (r *recordingReporter) snapshot() (int, mentat.Results) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.seen
}

// greenRun wires the shared bus driver/store fixture into a one-scenario green suite and
// returns the config plus the options that make it run.
func greenRun(t *testing.T, name string) (mentat.Config, []mentat.Option) {
	t.Helper()
	b := newBus()
	dir := t.TempDir()
	feature := "Feature: " + name + `
  Scenario: a scenario the reporter will render
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "membus ok"
`
	featPath := writeFile(t, dir, "reporter.feature", feature)
	cfg := mentat.Config{
		Store:   "membus",
		Targets: map[string]mentat.Target{"bot": {Adapter: "membus", Command: []string{"noop"}, MaxConcurrency: 1}},
		Poll:    mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	opts := []mentat.Option{
		mentat.WithFeatures(featPath),
		mentat.WithDriver("membus", func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "membus ok"}, nil
		}),
		mentat.WithStore("membus", func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	}
	return cfg, opts
}

// TestCustomReporter is US1 acceptance 2 end to end: declare, register, select, run,
// receive output — using facade names only.
func TestCustomReporter(t *testing.T) {
	t.Parallel()

	rep := &recordingReporter{}
	out := filepath.Join(t.TempDir(), "custom.txt")
	cfg, opts := greenRun(t, "custom reporter")
	opts = append(opts,
		mentat.WithReporter("dashboard", func(mentat.Config) (mentat.Reporter, error) { return rep, nil }),
		mentat.WithReports(map[string]string{"dashboard": out}),
	)

	res, err := mentat.Run(context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("Run returned a harness error: %v", err)
	}
	calls, seen := rep.snapshot()
	if calls != 1 {
		t.Fatalf("custom reporter invoked %d times, want exactly 1", calls)
	}
	if seen.Passed != res.Passed || seen.Total != res.Total {
		t.Errorf("reporter saw passed=%d total=%d, but Run returned passed=%d total=%d — the reporter must receive THIS run's outcome",
			seen.Passed, seen.Total, res.Passed, res.Total)
	}
	if seen.Total == 0 {
		t.Error("reporter saw Total=0; the suite ran one scenario")
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("custom reporter produced no output file: %v", err)
	}
	if !strings.Contains(string(body), "custom: 1/1 passed") {
		t.Errorf("custom reporter output = %q, want it to contain %q", body, "custom: 1/1 passed")
	}
}

// TestCustomReporterSeesFullFidelity is SC-008: a custom reporter reaches every datum a
// built-in one renders. Before 010 the facade's Results was lossy against the report by
// eight data points, so this assertion was unwritable.
func TestCustomReporterSeesFullFidelity(t *testing.T) {
	t.Parallel()

	rep := &recordingReporter{}
	cfg, opts := greenRun(t, "fidelity")
	opts = append(opts,
		mentat.WithReporter("fidelity", func(mentat.Config) (mentat.Reporter, error) { return rep, nil }),
		mentat.WithReports(map[string]string{"fidelity": filepath.Join(t.TempDir(), "f.txt")}),
	)
	if _, err := mentat.Run(context.Background(), cfg, opts...); err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, seen := rep.snapshot()
	// Suite-level data the old facade Results could not carry.
	if seen.Total == 0 {
		t.Error("Results.Total unreachable (was absent from the facade before 010)")
	}
	if seen.StartedAt.IsZero() {
		t.Error("Results.StartedAt unreachable (was absent from the facade before 010)")
	}
	if seen.Duration == 0 {
		t.Error("Results.Duration unreachable (was absent from the facade before 010)")
	}
	if len(seen.Scenarios) == 0 {
		t.Fatal("no scenarios reached the reporter")
	}
	// Per-scenario data likewise. Runs is the per-run table the built-in HTML reporter
	// renders; a custom reporter can now render the same thing.
	sc := seen.Scenarios[0]
	if len(sc.Runs) == 0 {
		t.Error("ScenarioResult.Runs unreachable (was absent from the facade before 010)")
	}
	if len(sc.RunIDs) != len(sc.Runs) {
		t.Errorf("RunIDs (%d) and Runs (%d) disagree", len(sc.RunIDs), len(sc.Runs))
	}
	// Tags/Sequence/Qualifiers/Aggregate are reachable as fields even when this
	// particular scenario leaves them empty — reachability is the property under test,
	// and a compile-time reference is what proves it.
	_, _, _, _ = sc.Tags, sc.Sequence, sc.Qualifiers, sc.Aggregate
}

// TestCustomReporterNameCollision is US1 acceptance 3: a name already taken by a built-in
// fails loudly at build, never last-wins.
func TestCustomReporterNameCollision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		regName string
	}{
		{name: "collides with built-in json", regName: "json"},
		{name: "collides with built-in html", regName: "html"},
		{name: "collides with built-in junit", regName: "junit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, opts := greenRun(t, "collision")
			opts = append(opts, mentat.WithReporter(tt.regName, func(mentat.Config) (mentat.Reporter, error) {
				return &recordingReporter{}, nil
			}))
			_, err := mentat.Run(context.Background(), cfg, opts...)
			if err == nil {
				t.Fatalf("registering %q silently succeeded; a collision must be a loud error", tt.regName)
			}
			if !strings.Contains(err.Error(), tt.regName) {
				t.Errorf("collision error = %v, want it to name the conflicting reporter %q", err, tt.regName)
			}
		})
	}
}

// TestCustomReporterRenderErrorSurfaces is US1 acceptance 4: a reporter that fails while
// rendering surfaces its failure WITH the reporter named, and the suite's own Results are
// still returned. A rendering failure is not a suite failure.
func TestCustomReporterRenderErrorSurfaces(t *testing.T) {
	t.Parallel()

	boom := errors.New("dashboard backend unreachable")
	rep := &recordingReporter{failWith: boom}
	cfg, opts := greenRun(t, "render error")
	opts = append(opts,
		mentat.WithReporter("dashboard", func(mentat.Config) (mentat.Reporter, error) { return rep, nil }),
		mentat.WithReports(map[string]string{"dashboard": filepath.Join(t.TempDir(), "d.txt")}),
	)

	res, err := mentat.Run(context.Background(), cfg, opts...)
	if err == nil {
		t.Fatal("a failing reporter produced no error; emission failures must not be swallowed")
	}
	if !strings.Contains(err.Error(), "dashboard") {
		t.Errorf("error = %v, want it to name the failing reporter %q", err, "dashboard")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap the reporter's own error", err)
	}
	// The run happened; only emission failed.
	if res.Passed != 1 || res.Failed != 0 {
		t.Errorf("Results after a reporter failure = passed %d failed %d, want 1/0 — the suite still ran",
			res.Passed, res.Failed)
	}
}

// TestWithReportsUnknownReporterErrors characterizes existing behaviour through the
// refactor: naming a reporter nothing registered is a loud error that names it, never a
// silently skipped file. EmitReports already did this; 010 additionally makes the message
// list what IS registered.
func TestWithReportsUnknownReporterErrors(t *testing.T) {
	t.Parallel()

	cfg, opts := greenRun(t, "unknown reporter")
	opts = append(opts, mentat.WithReports(map[string]string{"nope": filepath.Join(t.TempDir(), "x.txt")}))

	_, err := mentat.Run(context.Background(), cfg, opts...)
	if err == nil {
		t.Fatal("an unknown reporter name was silently ignored; it must be a loud error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v, want it to name the unknown reporter %q", err, "nope")
	}
	for _, builtin := range []string{"json", "html", "junit"} {
		if !strings.Contains(err.Error(), builtin) {
			t.Errorf("error = %v, want it to list the registered reporter %q so the author can fix the typo", err, builtin)
		}
	}
}

// TestConcurrentReporterRegistration is SC-010, and the property that forced D4. Two
// genuinely concurrent Runs register DIFFERENT reporters under the SAME name; each must
// use its own. The package-global map this replaced could not provide that — one run
// would have overwritten the other's registration, and no seal would have caught it.
//
// Run under -race; without it this can pass while broken.
func TestConcurrentReporterRegistration(t *testing.T) {
	t.Parallel()

	const shared = "same-name"
	var wg sync.WaitGroup
	type outcome struct {
		calls int
		body  string
		err   error
	}
	results := make([]outcome, 2)

	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rep := &recordingReporter{}
			out := filepath.Join(t.TempDir(), fmt.Sprintf("run-%d.txt", i))
			cfg, opts := greenRun(t, fmt.Sprintf("concurrent %d", i))
			opts = append(opts,
				// Same NAME, different instance, in each run.
				mentat.WithReporter(shared, func(mentat.Config) (mentat.Reporter, error) { return rep, nil }),
				mentat.WithReports(map[string]string{shared: out}),
			)
			if _, err := mentat.Run(context.Background(), cfg, opts...); err != nil {
				results[i] = outcome{err: err}
				return
			}
			calls, _ := rep.snapshot()
			body, err := os.ReadFile(out)
			results[i] = outcome{calls: calls, body: string(body), err: err}
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got.err != nil {
			t.Fatalf("run %d: %v", i, got.err)
		}
		if got.calls != 1 {
			t.Errorf("run %d invoked ITS OWN reporter %d times, want 1 — the runs shared reporter state", i, got.calls)
		}
		if !strings.Contains(got.body, "custom: 1/1 passed") {
			t.Errorf("run %d output = %q, want its own reporter's output", i, got.body)
		}
	}
}
