// Package mentat_test — SC-001 through the FACADE.
//
// Every other test for feature 011 builds its engine with engine.WithExtraComparator,
// an internal package no external module can import. That proves the machinery works;
// it does not prove the feature's headline claim, which is that a comparator registered
// the way an actual consumer registers one — mentat.WithComparator — can be named from
// a .feature file.
//
// This file closes that gap. It imports the facade and nothing else, exactly as
// examples/kafkaecho does, and drives a real mentat.Run hermetically over the bus
// driver+store harness from mentat_run_test.go.
package mentat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat"
)

// facadeRevenueExpectation is the comparator's own expectation type, declared in the
// consumer's package. Mentat never names it.
type facadeRevenueExpectation struct {
	Min      int    `json:"min"`
	Currency string `json:"currency"`
}

// facadeRevenue is a comparator written entirely against the facade — the only surface
// an external module has. It opts into Gherkin invocation by implementing
// mentat.ExpectationParser.
type facadeRevenue struct{ compared []mentat.Expectation }

func (c *facadeRevenue) Name() string { return "revenue-shape" }

func (c *facadeRevenue) ParseExpectation(text string) (mentat.Expectation, error) {
	var exp facadeRevenueExpectation
	if err := json.Unmarshal([]byte(text), &exp); err != nil {
		return nil, fmt.Errorf("revenue-shape: parsing %q: %w", text, err)
	}
	return exp, nil
}

func (c *facadeRevenue) Compare(_ context.Context, _ mentat.Evidence, e mentat.Expectation) (mentat.Verdict, error) {
	c.compared = append(c.compared, e)
	exp, ok := e.(facadeRevenueExpectation)
	if !ok {
		return mentat.Verdict{}, fmt.Errorf("revenue-shape: expected facadeRevenueExpectation, got %T", e)
	}
	if exp.Min > 10 {
		return mentat.Verdict{Pass: false, Reasons: []string{
			fmt.Sprintf("floor %d %s is unreachable", exp.Min, exp.Currency),
		}}, nil
	}
	return mentat.Verdict{Pass: true}, nil
}

// Compile-time witness that the facade alias alone is sufficient to implement the
// seam. If ExpectationParser were declared somewhere an external module cannot name,
// this file would not build — which is the failure mode the nameability sweep cannot
// catch, since it only walks what IS published.
var (
	_ mentat.Comparator        = (*facadeRevenue)(nil)
	_ mentat.ExpectationParser = (*facadeRevenue)(nil)
)

const facadeComparatorRegistryName = "facade-cmp"

// writeFacadeFeature writes the inline feature to a temp dir, since WithFeatures takes
// paths rather than contents.
func writeFacadeFeature(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "facade-comparator.feature")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	return path
}

// runFacadeComparator drives one scenario through mentat.Run with cmp registered via
// the public WithComparator hook, returning the results and captured stdout.
func runFacadeComparator(t *testing.T, cmp mentat.Comparator, body string) (mentat.Results, string, error) {
	t.Helper()
	b := newBus()
	var buf bytes.Buffer
	cfg := mentat.Config{
		Store: facadeComparatorRegistryName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: facadeComparatorRegistryName, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}
	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures(writeFacadeFeature(t, body)),
		mentat.WithConcurrency(1),
		mentat.WithOutput(&buf),
		mentat.WithDriver(facadeComparatorRegistryName, func(mentat.Config) (mentat.Driver, error) {
			return busDriver{bus: b, answer: "ok"}, nil
		}),
		mentat.WithStore(facadeComparatorRegistryName, func(mentat.Config) (mentat.TraceStore, error) {
			return busStore{bus: b}, nil
		}),
		// The line this whole feature exists for: the real external registration path.
		mentat.WithComparator("revenue-shape", func(mentat.Config) (mentat.Comparator, error) {
			return cmp, nil
		}),
	)
	return res, buf.String(), err
}

// TestFacadeRegisteredComparatorRunsFromFeatureFile is SC-001: a comparator registered
// through mentat.WithComparator and implementing mentat.ExpectationParser is named from
// a .feature file, receives the expectation its own parser produced, and its verdict
// lands as a normal scenario result.
func TestFacadeRegisteredComparatorRunsFromFeatureFile(t *testing.T) {
	cmp := &facadeRevenue{}
	res, out, err := runFacadeComparator(t, cmp, `Feature: facade comparator
  Scenario: a facade-registered comparator is driven from Gherkin
    Given the agent target "bot"
    When I run scenario "any"
    Then the "revenue-shape" comparator is satisfied by:
      """
      {"min": 4, "currency": "USD"}
      """
`)
	if err != nil {
		t.Fatalf("Run returned a harness error: %v\n%s", err, out)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("expected one passing scenario, got passed=%d failed=%d\n%s", res.Passed, res.Failed, out)
	}
	if len(cmp.compared) != 1 {
		t.Fatalf("Compare called %d time(s), want 1 — the step never reached the comparator\n%s", len(cmp.compared), out)
	}
	want := facadeRevenueExpectation{Min: 4, Currency: "USD"}
	if got := cmp.compared[0]; got != mentat.Expectation(want) {
		t.Fatalf("Compare received %#v, want %#v", got, want)
	}
}

// TestFacadeRegisteredComparatorGoesRed is the same path proven to FAIL correctly. A
// green-only facade proof would not distinguish "the comparator ran" from "the step was
// skipped", since a skipped assertion also yields a passing scenario.
func TestFacadeRegisteredComparatorGoesRed(t *testing.T) {
	cmp := &facadeRevenue{}
	res, out, err := runFacadeComparator(t, cmp, `Feature: facade comparator
  Scenario: a failing facade-registered comparator fails the scenario
    Given the agent target "bot"
    When I run scenario "any"
    Then the "revenue-shape" comparator is satisfied by:
      """
      {"min": 99, "currency": "USD"}
      """
`)
	if err != nil {
		t.Fatalf("Run returned a harness error: %v\n%s", err, out)
	}
	if res.Failed != 1 || res.Passed != 0 {
		t.Fatalf("expected one FAILING scenario, got passed=%d failed=%d\n%s", res.Passed, res.Failed, out)
	}
	if !strings.Contains(out, "floor 99 USD is unreachable") {
		t.Fatalf("stdout must carry the comparator's own reason, got:\n%s", out)
	}
}
