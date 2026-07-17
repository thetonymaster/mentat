package steps

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/report"
	"github.com/thetonymaster/mentat/internal/store"
)

// replayFixture is a saved run's fixture (LoadFixture / ctl.WriteFixture format)
// whose recorded runScenario is "r": a root agent span carrying token usage and two
// ordered tool calls (search -> summarize). The file store keys on runScenario, so
// resolving run id "r" replays exactly this forest.
const replayFixture = `{
  "runScenario": "r",
  "spans": [
    {"name":"invoke_agent researchbot","parentIndex":-1,"status":"Ok","attrs":{"gen_ai.operation.name":"invoke_agent","gen_ai.usage.input_tokens":"1200","gen_ai.usage.output_tokens":"600"}},
    {"name":"execute_tool search","parentIndex":0,"status":"Ok","attrs":{"gen_ai.operation.name":"execute_tool","gen_ai.tool.name":"search"}},
    {"name":"execute_tool summarize","parentIndex":0,"status":"Ok","attrs":{"gen_ai.operation.name":"execute_tool","gen_ai.tool.name":"summarize"}}
  ]
}`

// TestFileStoreOfflineReplayRunsGreen is the US5 offline-replay proof (T020): a
// saved fixture suite runs GREEN through the whole steps -> engine -> correlator
// pipeline with NO Docker and NO network — trace resolution is served entirely from
// a directory-backed store.FileStore reading local files.
//
// It uses the LIVE (unpinned) drive path: the correlator injects a FIXED run id
// ("r"), the real correlator resolves it against the file store (Query -> Fetch ->
// Decode via LoadFixture), and the trace-derived comparators (tool order, token
// budget) plus the driver-output comparator all pass. Independence from a live
// backend is the point — the only I/O is the local echo SUT and the fixture read.
func TestFileStoreOfflineReplayRunsGreen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "run-r.json"), []byte(replayFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fs, err := store.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// Real correlator with a FIXED run-id generator: Inject stamps every run with "r",
	// which the file store's fixture records as its runScenario.
	cor := correlate.New(func() string { return "r" },
		correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: 2 * time.Second})

	cfg := config.Config{
		Store:     "file",
		StorePath: dir,
		Targets:   map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	eng, err := engine.Build(cfg, fs, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: offline replay
  Scenario: replays a saved run from the file store with no network
    Given the agent target "bot"
    When I run scenario "happy"
    Then the agent calls tools in order:
      | search    |
      | summarize |
    And the tool "delete_record" is never called
    And total tokens are under 5000
    And the result contains "hi"
`
	col := report.NewCollector()
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: InitializerWithCollector(eng, col),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "offline replay", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("offline replay suite must pass, status=%d\n%s", status, out.String())
	}

	rep := col.Report(time.Unix(0, 0), 0, false)
	if len(rep.Scenarios) != 1 {
		t.Fatalf("got %d scenarios, want 1", len(rep.Scenarios))
	}
	if !rep.Scenarios[0].Pass {
		t.Fatalf("scenario verdict not green: reasons=%v", rep.Scenarios[0].Reasons)
	}
	if rep.Passed != 1 || rep.Failed != 0 {
		t.Fatalf("report tally passed=%d failed=%d, want 1/0", rep.Passed, rep.Failed)
	}
}
