package mentat_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thetonymaster/mentat"
)

const strictRegistryName = "strict-probe"

// TestUndefinedStepFailsTheRun pins the contract that a step matching no registered
// pattern FAILS the run.
//
// This passed the first time it was written, and is kept as a regression guard rather
// than presented as a fix. It is worth recording WHY it passes, because the reasoning
// that predicted otherwise was wrong in an instructive way.
//
// godog's default is non-strict: an undefined step is printed and the SUITE STATUS is
// still 0. run.go discards that status entirely (`_ = suite.Run()`) and derives
// Results from the collector — which looked like it should compound the problem. It
// does the opposite. godog passes the After hook "step is undefined: <text>" as
// stepErr, the hook is verdict-authoritative (`Pass: stepErr == nil`), and the
// scenario is recorded as FAILED. The collector path is what makes mentat.Run correct
// here, not what breaks it.
//
// The same gap WAS real in ctl.ReplayFeature, which derived its verdict from the suite
// status directly; that one is fixed with Strict and covered by
// TestReplayFeatureFailsOnUndefinedStep.
//
// The guard matters because the behaviour is emergent from godog's hook contract
// rather than asserted anywhere in mentat: a godog upgrade that stopped passing
// ErrUndefined to the After hook would silently turn every mistyped step green.
func TestUndefinedStepFailsTheRun(t *testing.T) {
	b := newBus()
	var buf bytes.Buffer

	path := filepath.Join(t.TempDir(), "undefined.feature")
	body := `Feature: undefined step
  Scenario: a step that matches no registered pattern
    Given the agent target "bot"
    When I run scenario "any"
    Then the moon is made of green cheese
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	cfg := mentat.Config{
		Store: strictRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: strictRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(path),
		mentat.WithConcurrency(1),
		mentat.WithOutput(&buf),
		mentat.WithDriver(strictRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(strictRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Run returned a harness error: %v\n%s", err, buf.String())
	}

	if res.Passed != 0 {
		t.Errorf("Results report %d passing scenario(s); a scenario whose assertion never ran must not count as passed\n%s", res.Passed, buf.String())
	}
	if res.Failed != 1 {
		t.Errorf("Results report failed=%d, want 1: an undefined step must fail the run\n%s", res.Failed, buf.String())
	}
}
