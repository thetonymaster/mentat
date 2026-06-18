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
func ReplayFeature(ctx context.Context, eng *engine.Engine, runID, featurePath, scenario string, w io.Writer) error {
	eng.PinRun(runID)
	opts := godog.Options{
		Format: "pretty",
		Paths:  []string{featurePath},
		Output: w,
		Tags:   scenario, // empty = all scenarios in the file
	}
	suite := godog.TestSuite{ScenarioInitializer: steps.Initializer(eng), Options: &opts}
	if status := suite.Run(); status != 0 {
		return fmt.Errorf("replay: feature failed against run %s (status %d)", runID, status)
	}
	return nil
}
