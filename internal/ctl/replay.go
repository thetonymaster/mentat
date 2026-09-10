package ctl

import (
	"context"
	"fmt"
	"io"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/steps"
)

// ReplayFeature re-evaluates a feature against a STORED run (no driving). It pins the
// engine to runID, then runs the feature through the same godog step grammar.
//
// tagExpr is a godog tag expression (e.g. "@wip"); empty runs all scenarios in the feature.
func ReplayFeature(ctx context.Context, eng *engine.Engine, runID, featurePath, tagExpr string, w io.Writer) error {
	if runID == "" {
		return fmt.Errorf("replay: run id is required")
	}
	eng.PinRun(runID)
	opts := godog.Options{
		Format:         "pretty",
		Paths:          []string{featurePath},
		Output:         w,
		Tags:           tagExpr, // empty = all scenarios in the file
		DefaultContext: ctx,
		// Strict, because this function derives its verdict from the suite's exit
		// status. Godog's default is non-strict, where an UNDEFINED or PENDING step is
		// printed and the suite still exits 0 — so a mistyped step reported a
		// successful replay while the assertion never ran (Constitution IV).
		//
		// mentat.Run does not need this: it discards the suite status and derives
		// Results from the collector, whose After hook receives "step is undefined: …"
		// as stepErr and records the scenario as failed.
		Strict: true,
	}
	suite := godog.TestSuite{ScenarioInitializer: steps.Initializer(eng), Options: &opts}
	if status := suite.Run(); status != 0 {
		return fmt.Errorf("replay: feature failed against run %s (status %d)", runID, status)
	}
	return nil
}
