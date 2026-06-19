package steps

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// happyTrace: tools search->summarize, 1800 tokens.
func happyTrace() *trace.Trace {
	mk := func(op, tool string) *trace.Span {
		return &trace.Span{Name: op + " " + tool, Attrs: map[string]string{genai.Op: op, genai.ToolName: tool}}
	}
	root := &trace.Span{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "1200", genai.OutTokens: "600"}}
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: []*trace.Span{root, mk(genai.OpExecuteTool, "search"), mk(genai.OpExecuteTool, "summarize")}}
}

func TestFeatureExercisesGrammarAgainstFakeEngine(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(happyTrace(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: grammar
  Scenario: happy
    Given the agent target "bot"
    When I run scenario "happy"
    Then the agent calls tools in order:
      | search    |
      | summarize |
    And the tool "delete_record" is never called
    And total tokens are under 5000
    And the result contains "hi"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "grammar", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}
}

// TestFeatureGoesRedOnBadScenario proves that the godog layer itself reports a
// non-zero exit when a step's comparator returns Pass=false. The inline feature
// asserts "total tokens are under 1" against the 1800-token happyTrace, which
// must fail. This is the hermetic unit-level complement to the binary-level L3
// meta-test (Task 8).
func TestFeatureGoesRedOnBadScenario(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(happyTrace(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	// "total tokens are under 1" will fail: happyTrace has 1800 tokens.
	feature := `Feature: bad-budget
  Scenario: violates token budget
    Given the agent target "bot"
    When I run scenario "any"
    Then total tokens are under 1
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "bad-budget", Contents: []byte(feature)}},
		},
	}
	status := suite.Run()
	if status == 0 {
		t.Fatalf("expected suite to fail (non-zero), but it passed\n%s", out.String())
	}
	outStr := out.String()
	if !strings.Contains(outStr, "budgets failed") && !strings.Contains(outStr, "exceed budget") {
		t.Fatalf("expected output to contain failure reason (budgets failed / exceed budget), got:\n%s", outStr)
	}
}

// craftedTrace builds a trace with real timestamps and cost for unit-testing
// the step methods that the happy scenario doesn't exercise.
func craftedTrace() *trace.Trace {
	now := time.Now()
	root := &trace.Span{
		Name:  "invoke_agent",
		Start: now,
		End:   now.Add(50 * time.Millisecond),
		Attrs: map[string]string{
			genai.Op:        genai.OpInvokeAgent,
			genai.InTokens:  "100",
			genai.OutTokens: "50",
			genai.CostUSD:   "0.01",
		},
	}
	return &trace.Trace{
		Roots: []*trace.Span{root},
		Spans: []*trace.Span{root},
	}
}

// buildEng returns an engine wired to a mock store returning tr.
func buildEng(t *testing.T, tr *trace.Trace) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			"svc": {Adapter: "shell", Command: []string{"sh", "-c", "echo done"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// TestToolsInOrderEmptyCell proves that toolsInOrder returns a descriptive error
// (not a panic) when a table row has no cells, has a blank tool name, or the
// table itself is empty.
func TestToolsInOrderEmptyCell(t *testing.T) {
	tests := []struct {
		name      string
		tbl       *godog.Table
		wantErr   bool
		errSub    string
		errSubNot string // if non-empty, fail if err.Error() contains this substring
	}{
		{
			name: "well_formed_row_no_error",
			tbl: &godog.Table{
				Rows: []*messages.PickleTableRow{
					{Cells: []*messages.PickleTableCell{{Value: "search"}}},
				},
			},
			// toolsInOrder succeeds building the order slice; check() will fail
			// because no drive has been run, but the row-guard itself should not error.
			wantErr:   true,           // check("sequence",...) will error — no evidence; guards still exercised
			errSubNot: "has no cells", // a regression that fires the cell-guard on a valid row must be caught
		},
		{
			name: "empty_cells_slice_returns_error",
			tbl: &godog.Table{
				Rows: []*messages.PickleTableRow{
					{Cells: []*messages.PickleTableCell{}},
				},
			},
			wantErr: true,
			errSub:  "has no cells",
		},
		{
			name: "nil_cells_returns_error",
			tbl: &godog.Table{
				Rows: []*messages.PickleTableRow{
					{Cells: nil},
				},
			},
			wantErr: true,
			errSub:  "has no cells",
		},
		{
			name:    "empty_table_nil_rows",
			tbl:     &godog.Table{Rows: nil},
			wantErr: true,
			errSub:  "at least one tool is required",
		},
		{
			name:    "empty_table_no_rows",
			tbl:     &godog.Table{Rows: []*messages.PickleTableRow{}},
			wantErr: true,
			errSub:  "at least one tool is required",
		},
		{
			name: "blank_cell_whitespace",
			tbl: &godog.Table{
				Rows: []*messages.PickleTableRow{
					{Cells: []*messages.PickleTableCell{{Value: "   "}}},
				},
			},
			wantErr: true,
			errSub:  "empty tool name",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &world{} // no engine needed — error fires before check()
			err := w.toolsInOrder(tt.tbl)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if tt.errSub != "" && (err == nil || !strings.Contains(err.Error(), tt.errSub)) {
				t.Fatalf("expected error containing %q, got: %v", tt.errSub, err)
			}
			if tt.errSubNot != "" && err != nil && strings.Contains(err.Error(), tt.errSubNot) {
				t.Fatalf("error must NOT contain %q (regression: cell-guard fired on valid row), got: %v", tt.errSubNot, err)
			}
		})
	}
}

// TestCELStepPasses exercises the inline + docstring "the run satisfies" grammar
// against the fake engine. happyTrace has tools search/summarize and 1800 tokens;
// the shell target echoes "hi" so answer == 'hi'. Inline CEL uses single-quoted
// strings (the step regex forbids embedded double quotes); the docstring form
// may use double quotes freely.
func TestCELStepPasses(t *testing.T) {
	eng := buildEng(t, happyTrace())
	feature := `Feature: cel
  Scenario: satisfies inline and docstring
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "answer == 'hi' && tokens < 5000"
    And the run satisfies:
      """
      "search" in tools && "summarize" in tools
      """
`
	// buildEng's target runs `sh -c echo done`; override answer expectation:
	// the buildEng shell prints "done", so assert on that instead of "hi".
	feature = strings.Replace(feature, "answer == 'hi'", "answer == 'done'", 1)

	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "cel", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}
}

// TestCELStepGoesRedOnFalse proves the godog layer reports non-zero when a cel
// expression is false, and surfaces the §9 value snapshot.
func TestCELStepGoesRedOnFalse(t *testing.T) {
	eng := buildEng(t, happyTrace())
	feature := `Feature: cel-red
  Scenario: false expression fails
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "tokens < 1"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "cel-red", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected failing suite, got 0\n%s", out.String())
	}
	if s := out.String(); !strings.Contains(s, "cel false") || !strings.Contains(s, "tokens=1800") {
		t.Fatalf("expected 'cel false' + 'tokens=1800' in output, got:\n%s", s)
	}
}

// TestPrecompileScenario unit-tests §7: a malformed expression fails at
// scenario-init; good inline and docstring forms compile cleanly.
func TestPrecompileScenario(t *testing.T) {
	eng := buildEng(t, happyTrace())
	w := &world{eng: eng}

	good := []*messages.PickleStep{{Text: `the run satisfies "tokens < 5000"`}}
	if err := w.precompileScenario(good); err != nil {
		t.Fatalf("good inline precompile: %v", err)
	}
	doc := []*messages.PickleStep{{
		Text:     `the run satisfies:`,
		Argument: &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: `"search" in tools`}},
	}}
	if err := w.precompileScenario(doc); err != nil {
		t.Fatalf("good docstring precompile: %v", err)
	}
	bad := []*messages.PickleStep{{Text: `the run satisfies "tokens <"`}}
	if err := w.precompileScenario(bad); err == nil {
		t.Fatal("want error for malformed expr at scenario-init, got nil")
	}
}

// TestCELScenarioInitFailsBeforeDrive proves §7: a malformed expression fails the
// scenario before any SUT is driven — the store is never queried (Times(0)).
func TestCELScenarioInitFailsBeforeDrive(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"svc": {Adapter: "shell", Command: []string{"sh", "-c", "echo done"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Times(0)
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Times(0)
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: cel-bad
  Scenario: malformed expression fails at scenario-init
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "nope == "
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "cel-bad", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected failing suite for malformed expr, got 0\n%s", out.String())
	}
	if s := out.String(); !strings.Contains(s, "scenario-init") {
		t.Fatalf("expected 'scenario-init' in output, got:\n%s", s)
	}
	// ctrl's t.Cleanup asserts Query/GetByID were never called → no SUT resolved.
}

func TestRunSatisfiesDocNil(t *testing.T) {
	w := &world{}
	if err := w.runSatisfiesDoc(nil); err == nil {
		t.Fatal("want error for nil docstring, got nil")
	}
}

// TestStepMethods exercises each step method that the happy-scenario godog run
// does not reach, using a crafted Evidence so comparators have the data they need.
func TestStepMethods(t *testing.T) {
	cr := craftedTrace()

	tests := []struct {
		name    string
		fn      func(w *world) error
		wantErr bool
	}{
		{
			name: "costUnder_pass",
			fn: func(w *world) error {
				return w.costUnder(1.0) // 0.01 < 1.0
			},
		},
		{
			name: "costUnder_fail",
			fn: func(w *world) error {
				return w.costUnder(0.001) // 0.01 > 0.001
			},
			wantErr: true,
		},
		{
			name: "latencyUnder_pass",
			fn: func(w *world) error {
				return w.latencyUnder(1000) // 50ms < 1000ms
			},
		},
		{
			name: "latencyUnder_fail",
			fn: func(w *world) error {
				return w.latencyUnder(1) // 50ms > 1ms
			},
			wantErr: true,
		},
		{
			name: "noErrorSpans_pass",
			fn: func(w *world) error {
				return w.noErrorSpans()
			},
		},
		{
			name: "noErrorSpans_fail",
			fn: func(w *world) error {
				// inject an error span
				w.ev.Trace.Spans[0].Status = "Error"
				return w.noErrorSpans()
			},
			wantErr: true,
		},
		{
			name: "resultEquals_pass",
			fn: func(w *world) error {
				return w.resultEquals("done")
			},
		},
		{
			name: "resultEquals_fail",
			fn: func(w *world) error {
				return w.resultEquals("wrong")
			},
			wantErr: true,
		},
		{
			name: "responseStatus_pass",
			fn: func(w *world) error {
				return w.responseStatus(200)
			},
		},
		{
			name: "responseStatus_fail",
			fn: func(w *world) error {
				return w.responseStatus(404)
			},
			wantErr: true,
		},
		{
			name: "runPrompt_pass",
			fn: func(w *world) error {
				if err := w.target_("svc"); err != nil {
					return err
				}
				return w.runPrompt("hello world")
			},
		},
		{
			// drive with no target set must return error (no silent fallback).
			// fn resets target to "" itself so the no-target guard fires; no pre-drive.
			name: "drive_no_target",
			fn: func(w *world) error {
				w.target = ""
				return w.runScenario("any")
			},
			wantErr: true,
		},
		{
			// check with unknown comparator name must return error.
			// fn drives the world itself so the call reaches the comparator lookup.
			name: "check_unknown_comparator",
			fn: func(w *world) error {
				if err := w.target_("svc"); err != nil {
					return err
				}
				if err := w.runScenario("any"); err != nil {
					return err
				}
				return w.check("no_such_comparator", nil)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, cr)
			w := &world{eng: eng}

			// All cases except the three that drive themselves need a pre-driven world
			// with crafted Evidence so comparators have the data they need.
			switch tt.name {
			case "runPrompt_pass", "drive_no_target", "check_unknown_comparator":
				// fn manages its own setup.
			default:
				if err := w.target_("svc"); err != nil {
					t.Fatalf("setup target_: %v", err)
				}
				if err := w.runScenario("any"); err != nil {
					t.Fatalf("setup runScenario: %v", err)
				}
				w.ev = core.Evidence{
					RunID: "r",
					Trace: cr,
					Output: core.Output{
						Answer: "done",
						Status: 200,
					},
				}
				// noErrorSpans_fail mutates a span's Status; give it a fresh trace
				// so the mutation does not bleed into other sub-tests.
				if tt.name == "noErrorSpans_fail" {
					w.ev.Trace = craftedTrace()
				}
			}

			err := tt.fn(w)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
