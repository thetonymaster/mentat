// Package mentat_test — output/godog-knob option proofs and the Results ⇔ CLI
// exit-code contract for the library entry point (spec 007, T012 chain). These
// exercise mentat.Run through the PUBLIC facade only, growing the surface so the
// CLI ("consumer zero", research R7) can later ride mentat.Run.
//
// The bus/busDriver/busStore harness, echoTarget, newBus and writeFile live in
// mentat_run_test.go — same package.
package mentat_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat"
)

// runMembusFeature runs one hermetic membus suite: a custom driver+store pair
// (registered under regName) writes and serves a trace keyed by the injected run
// id, so the default UUID correlator resolves it. The caller supplies the whole
// feature text (so it controls scenario count, tags and which steps pass) and the
// single answer the driver returns for every run in that suite; extra options are
// appended verbatim, which is how each knob under test is plumbed in.
func runMembusFeature(t *testing.T, regName, answer, feature string, extra ...mentat.Option) (mentat.Results, error) {
	t.Helper()
	b := newBus()
	dir := t.TempDir()
	featPath := writeFile(t, dir, "opts.feature", feature)
	cfg := mentat.Config{
		Store: regName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: regName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	opts := append([]mentat.Option{
		mentat.WithFeatures(featPath),
		mentat.WithDriver(regName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: answer}, nil
		}),
		mentat.WithStore(regName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	}, extra...)
	return mentat.Run(context.Background(), cfg, opts...)
}

// TestResultsExitCode proves the Results ⇔ CLI exit-code contract (public-surface.md:
// "Results status ⇔ CLI exit semantics"): interrupted wins with 130, else any failure
// is 1, else 0 — the exact mapping cmd/mentat's main() uses so the CLI can ride
// mentat.Run as consumer zero.
func TestResultsExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  mentat.Results
		want int
	}{
		{name: "all passed", res: mentat.Results{Passed: 3}, want: 0},
		{name: "some failed", res: mentat.Results{Passed: 2, Failed: 1}, want: 1},
		{name: "interrupted only", res: mentat.Results{Passed: 1, Interrupted: true}, want: 130},
		{name: "interrupted wins over failure", res: mentat.Results{Failed: 2, Interrupted: true}, want: 130},
		{name: "empty is zero", res: mentat.Results{}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.res.ExitCode(); got != tt.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

// twoScenarioFeature builds a feature whose two scenarios both assert the driver's
// answer, so both pass under any concurrency. name1/name2 tag the scenarios so a
// caller can tell which ran.
func twoScenarioFeature(answer string) string {
	return fmt.Sprintf(`Feature: two scenarios
  Scenario: first
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %[1]q
  Scenario: second
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %[1]q
`, answer)
}

// TestWithConcurrency proves WithConcurrency is plumbed into godog: an unset/zero
// value runs at the default (1) and a value >1 runs a multi-scenario feature green
// (the pretty formatter is concurrency-wrapped by godog, so >1 must not break the
// run). Concurrency itself is not directly observable from Results, so the proof is
// that both scenarios still pass green at each level.
func TestWithConcurrency(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		regName     string
		concurrency int
		set         bool
	}{
		{name: "unset defaults to 1", regName: "conc-unset", set: false},
		{name: "explicit zero clamps to 1", regName: "conc-zero", concurrency: 0, set: true},
		{name: "two runs multi-scenario green", regName: "conc-two", concurrency: 2, set: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			answer := tt.regName + " ok"
			var extra []mentat.Option
			if tt.set {
				extra = append(extra, mentat.WithConcurrency(tt.concurrency))
			}
			res, err := runMembusFeature(t, tt.regName, answer, twoScenarioFeature(answer), extra...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Passed != 2 || res.Failed != 0 {
				t.Fatalf("want both scenarios green, got passed=%d failed=%d; scenarios=%+v", res.Passed, res.Failed, res.Scenarios)
			}
		})
	}
}

// TestWithTags proves WithTags is plumbed into godog's tag filter: a two-scenario
// feature with one @wanted scenario runs only the tagged one when WithTags("@wanted")
// is passed, and runs both when it is not. The proof is which scenarios land in
// Results (godog silently skips the filtered-out scenario — never reported).
func TestWithTags(t *testing.T) {
	t.Parallel()
	tagged := func(answer string) string {
		return fmt.Sprintf(`Feature: tag filter
  @wanted
  Scenario: wanted
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %[1]q
  Scenario: unwanted
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %[1]q
`, answer)
	}
	tests := []struct {
		name      string
		regName   string
		tags      string
		set       bool
		wantNames []string
	}{
		{name: "no tag filter runs both", regName: "tags-none", set: false, wantNames: []string{"wanted", "unwanted"}},
		{name: "tag filter runs only the wanted", regName: "tags-wanted", tags: "@wanted", set: true, wantNames: []string{"wanted"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			answer := tt.regName + " ok"
			var extra []mentat.Option
			if tt.set {
				extra = append(extra, mentat.WithTags(tt.tags))
			}
			res, err := runMembusFeature(t, tt.regName, answer, tagged(answer), extra...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(res.Scenarios))
			for _, sc := range res.Scenarios {
				got = append(got, sc.Name)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("ran scenarios %v, want %v", got, tt.wantNames)
			}
			want := map[string]bool{}
			for _, n := range tt.wantNames {
				want[n] = true
			}
			for _, n := range got {
				if !want[n] {
					t.Fatalf("unexpected scenario %q ran; want only %v", n, tt.wantNames)
				}
			}
		})
	}
}

// TestWithFailFast proves WithFailFast is plumbed into godog's StopOnFailure: on a
// two-scenario feature whose first scenario fails, WithFailFast(true) stops before
// the second scenario runs (only the failed one lands in Results), while the default
// runs both. Ordering is deterministic at the default concurrency (1), so the
// skipped scenario is exactly the second.
func TestWithFailFast(t *testing.T) {
	t.Parallel()
	// answer is what the driver returns; scenario "first fails" asserts a substring
	// the answer does NOT contain, so it fails; "second passes" asserts the answer.
	failFirst := func(answer string) string {
		return fmt.Sprintf(`Feature: fail fast
  Scenario: first fails
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains "NEVER-PRESENT"
  Scenario: second passes
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %q
`, answer)
	}
	tests := []struct {
		name       string
		regName    string
		failFast   bool
		set        bool
		wantCount  int
		wantPassed int
		wantFailed int
	}{
		{name: "default runs both", regName: "ff-off", set: false, wantCount: 2, wantPassed: 1, wantFailed: 1},
		{name: "fail fast stops after first failure", regName: "ff-on", failFast: true, set: true, wantCount: 1, wantPassed: 0, wantFailed: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			answer := tt.regName + " ok"
			var extra []mentat.Option
			if tt.set {
				extra = append(extra, mentat.WithFailFast(tt.failFast))
			}
			res, err := runMembusFeature(t, tt.regName, answer, failFirst(answer), extra...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(res.Scenarios) != tt.wantCount {
				names := make([]string, 0, len(res.Scenarios))
				for _, sc := range res.Scenarios {
					names = append(names, sc.Name)
				}
				t.Fatalf("ran %d scenarios %v, want %d", len(res.Scenarios), names, tt.wantCount)
			}
			if res.Passed != tt.wantPassed || res.Failed != tt.wantFailed {
				t.Fatalf("tally passed=%d failed=%d, want %d/%d", res.Passed, res.Failed, tt.wantPassed, tt.wantFailed)
			}
			if tt.failFast && res.Scenarios[0].Name != "first fails" {
				t.Fatalf("fail-fast must keep the failed scenario, got %q", res.Scenarios[0].Name)
			}
		})
	}
}

// TestWithOutput proves WithOutput narrates the godog pretty report to the given
// writer, and that the library default (no WithOutput) narrates nothing — the
// writer stays io.Discard, so a caller's buffer is untouched (library mode is
// silent by default).
func TestWithOutput(t *testing.T) {
	t.Parallel()
	const scenarioName = "narrates the run to the writer"
	tests := []struct {
		name       string
		regName    string
		withOutput bool
		wantEmpty  bool
	}{
		{name: "with output narrates scenario and summary", regName: "outbuf-on", withOutput: true, wantEmpty: false},
		{name: "default discards", regName: "outbuf-off", withOutput: false, wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			answer := tt.regName + " ok"
			feature := fmt.Sprintf(`Feature: output option
  Scenario: %s
    Given the agent target "bot"
    When I run scenario "echo"
    Then the result contains %q
`, scenarioName, answer)
			var extra []mentat.Option
			if tt.withOutput {
				extra = append(extra, mentat.WithOutput(&buf))
			}
			res, err := runMembusFeature(t, tt.regName, answer, feature, extra...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Passed != 1 || res.Failed != 0 {
				t.Fatalf("want a single green scenario, got %+v", res)
			}
			if tt.wantEmpty {
				if buf.Len() != 0 {
					t.Fatalf("default library mode must narrate nothing, got:\n%s", buf.String())
				}
				return
			}
			out := buf.String()
			if !strings.Contains(out, scenarioName) {
				t.Fatalf("output must contain the scenario name %q, got:\n%s", scenarioName, out)
			}
			if !strings.Contains(out, "1 scenarios") {
				t.Fatalf("output must contain the godog summary marker %q, got:\n%s", "1 scenarios", out)
			}
		})
	}
}
