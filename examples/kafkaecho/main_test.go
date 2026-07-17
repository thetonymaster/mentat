// Package kafkaecho_test drives the example extension through the PUBLIC mentat
// facade alone — no internal/... import — proving a third-party module can register
// a custom driver + store and run a feature green via mentat.Run (spec 007 US1;
// SC-001/SC-002). It is the living documentation for docs/extending/driver.md.
package kafkaecho_test

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat"
	"github.com/thetonymaster/mentat/examples/kafkaecho"
)

// TestKafkaEchoDrivesGreen registers the toy driver + store under their adapter
// names, points a target at the driver, and runs the echo feature through
// mentat.Run. The custom driver publishes a trace keyed by the injected run id and
// the custom store serves it back, so the scenario resolves and goes green — no
// built-in adapter, no Tempo, no network.
func TestKafkaEchoDrivesGreen(t *testing.T) {
	driver, store := kafkaecho.New()

	cfg := mentat.Config{
		Store: kafkaecho.StoreName,
		Targets: map[string]mentat.Target{
			"bot": {Adapter: kafkaecho.Adapter, Command: []string{"noop"}, MaxConcurrency: 1},
		},
		// A fast, short stability poll: the store's payload is byte-identical across
		// calls, so it converges immediately (StableFor: 1).
		Poll: mentat.PollSpec{Interval: "1ms", StableFor: 1},
	}

	res, err := mentat.Run(context.Background(), cfg,
		mentat.WithFeatures("testdata/echo.feature"),
		mentat.WithDriver(kafkaecho.Adapter, driver),
		mentat.WithStore(kafkaecho.StoreName, store),
	)
	if err != nil {
		t.Fatalf("mentat.Run returned a harness error (a custom driver+store green run must not error): %v", err)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Fatalf("tally passed=%d failed=%d, want 1/0; scenarios=%+v", res.Passed, res.Failed, res.Scenarios)
	}
	if len(res.Scenarios) != 1 || !res.Scenarios[0].Pass {
		t.Fatalf("the kafkaecho scenario did not pass green: %+v", res.Scenarios)
	}
	if len(res.Scenarios[0].RunIDs) == 0 || res.Scenarios[0].RunIDs[0] == "" {
		t.Fatalf("a green scenario must carry the injected run id, got %v", res.Scenarios[0].RunIDs)
	}
}
