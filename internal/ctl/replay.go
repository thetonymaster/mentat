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
	}
	suite := godog.TestSuite{ScenarioInitializer: steps.Initializer(eng), Options: &opts}
	if status := suite.Run(); status != 0 {
		return fmt.Errorf("replay: feature failed against run %s (status %d)", runID, status)
	}
	return nil
}
