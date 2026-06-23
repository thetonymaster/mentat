package steps

import (
	"bytes"
	"context"
	"fmt"
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
	"github.com/thetonymaster/mentat/internal/report"
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

// TestCELStep exercises the inline + docstring "the run satisfies" grammar
// end-to-end through a godog suite: a true expression passes the suite; a false
// one fails it and surfaces the §9 value snapshot. happyTrace has tools
// search/summarize and 1800 tokens; buildEng's shell target prints "done", so
// answer == 'done'. Inline CEL uses single-quoted strings (the step regex
// forbids embedded double quotes); the docstring form may use double quotes.
func TestCELStep(t *testing.T) {
	tests := []struct {
		name     string
		feature  string
		wantPass bool
		contains []string // substrings required in the suite output (failure cases)
	}{
		{
			name: "inline and docstring both satisfied",
			feature: `Feature: cel
  Scenario: satisfies inline and docstring
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "answer == 'done' && tokens < 5000"
    And the run satisfies:
      """
      "search" in tools && "summarize" in tools
      """
`,
			wantPass: true,
		},
		{
			name: "false expression fails with value snapshot",
			feature: `Feature: cel-red
  Scenario: false expression fails
    Given the agent target "svc"
    When I run scenario "happy"
    Then the run satisfies "tokens < 1"
`,
			wantPass: false,
			contains: []string{"cel false", "tokens=1800"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, happyTrace())
			var out bytes.Buffer
			suite := godog.TestSuite{
				ScenarioInitializer: Initializer(eng),
				Options: &godog.Options{
					Format:          "pretty",
					Output:          &out,
					FeatureContents: []godog.Feature{{Name: tt.name, Contents: []byte(tt.feature)}},
				},
			}
			status := suite.Run()
			if tt.wantPass && status != 0 {
				t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
			}
			if !tt.wantPass && status == 0 {
				t.Fatalf("expected failing suite, got 0\n%s", out.String())
			}
			for _, sub := range tt.contains {
				if !strings.Contains(out.String(), sub) {
					t.Fatalf("expected %q in suite output, got:\n%s", sub, out.String())
				}
			}
		})
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

	// Fix 3: docstring form of "the runs satisfy:" is precompiled via the aggregate-cel path.
	runsDoc := []*messages.PickleStep{{
		Text:     `the runs satisfy:`,
		Argument: &messages.PickleStepArgument{DocString: &messages.PickleDocString{Content: `rate(r, 'search' in r.tools) >= 0.5`}},
	}}
	if err := w.precompileScenario(runsDoc); err != nil {
		t.Fatalf("runs-satisfy docstring precompile: %v", err)
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

// TestSchemaStep exercises the `the response body matches schema` step. The
// schema matcher reads ev.Output.Body, which the shell driver does not populate,
// so the body is set directly on the world's Evidence (the engine.Build inside
// buildEng registers the schema matcher + result comparator).
func TestSchemaStep(t *testing.T) {
	eng := buildEng(t, happyTrace())
	const schema = `{"type":"object","required":["status"],` +
		`"properties":{"status":{"type":"string"}}}`

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "valid body passes", body: `{"status":"confirmed"}`, wantErr: false},
		{name: "missing field fails", body: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &world{eng: eng}
			w.ev = core.Evidence{Output: core.Output{Body: []byte(tt.body)}}
			err := w.responseBodyMatchesSchema(&godog.DocString{Content: schema})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}

	t.Run("nil docstring is an error", func(t *testing.T) {
		w := &world{eng: eng}
		if err := w.responseBodyMatchesSchema(nil); err == nil {
			t.Fatal("want error for nil docstring, got nil")
		}
	})
}

// TestRegexStep exercises the `the result matches regex` grammar end-to-end
// through a godog suite: a matching pattern passes the suite; a non-matching one
// fails it and surfaces the result-comparator's regex reason. buildEng's `svc`
// shell target prints "done", so answer == "done".
func TestRegexStep(t *testing.T) {
	tests := []struct {
		name     string
		feature  string
		wantPass bool
		contains []string // substrings required in suite output (failure cases)
	}{
		{
			name: "matching pattern passes",
			feature: `Feature: regex
  Scenario: answer matches
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result matches regex "^do.e$"
`,
			wantPass: true,
		},
		{
			name: "non-matching pattern fails with reason",
			feature: `Feature: regex-red
  Scenario: answer does not match
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result matches regex "zzz"
`,
			wantPass: false,
			contains: []string{"result regex"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, happyTrace())
			var out bytes.Buffer
			suite := godog.TestSuite{
				ScenarioInitializer: Initializer(eng),
				Options: &godog.Options{
					Format:          "pretty",
					Output:          &out,
					FeatureContents: []godog.Feature{{Name: tt.name, Contents: []byte(tt.feature)}},
				},
			}
			status := suite.Run()
			if tt.wantPass && status != 0 {
				t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
			}
			if !tt.wantPass && status == 0 {
				t.Fatalf("expected failing suite, got 0\n%s", out.String())
			}
			for _, sub := range tt.contains {
				if !strings.Contains(out.String(), sub) {
					t.Fatalf("expected %q in suite output, got:\n%s", sub, out.String())
				}
			}
		})
	}
}

func TestRunSatisfiesDocNil(t *testing.T) {
	w := &world{}
	if err := w.runSatisfiesDoc(nil); err == nil {
		t.Fatal("want error for nil docstring, got nil")
	}
}

// TestRunsSatisfiesDocNil tests that runsSatisfiesDoc returns an error for a nil docstring.
func TestRunsSatisfiesDocNil(t *testing.T) {
	w := &world{}
	if err := w.runsSatisfiesDoc(nil); err == nil {
		t.Fatal("want error for nil docstring, got nil")
	}
}

// TestCheckRunsErrors exercises checkRuns error paths (no evs driven).
func TestCheckRunsErrors(t *testing.T) {
	eng := buildEng(t, happyTrace())
	tests := []struct {
		name   string
		setup  func(w *world)
		errSub string
	}{
		{
			name:   "no_evs_driven",
			setup:  func(w *world) {}, // evs is nil
			errSub: "no runs driven",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &world{eng: eng}
			tt.setup(w)
			err := w.checkRuns("true")
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("expected error containing %q, got: %v", tt.errSub, err)
			}
		})
	}
}

// TestParseRunsTag tests the @runs tag parser including error paths.
func TestParseRunsTag(t *testing.T) {
	tests := []struct {
		name    string
		tags    []*messages.PickleTag
		wantN   int
		wantPar bool
		wantErr bool
		errSub  string
	}{
		{
			name:    "absent_tag_defaults_to_1",
			tags:    nil,
			wantN:   1,
			wantPar: false,
		},
		{
			name:    "runs_3",
			tags:    []*messages.PickleTag{{Name: "@runs(3)"}},
			wantN:   3,
			wantPar: false,
		},
		{
			name:    "runs_2_parallel",
			tags:    []*messages.PickleTag{{Name: "@runs(2,parallel)"}},
			wantN:   2,
			wantPar: true,
		},
		{
			name:    "malformed_tag",
			tags:    []*messages.PickleTag{{Name: "@runs(bad)"}},
			wantErr: true,
			errSub:  "malformed @runs tag",
		},
		{
			name:    "zero_n_is_rejected",
			tags:    []*messages.PickleTag{{Name: "@runs(0)"}},
			wantErr: true,
			errSub:  "@runs requires N>=1",
		},
		{
			name:    "unrelated_tag_ignored",
			tags:    []*messages.PickleTag{{Name: "@smoke"}},
			wantN:   1,
			wantPar: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			n, par, err := parseRunsTag(tt.tags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("expected error containing %q, got: %v", tt.errSub, err)
				}
				return
			}
			if n != tt.wantN {
				t.Fatalf("n=%d want=%d", n, tt.wantN)
			}
			if par != tt.wantPar {
				t.Fatalf("parallel=%v want=%v", par, tt.wantPar)
			}
		})
	}
}

// runsEngine builds an engine whose store returns happyTrace for every run, with
// distinct run ids, for hermetic @runs scenarios.
func runsEngine(t *testing.T, tr *trace.Trace) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 4}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
	var n int
	cor := correlate.New(func() string { n++; return fmt.Sprintf("run-%d", n) }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

func TestRunsSatisfiesStep(t *testing.T) {
	feature := `Feature: multirun
  @runs(3)
  Scenario: search always present
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, 'search' in r.tools) >= 0.9"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(runsEngine(t, happyTrace())),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true, // undefined steps must be treated as failures
			FeatureContents: []godog.Feature{{Name: "multirun", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected pass (happyTrace has search), status=%d\n%s", status, out.String())
	}
}

// badDistEngine returns an engine whose store yields a trace WITH "search" on a
// fraction of runs and WITHOUT it on the rest, deterministically by call count.
func badDistEngine(t *testing.T, withSearch, total int) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	withTrace := &trace.Trace{
		Roots: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}},
		Spans: []*trace.Span{
			{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}},
			{Name: "tool search", Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"}},
		},
	}
	withoutTrace := &trace.Trace{
		Roots: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}},
		Spans: []*trace.Span{{Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	// calls counts GetByID invocations; each run makes exactly 1 GetByID when the
	// polling timeout fires immediately (Timeout: time.Nanosecond forces a single
	// poll per run — if spans>0 the correlator returns best-effort at deadline).
	var calls int
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string) (*trace.Trace, error) {
		calls++
		if calls <= withSearch {
			return withTrace, nil
		}
		return withoutTrace, nil
	}).Times(total)
	var n int
	cor := correlate.New(func() string { n++; return fmt.Sprintf("run-%d", n) }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Nanosecond})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// TestMultirunGoesRedOnBadDistribution is the L3 meta-test: 5/10 runs have
// "search" so rate=0.5, the assertion requires >=0.8; Mentat must go RED.
func TestMultirunGoesRedOnBadDistribution(t *testing.T) {
	feature := `Feature: meta-multirun
  @runs(10)
  Scenario: search must be consulted in >= 80% of runs
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, 'search' in r.tools) >= 0.8"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(badDistEngine(t, 5, 10)),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "meta-multirun", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected RED on a 0.5 search rate vs >=0.8, but suite passed\n%s", out.String())
	}
	if !strings.Contains(out.String(), "aggregate-cel failed") {
		t.Fatalf("expected aggregate-cel failure reason, got:\n%s", out.String())
	}
}

// TestMultirunGoesGreenOnGoodDistribution: 9/10 runs have "search" so rate=0.9
// >= 0.8; Mentat must go GREEN.
func TestMultirunGoesGreenOnGoodDistribution(t *testing.T) {
	feature := `Feature: meta-multirun
  @runs(10)
  Scenario: search consulted in >= 80% of runs
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "rate(r, 'search' in r.tools) >= 0.8"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(badDistEngine(t, 9, 10)),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "meta-multirun", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected GREEN on a 0.9 search rate vs >=0.8, status=%d\n%s", status, out.String())
	}
}

// TestSingleRunStepRejectedInMultirunScenario is the L3 meta-test that proves a
// single-run comparator step used inside a @runs(N>1) scenario is a hard,
// descriptive error (no silent first-run-only evaluation). The feature uses
// "total tokens are under 5000" which would PASS against happyTrace's 1800 tokens
// if the guard were absent — a GREEN result here means the guard is missing.
func TestSingleRunStepRejectedInMultirunScenario(t *testing.T) {
	feature := `Feature: mixed-grammar
  @runs(2)
  Scenario: single-run step under @runs is rejected
    Given the agent target "bot"
    When I run scenario "x"
    Then total tokens are under 5000
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(runsEngine(t, happyTrace())),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "mixed-grammar", Contents: []byte(feature)}},
		},
	}
	status := suite.Run()
	if status == 0 {
		t.Fatalf("expected RED: single-run step inside @runs(2) must be rejected, but suite passed\n%s", out.String())
	}
	outStr := out.String()
	if !strings.Contains(outStr, "@runs(2)") {
		t.Fatalf("expected error to mention @runs(2), got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "the runs satisfy") {
		t.Fatalf("expected error to mention \"the runs satisfy\", got:\n%s", outStr)
	}
}

// TestInitializer_CollectsResults verifies that InitializerWithCollector wires an
// After hook that appends one ScenarioResult per passing scenario to the Collector.
// Mirrors the harness pattern from TestFeatureExercisesGrammarAgainstFakeEngine.
func TestInitializer_CollectsResults(t *testing.T) {
	col := report.NewCollector()

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

	feature := `Feature: collect
  Scenario: passing scenario
    Given the agent target "bot"
    When I run scenario "happy"
    Then the result contains "hi"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: InitializerWithCollector(eng, col),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "collect", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}

	rep := col.Report(time.Unix(0, 0), 0)
	if rep.Total != 1 {
		t.Fatalf("collector got %d scenarios, want 1", rep.Total)
	}
	if !rep.Scenarios[0].Pass {
		t.Fatalf("scenario[0].Pass=false, want true")
	}
}

// TestInitializer_CollectsFailingAggregateDetail proves the failing-aggregate path
// carries its Detail all the way to the Collector. checkRuns records w.lastDetail
// BEFORE returning the failure error, and the After hook folds that Detail into the
// scenario Verdict — so a RED "the runs satisfy" still yields a populated
// ScenarioResult.Aggregate (computed-vs-expected), not a bare pass/fail flag.
func TestInitializer_CollectsFailingAggregateDetail(t *testing.T) {
	col := report.NewCollector()
	eng := runsEngine(t, happyTrace())

	// happyTrace always calls "search", so count==2 on every run; the assertion
	// count==0 fails. It is canonical, so a Detail is produced and must survive.
	feature := `Feature: collect-fail
  @runs(2)
  Scenario: failing aggregate carries detail
    Given the agent target "bot"
    When I run scenario "x"
    Then the runs satisfy "count(r, 'search' in r.tools) == 0"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: InitializerWithCollector(eng, col),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "collect-fail", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected RED on count==0 vs 2 search runs, but suite passed\n%s", out.String())
	}

	rep := col.Report(time.Unix(0, 0), 0)
	if rep.Total != 1 {
		t.Fatalf("collector got %d scenarios, want 1", rep.Total)
	}
	sr := rep.Scenarios[0]
	if sr.Pass {
		t.Fatalf("scenario Pass=true, want false")
	}
	if sr.Aggregate == nil {
		t.Fatalf("Aggregate nil: lastDetail not captured before the failure return")
	}
	if sr.Aggregate.Macro != "count" || sr.Aggregate.Op != "==" {
		t.Errorf("Aggregate macro/op = %q/%q, want count/==", sr.Aggregate.Macro, sr.Aggregate.Op)
	}
	if sr.Aggregate.Computed != 2 || sr.Aggregate.Expected != 0 {
		t.Errorf("Aggregate computed/expected = %v/%v, want 2/0", sr.Aggregate.Computed, sr.Aggregate.Expected)
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
