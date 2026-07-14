package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/registry"
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

// degradedTrace: a single span with no gen_ai.* tool attrs and no service.name, so
// the report's tool/service sequence derivation is impossible. Drive still succeeds
// and the driver output is unaffected — used to prove that a derivation problem
// never flips a verdict (audit A8 / research R5).
func degradedTrace() *trace.Trace {
	span := &trace.Span{ID: "abc123", Name: "fetch", Attrs: map[string]string{}}
	return &trace.Trace{Roots: []*trace.Span{span}, Spans: []*trace.Span{span}}
}

// stubStoredTrace stubs the feature-004 store seam pair (FetchPayload +
// DecodePayload) on st to serve tr for every trace id, with a byte-stable
// payload so Resolve's stability gate converges exactly as the old
// constant-trace GetByID stub did.
func stubStoredTrace(st *mocks.MockTraceStore, tr *trace.Trace) {
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).Return([]byte("payload"), nil).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
}

func TestFeatureExercisesGrammarAgainstFakeEngine(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, happyTrace())
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
	stubStoredTrace(st, happyTrace())
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
	stubStoredTrace(st, tr)
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

// TestShapeStepSelectorErrorsWrapped: shape step handlers must wrap ParseSelector
// failures with which selector failed (subject/parent) and the raw value, per the
// %w error-wrapping convention — not return the bare parser error.
func TestShapeStepSelectorErrorsWrapped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		call   func(w *world) error
		errSub string
	}{
		{"exists subject", func(w *world) error { return w.shapeExists("noequals") }, `parse shape subject selector "noequals"`},
		{"childOf subject", func(w *world) error { return w.shapeChildOf("badchild", "k=v") }, `parse shape subject selector "badchild"`},
		{"childOf parent", func(w *world) error { return w.shapeChildOf("k=v", "badparent") }, `parse shape parent selector "badparent"`},
		{"fanout parent", func(w *world) error { return w.shapeFanoutAtLeast("badparent", 2, "k=v") }, `parse shape parent selector "badparent"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &world{} // no engine needed — parse error fires before check()
			err := tt.call(w)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errSub)
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("expected error containing %q, got: %v", tt.errSub, err)
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
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).Times(0)
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).Times(0)
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
	// ctrl's t.Cleanup asserts Query/FetchPayload were never called → no SUT resolved.
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

// failingDriveEngine wires an engine whose driver succeeds (shell echo) but whose
// correlator always fails Resolve, so a single-run drive yields a failed evs[0]
// carrying FailureMsg — the A2 "broken run" case.
func failingDriveEngine(t *testing.T, resolveErr error) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1").AnyTimes()
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, resolveErr).AnyTimes()
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// TestSingleRunDriveFailureGoesRed is the A2 core: a broken single run can never
// pass. Both an asserting scenario AND an assertion-free scenario (Given + When,
// no Then) must go RED, surfacing the underlying drive failure — even though the
// resolve-failure path RETAINS the real driver Output (which would otherwise let a
// "the result contains" assertion pass on a broken run).
func TestSingleRunDriveFailureGoesRed(t *testing.T) {
	tests := []struct {
		name     string
		feature  string
		contains []string
	}{
		{
			name: "asserting scenario goes red on failed drive",
			feature: `Feature: fail
  Scenario: drive fails with an assertion present
    Given the agent target "bot"
    When I run scenario "x"
    Then the result contains "hi"
`,
			contains: []string{"store down"},
		},
		{
			name: "assertion-free scenario still goes red on failed drive",
			feature: `Feature: fail
  Scenario: drive fails with no Then
    Given the agent target "bot"
    When I run scenario "x"
`,
			contains: []string{"store down"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := failingDriveEngine(t, errors.New("store down"))
			var out bytes.Buffer
			suite := godog.TestSuite{
				ScenarioInitializer: Initializer(eng),
				Options: &godog.Options{
					Format:          "pretty",
					Output:          &out,
					Strict:          true,
					FeatureContents: []godog.Feature{{Name: tt.name, Contents: []byte(tt.feature)}},
				},
			}
			if status := suite.Run(); status == 0 {
				t.Fatalf("expected RED on a failed drive, but suite passed\n%s", out.String())
			}
			for _, sub := range tt.contains {
				if !strings.Contains(out.String(), sub) {
					t.Fatalf("expected %q in suite output, got:\n%s", sub, out.String())
				}
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
	stubStoredTrace(st, tr)
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
// fraction of runs and WITHOUT it on the rest, deterministically by run id.
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
	// Distribution is keyed by RUN ID, not store call count. A successful Resolve
	// needs ≥2 polls per run (StableFor:1 requires one unchanged observation), so
	// the store is polled more than once per run and a call-count split no longer
	// maps 1:1 to runs. Query returns the run id AS the trace id so the decode can
	// identify the run; the payload is constant per run across polls, so the
	// observation is byte-stable and Resolve succeeds — the deadline/best-effort
	// path (now a hard error, audit A3) is never taken.
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) ([]byte, error) {
			return []byte("payload-" + id), nil
		}).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(id string, _ []byte) (*trace.Trace, error) {
			// id is the run id "run-K"; runs run-1..run-withSearch carry "search".
			var k int
			if _, err := fmt.Sscanf(id, "run-%d", &k); err != nil {
				// Fail loudly rather than silently defaulting to withoutTrace:
				// an id that does not match "run-K" means the test's own wiring
				// drifted, not a real not-found (no silent fallbacks).
				return nil, fmt.Errorf("badDistEngine mock: unexpected trace id %q: %w", id, err)
			}
			if k >= 1 && k <= withSearch {
				return withTrace, nil
			}
			return withoutTrace, nil
		}).AnyTimes()
	var n int
	cor := correlate.New(func() string { n++; return fmt.Sprintf("run-%d", n) },
		correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
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
	stubStoredTrace(st, happyTrace())
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

	rep := col.Report(time.Unix(0, 0), 0, false)
	if rep.Total != 1 {
		t.Fatalf("collector got %d scenarios, want 1", rep.Total)
	}
	if !rep.Scenarios[0].Pass {
		t.Fatalf("scenario[0].Pass=false, want true")
	}
}

// TestInitializer_DerivationDegradationKeepsVerdict is the audit-A8 regression guard
// (research R5): a scenario whose report derivation is impossible — here a span
// missing service.name so no tool/service sequence can be built — must STAY passing
// because the report is an observer, while the resulting ScenarioResult still
// surfaces a DerivationNote. Before the fix the After hook propagated report.Derive's
// error to godog, flipping the verdict to fail.
func TestInitializer_DerivationDegradationKeepsVerdict(t *testing.T) {
	col := report.NewCollector()
	eng := runsEngine(t, degradedTrace())

	feature := `Feature: degraded-derivation
  Scenario: passing scenario with underivable sequence
    Given the agent target "bot"
    When I run scenario "x"
    Then the result contains "hi"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: InitializerWithCollector(eng, col),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "degraded-derivation", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("scenario failed (status=%d); a derivation problem must not flip the verdict\n%s", status, out.String())
	}

	rep := col.Report(time.Unix(0, 0), 0, false)
	if rep.Total != 1 {
		t.Fatalf("collector got %d scenarios, want 1", rep.Total)
	}
	sr := rep.Scenarios[0]
	if !sr.Pass {
		t.Errorf("scenario Pass=false, want true (verdict unchanged by derivation)")
	}
	if sr.DerivationNote == "" {
		t.Fatalf("DerivationNote empty, want a note naming the missing service.name")
	}
	if !strings.Contains(sr.DerivationNote, "service.name") {
		t.Errorf("DerivationNote = %q, want it to name service.name", sr.DerivationNote)
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

	rep := col.Report(time.Unix(0, 0), 0, false)
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

// writeExpectation writes one pattern file into a fresh dir and returns the dir.
func writeExpectation(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write pattern: %v", err)
	}
	return dir
}

func shapePatternEngine(t *testing.T, expDir string) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Expectations: expDir,
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, shapeTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

func runShapePatternFeature(t *testing.T, eng *engine.Engine, feature string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "pattern", Contents: []byte(feature)}},
		},
	}
	return suite.Run(), out.String()
}

func TestFeatureMatchesShapePattern(t *testing.T) {
	eng := shapePatternEngine(t, writeExpectation(t, `name: research-shape
clauses:
  - exists: "gen_ai.tool.name=search"
    count: ">=2"
  - child: "gen_ai.tool.name=search"
    of: "gen_ai.operation.name=chat"
  - fanout:
      parent: "gen_ai.operation.name=chat"
      child: "gen_ai.tool.name=search"
      count: ">=2"
`))
	feature := `Feature: pattern
  Scenario: structural pattern holds
    Given the agent target "bot"
    When I run scenario "happy"
    Then the run matches shape "research-shape"
`
	if status, out := runShapePatternFeature(t, eng, feature); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out)
	}
}

func TestFeatureMatchesShapePatternRed(t *testing.T) {
	eng := shapePatternEngine(t, writeExpectation(t, `name: impossible
clauses:
  - child: "gen_ai.operation.name=invoke_agent"
    of: "gen_ai.tool.name=search"
`))
	feature := `Feature: pattern
  Scenario: impossible containment fails
    Given the agent target "bot"
    When I run scenario "happy"
    Then the run matches shape "impossible"
`
	status, out := runShapePatternFeature(t, eng, feature)
	if status == 0 {
		t.Fatalf("expected failing suite, but it passed\n%s", out)
	}
	if !strings.Contains(out, "shape failed") {
		t.Fatalf("expected \"shape failed\" in output, got:\n%s", out)
	}
}

// TestFeatureUnknownShapePattern proves that an unknown shape pattern name fails
// the scenario in sc.Before — before the SUT is driven. Times(0) on Query/FetchPayload
// asserts the store is never contacted, mirroring TestCELScenarioInitFailsBeforeDrive.
func TestFeatureUnknownShapePattern(t *testing.T) {
	// Load a dir that has a KNOWN pattern so engine.Build succeeds; the feature
	// references a DIFFERENT name ("does-not-exist") that was never loaded.
	expDir := writeExpectation(t, `name: known
clauses:
  - exists: "gen_ai.tool.name=search"
`)
	cfg := config.Config{
		OTLPEndpoint: "x",
		Expectations: expDir,
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	// Times(0): the store must never be queried — the scenario aborts in sc.Before.
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Times(0)
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).Times(0)
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).Times(0)
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: pattern
  Scenario: unknown pattern name fails before driving
    Given the agent target "bot"
    When I run scenario "happy"
    Then the run matches shape "does-not-exist"
`
	status, out := runShapePatternFeature(t, eng, feature)
	if status == 0 {
		t.Fatalf("expected failing suite for unknown pattern, but it passed\n%s", out)
	}
	if !strings.Contains(out, "unknown shape pattern") {
		t.Fatalf("expected \"unknown shape pattern\" in output, got:\n%s", out)
	}
	// ctrl's t.Cleanup asserts Query/FetchPayload were never called → pre-check fired before drive.
}

// spanResultTrace: two "search" calls with distinct results + start times and one
// "summarize" — drives the tool-form grammar through a godog suite.
func spanResultTrace() *trace.Trace {
	t0 := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	mk := func(id, tool, res string, start time.Time) *trace.Span {
		return &trace.Span{ID: id, Name: "execute_tool " + tool, Start: start,
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tool, genai.ToolResult: res}}
	}
	root := &trace.Span{Name: "invoke_agent", Attrs: map[string]string{
		genai.Op: genai.OpInvokeAgent, genai.InTokens: "100", genai.OutTokens: "50"}}
	s1 := mk("s1", "search", "first-result", t0)
	s2 := mk("s2", "search", "second-result", t0.Add(time.Second))
	s3 := mk("s3", "summarize", "the summary", t0.Add(2*time.Second))
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: []*trace.Span{root, s1, s2, s3}}
}

// TestVerbToMatcher covers all five verb→matcher mappings and the error path for
// an unknown verb. t.Parallel() is safe: no engine, env, or mutable state.
func TestVerbToMatcher(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		verb    string
		want    string
		wantErr bool
	}{
		{name: "contains", verb: "contains", want: "contains"},
		{name: "equals", verb: "equals", want: "exact"},
		{name: "matches regex", verb: "matches regex", want: "regex"},
		{name: "json-contains", verb: "json-contains", want: "json-subset"},
		{name: "matches schema", verb: "matches schema", want: "schema"},
		{name: "unknown verb returns error", verb: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := verbToMatcher(tt.verb)
			if (err != nil) != tt.wantErr {
				t.Fatalf("verbToMatcher(%q) err=%v wantErr=%v", tt.verb, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("verbToMatcher(%q) = %q, want %q", tt.verb, got, tt.want)
			}
		})
	}
}

func TestResultToolStep(t *testing.T) {
	// serial: engine.Build() writes to unsynchronized package-level registry maps;
	// -race proves a data race under t.Parallel() (see commit f0b4505). Keep serial.
	tests := []struct {
		name     string
		feature  string
		wantPass bool
	}{
		{
			name: "bare tool form (single match) passes",
			feature: `Feature: result-tool
  Scenario: summarize result
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of tool "summarize" contains "summary"
`,
			wantPass: true,
		},
		{
			name: "first call ordinal passes",
			feature: `Feature: result-tool
  Scenario: first search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of the first call to tool "search" equals "first-result"
`,
			wantPass: true,
		},
		{
			name: "last call ordinal passes",
			feature: `Feature: result-tool
  Scenario: last search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of the last call to tool "search" equals "second-result"
`,
			wantPass: true,
		},
		{
			name: "every call quantifier passes",
			feature: `Feature: result-tool
  Scenario: every search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of every call to tool "search" contains "result"
`,
			wantPass: true,
		},
		{
			name: "any call quantifier passes",
			feature: `Feature: result-tool
  Scenario: any search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of any call to tool "search" contains "second"
`,
			wantPass: true,
		},
		{
			name: "docstring json-contains fails (plain string is not JSON)",
			feature: `Feature: result-tool
  Scenario: summarize json
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of tool "summarize" json-contains:
      """
      "the summary"
      """
`,
			wantPass: false, // "the summary" is a plain string, not JSON-subset of itself-as-string; see note
		},
		{
			name: "ambiguous bare tool form fails the suite",
			feature: `Feature: result-tool
  Scenario: ambiguous search
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of tool "search" contains "result"
`,
			wantPass: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, spanResultTrace())
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
		})
	}
}

func TestResultAttrStep(t *testing.T) {
	// serial: engine.Build() writes to unsynchronized package-level registry maps;
	// -race proves a data race under t.Parallel() (see commit f0b4505). Keep serial.
	tests := []struct {
		name     string
		feature  string
		wantPass bool
	}{
		{
			name: "selector form: last search result by attribute passes",
			feature: `Feature: result-attr
  Scenario: last search via selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "gen_ai.tool.call.result" of the last span matching "gen_ai.tool.name=search" equals "second-result"
`,
			wantPass: true,
		},
		{
			name: "selector form: every search via selector passes",
			feature: `Feature: result-attr
  Scenario: every search via selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "gen_ai.tool.call.result" of every span matching "gen_ai.tool.name=search" contains "result"
`,
			wantPass: true,
		},
		{
			name: "selector form: reserved span.* attribute (name) passes",
			feature: `Feature: result-attr
  Scenario: span name via selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "span.name" of the first span matching "gen_ai.tool.name=summarize" contains "summarize"
`,
			wantPass: true,
		},
		{
			name: "selector form: bad selector fails the suite",
			feature: `Feature: result-attr
  Scenario: bad selector
    Given the agent target "svc"
    When I run scenario "happy"
    Then attribute "gen_ai.tool.call.result" of the span matching "noequals" contains "x"
`,
			wantPass: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, spanResultTrace())
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
		})
	}
}

func TestParseSpanSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		slot    string
		wantQ   comparator.Quant
		wantIdx int
		wantErr bool
	}{
		{"", comparator.QuantOne, 0, false},
		{"the first call", comparator.QuantFirst, 0, false},
		{"the last call", comparator.QuantLast, 0, false},
		{"the 2nd call", comparator.QuantNth, 2, false},
		{"the 1st", comparator.QuantNth, 1, false},
		{"every call", comparator.QuantEvery, 0, false},
		{"any span", comparator.QuantAny, 0, false},
		{"the 0th call", 0, 0, true},
		{"sideways call", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.slot, func(t *testing.T) {
			t.Parallel()
			q, idx, err := parseSpanSpec(tt.slot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if q != tt.wantQ || idx != tt.wantIdx {
				t.Errorf("got (q=%d idx=%d), want (q=%d idx=%d)", q, idx, tt.wantQ, tt.wantIdx)
			}
		})
	}
}

// shapeTrace: invoke_agent(root) → chat → {search, search, summarize(ERROR)}. IDs are
// set explicitly so shape's containment/fan-out matching works (LoadFixture omits IDs).
func shapeTrace() *trace.Trace {
	root := &trace.Span{ID: "root", Name: "invoke_agent", Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
	chat := &trace.Span{ID: "chat", ParentID: "root", Name: "chat", Attrs: map[string]string{genai.Op: genai.OpChat}}
	mk := func(id, tool, status string) *trace.Span {
		return &trace.Span{ID: id, ParentID: "chat", Name: "execute_tool " + tool, Status: status,
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tool}}
	}
	s1, s2, sum := mk("t1", "search", trace.StatusOk), mk("t2", "search", trace.StatusOk), mk("t3", "summarize", trace.StatusError)
	return &trace.Trace{Roots: []*trace.Span{root}, Spans: []*trace.Span{root, chat, s1, s2, sum}}
}

func TestFeatureExercisesShapeGrammar(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, shapeTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: shape grammar
  Scenario: structural assertions hold
    Given the agent target "bot"
    When I run scenario "happy"
    Then a span matching "gen_ai.tool.name=search" exists
    And no span matching "gen_ai.tool.name=delete" exists
    And at least 2 spans match "gen_ai.tool.name=search"
    And exactly 1 span matches "gen_ai.tool.name=summarize"
    And a span matching "gen_ai.tool.name=search" is a child of a span matching "gen_ai.operation.name=chat"
    And a span matching "gen_ai.tool.name=search" is a descendant of a span matching "gen_ai.operation.name=invoke_agent"
    And a span matching "gen_ai.operation.name=chat" has at least 2 children matching "gen_ai.tool.name=search"
    And a span matching "span.status=Error" exists
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "shape", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
	}
}

// TestFeatureGoesRedOnBadShape: invoke_agent is a root, so asserting it is a child of a
// tool span must fail — the hermetic complement to the binary L3 meta-test (Task 6).
func TestFeatureGoesRedOnBadShape(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, shapeTrace())
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: bad-shape
  Scenario: impossible containment
    Given the agent target "bot"
    When I run scenario "any"
    Then a span matching "gen_ai.operation.name=invoke_agent" is a child of a span matching "gen_ai.tool.name=search"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "bad-shape", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status == 0 {
		t.Fatalf("expected suite to fail (non-zero), but it passed\n%s", out.String())
	}
	if !strings.Contains(out.String(), "shape failed") {
		t.Fatalf("expected output to contain \"shape failed\", got:\n%s", out.String())
	}
}

// TestSpanResultGoesRedOnBadResult proves the result comparator goes red when a
// tool's span result does not match — the L3 contract for the span-attribute source.
func TestSpanResultGoesRedOnBadResult(t *testing.T) {
	eng := buildEng(t, spanResultTrace())
	feature := `Feature: bad-span-result
  Scenario: last search result mismatch
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result of the last call to tool "search" contains "NONEXISTENT"
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "bad-span-result", Contents: []byte(feature)}},
		},
	}
	status := suite.Run()
	if status == 0 {
		t.Fatalf("expected suite to fail (non-zero), but it passed\n%s", out.String())
	}
	if !strings.Contains(out.String(), "result contains") {
		t.Fatalf("expected output to contain 'result contains', got:\n%s", out.String())
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
			w := &world{eng: eng, ctx: context.Background()}

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

// TestResultMeansStep proves the inline and docstring `the result means` steps
// build ResultExpectation{Matcher:"semantic"} with the captured meaning as Want
// and dispatch through the result comparator to the registered "semantic"
// matcher — identical routing to the other result steps. Serial: engine.Build +
// RegisterMatcher mutate package-level registry maps and each subtest drives a
// gomock controller, so no t.Parallel().
func TestResultMeansStep(t *testing.T) {
	tests := []struct {
		name string
		call func(w *world) error
		want string
	}{
		{
			name: "inline form",
			call: func(w *world) error { return w.resultMeans("the answer confirms the booking") },
			want: "the answer confirms the booking",
		},
		{
			name: "docstring form",
			call: func(w *world) error {
				return w.resultMeansDoc(&godog.DocString{Content: "the answer\nspans multiple lines"})
			},
			want: "the answer\nspans multiple lines",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			eng := buildEng(t, happyTrace())
			// engine.Build (inside buildEng) re-registers the real Claude-backed
			// "semantic" matcher into the global registry, clobbering any stand-in.
			// Register the mock AFTER Build so it wins at dispatch time.
			mockMatcher := mocks.NewMockMatcher(ctrl)
			// The result comparator resolves the matcher by name and invokes only
			// Match(ctx, ev, want, target); it never calls Name(). Asserting the
			// captured meaning arrives as `want` is the whole point of this test.
			mockMatcher.EXPECT().
				Match(gomock.Any(), gomock.Any(), tt.want, gomock.Any()).
				Return(core.Verdict{Pass: true}, nil)
			registry.ResetForTest(t) // reopen to override the semantic matcher after Build sealed
			registry.RegisterMatcher("semantic", mockMatcher)

			w := &world{eng: eng}
			w.ev = core.Evidence{Output: core.Output{Answer: "done"}}

			if err := tt.call(w); err != nil {
				t.Fatalf("step returned error: %v", err)
			}
			// ctrl's t.Cleanup asserts Match was called exactly once with tt.want,
			// proving the step routed through w.check("result", ...) with
			// Matcher:"semantic" and the captured meaning as Want.
		})
	}
}

// TestResultMeansDocNil mirrors the other docstring result steps: a nil docstring
// is a descriptive error, not a panic or a silent pass.
func TestResultMeansDocNil(t *testing.T) {
	w := &world{}
	if err := w.resultMeansDoc(nil); err == nil {
		t.Fatal("want error for nil docstring, got nil")
	}
}

// TestResultMeansRejectedInMultirunScenario proves the existing world.check
// @runs(N>1) guard hard-errors for the single-run `the result means` step,
// directing the author to "the runs satisfy" — the same error every other
// single-run result step produces under @runs(N>1).
func TestResultMeansRejectedInMultirunScenario(t *testing.T) {
	w := &world{n: 2}
	err := w.resultMeans("the answer is correct")
	if err == nil {
		t.Fatal("want @runs(2) guard error for single-run step, got nil")
	}
	if !strings.Contains(err.Error(), "@runs(2)") {
		t.Fatalf("expected error to mention @runs(2), got: %v", err)
	}
	if !strings.Contains(err.Error(), "the runs satisfy") {
		t.Fatalf(`expected error to mention "the runs satisfy", got: %v`, err)
	}
}

// --- Feature 003 (US3): scenario context threading ---------------------------

// ctxSpyComparator records the context it is invoked with, to prove the step
// layer threads the scenario context into Compare rather than a fresh Background.
type ctxSpyComparator struct{ gotCtx context.Context }

func (*ctxSpyComparator) Name() string { return "ctx-spy" }
func (s *ctxSpyComparator) Compare(ctx context.Context, _ core.Evidence, _ core.Expectation) (core.Verdict, error) {
	s.gotCtx = ctx
	return core.Verdict{Pass: true}, nil
}

type ctxMarkerKey struct{}

// TestStepsThreadScenarioContext pins feature 003 (US3): drive and compare use the
// world's scenario context, not a discarded context.Background().
func TestStepsThreadScenarioContext(t *testing.T) {
	t.Run("drive receives the scenario context", func(t *testing.T) {
		eng := buildEng(t, happyTrace())
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // a cancelled scenario ctx must reach DriveN, not be replaced by Background
		w := &world{eng: eng, ctx: ctx, target: "svc"}
		err := w.runScenario("happy")
		if err == nil {
			t.Fatal("expected a cancellation error; drive discarded the scenario ctx")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v does not wrap context.Canceled", err)
		}
	})

	t.Run("compare receives the scenario context", func(t *testing.T) {
		spy := &ctxSpyComparator{}
		registry.ResetForTest(t) // reopen to register a test-only comparator seam
		registry.RegisterComparator("ctx-spy", spy)
		eng := buildEng(t, happyTrace())
		ctx := context.WithValue(context.Background(), ctxMarkerKey{}, "scenario-marker")
		w := &world{eng: eng, ctx: ctx}
		if err := w.check("ctx-spy", nil); err != nil {
			t.Fatalf("check: %v", err)
		}
		if spy.gotCtx == nil {
			t.Fatal("comparator received a nil context")
		}
		if got := spy.gotCtx.Value(ctxMarkerKey{}); got != "scenario-marker" {
			t.Fatalf("comparator received %v, not the scenario context (marker=%v)", spy.gotCtx, got)
		}
	})
}
