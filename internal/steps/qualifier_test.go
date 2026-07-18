package steps

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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

// qualEvidence builds an agent run whose forest is rich enough that every comparator
// under test returns a VERDICT (never an error): tools search+summarize, 1800 tokens,
// a cost attribute, and real timestamps for a bounded latency envelope. The qualifier
// is attached only when a comparator returns a verdict, so evidence that made a
// comparator error would mask its sensitivity.
func qualEvidence() core.Evidence {
	t0 := time.Unix(0, 0)
	root := &trace.Span{
		Name: "invoke_agent", Start: t0, End: t0.Add(50 * time.Millisecond),
		Attrs: map[string]string{
			genai.Op:        genai.OpInvokeAgent,
			genai.InTokens:  "1200",
			genai.OutTokens: "600",
			genai.CostUSD:   "0.01",
		},
	}
	mk := func(tool string, start time.Time) *trace.Span {
		return &trace.Span{
			Name: "execute_tool " + tool, Start: start, End: start.Add(5 * time.Millisecond),
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: tool},
		}
	}
	tr := &trace.Trace{
		Roots: []*trace.Span{root},
		Spans: []*trace.Span{root, mk("search", t0.Add(5*time.Millisecond)), mk("summarize", t0.Add(15*time.Millisecond))},
	}
	return core.Evidence{RunID: "r", Trace: tr, Output: core.Output{Answer: "done", Status: 200}}
}

// qualPassMatcher is a trivial "semantic" matcher stub returning pass, so the
// non-sensitive `the result means` row can be exercised hermetically (no judge/Claude
// call). No call-count verification is needed, so a value stub is sufficient.
type qualPassMatcher struct{}

func (qualPassMatcher) Name() string { return "semantic" }
func (qualPassMatcher) Match(context.Context, core.Evidence, string, string) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

// qualTable builds a single-column godog table from the given cell values.
func qualTable(vals ...string) *godog.Table {
	tbl := &godog.Table{}
	for _, v := range vals {
		tbl.Rows = append(tbl.Rows, &messages.PickleTableRow{Cells: []*messages.PickleTableCell{{Value: v}}})
	}
	return tbl
}

// boundedHTTPEngine builds an engine with a single request-scoped (http, 5s settle →
// bounded) target "web" plus a stub "semantic" matcher, for exercising step
// sensitivity against a bounded contract.
func boundedHTTPEngine(t *testing.T) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			"web": {Adapter: "http", MaxConcurrency: 1, Completeness: config.Completeness{Mode: "settle", Settle: 5 * time.Second}},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := engine.Build(cfg, st, cor, engine.WithExtraMatcher("semantic", qualPassMatcher{}))
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

// TestStepCompletenessSensitivity pins which step phrasings mark their expectation
// completeness-sensitive (T014): absence, exact-count, budget/error-count,
// CEL-aggregate, and upper-bound COUNT (retry "at most N") steps DO — so against a
// bounded (request-scoped) target they record the ingestion-window qualifier;
// presence, ordering, lower-bound counts, value matches (contains), semantic (the
// result means), and single-run CEL do NOT. The qualifier is recorded on pass AND fail, so each row uses evidence that
// yields a verdict (never a comparator error) and asserts only whether a qualifier
// was recorded — the step's own pass/fail is irrelevant.
func TestStepCompletenessSensitivity(t *testing.T) {
	ev := qualEvidence() // tools search+summarize; 1800 tokens; cost 0.01; 50ms envelope

	tests := []struct {
		name          string
		call          func(w *world) error
		wantSensitive bool
	}{
		// --- completeness-sensitive ---
		{"absence: tool never called", func(w *world) error { return w.toolNeverCalled("delete_record") }, true},
		{"absence: shape absent", func(w *world) error { return w.shapeAbsent("gen_ai.tool.name=delete_record") }, true},
		{"exact-count: shape exactly", func(w *world) error { return w.shapeExactly(1, "gen_ai.tool.name=search") }, true},
		{"budget: tokens under", func(w *world) error { return w.tokensUnder(5000) }, true},
		{"budget: cost under", func(w *world) error { return w.costUnder(1.0) }, true},
		{"budget: latency under", func(w *world) error { return w.latencyUnder(60000) }, true},
		{"error-count: no error spans", func(w *world) error { return w.noErrorSpans() }, true},
		{"cel-aggregate: runs satisfy", func(w *world) error { return w.runsSatisfies("count(r, 'search' in r.tools) >= 0") }, true},
		// upper-bound COUNT: "at most N" undercounts on a partial forest, so a green
		// verdict here can be falsified by a late span — sensitive like its sibling
		// `toolNeverCalled`. On this bounded (http, 5s settle) target a passing
		// at-most-N verdict must carry the ingestion-window qualifier.
		{"upper-bound count: tool called at most", func(w *world) error { return w.toolCalledAtMost("search", 5) }, true},

		// --- NOT completeness-sensitive ---
		{"presence: tools in order", func(w *world) error { return w.toolsInOrder(qualTable("search", "summarize")) }, false},
		{"presence: shape exists", func(w *world) error { return w.shapeExists("gen_ai.tool.name=search") }, false},
		{"lower-bound: shape at least", func(w *world) error { return w.shapeAtLeast(1, "gen_ai.tool.name=search") }, false},
		{"contains: result contains", func(w *world) error { return w.resultContains("done") }, false},
		{"semantic: result means", func(w *world) error { return w.resultMeans("the answer is done") }, false},
		{"single-run cel: run satisfies", func(w *world) error { return w.runSatisfies("tokens < 5000") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := boundedHTTPEngine(t)
			w := &world{eng: eng, ctx: context.Background(), target: "web", ev: ev, evs: []core.Evidence{ev, ev}}
			_ = tt.call(w) // pass/fail irrelevant; only qualifier recording matters
			gotSensitive := len(w.lastQualifiers) > 0
			if gotSensitive != tt.wantSensitive {
				t.Fatalf("recorded qualifiers=%v, wantSensitive=%v", w.lastQualifiers, tt.wantSensitive)
			}
			if tt.wantSensitive {
				q := w.lastQualifiers[0]
				if !strings.Contains(q, "trace-completeness") || !strings.Contains(q, "settle 5s") {
					t.Fatalf("qualifier = %q, want canonical text naming the 5s settle window", q)
				}
			}
		})
	}
}

// TestInitializer_CarriesBoundedQualifierToReport is the end-to-end guard for E1: a
// completeness-sensitive (absence) assertion against a bounded (http, 5s settle)
// target drives a real godog scenario, and the engine-attached qualifier flows through
// the After hook and report.Derive into the collected ScenarioResult — on a PASSING
// scenario. This exercises the steps→report wiring the unit tests above cannot.
func TestInitializer_CarriesBoundedQualifierToReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			// A small settle keeps the settle-window barrier (US1) fast while still
			// exercising the bounded-contract qualifier end-to-end; the qualifier echoes
			// whatever the effective settle is (here 20ms).
			"web": {
				Adapter:        "http",
				MaxConcurrency: 1,
				HTTP:           config.HTTP{URL: srv.URL, Method: http.MethodPost},
				Completeness:   config.Completeness{Mode: "settle", Settle: 20 * time.Millisecond},
			},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	stubStoredTrace(st, happyTrace()) // tools search/summarize, no delete_record
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	col := report.NewCollector()
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := `Feature: bounded
  Scenario: absence assertion on a bounded target passes and is qualified
    Given the service target "web"
    When I run scenario "happy"
    Then the tool "delete_record" is never called
`
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: InitializerWithCollector(eng, col),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			Strict:          true,
			FeatureContents: []godog.Feature{{Name: "bounded", Contents: []byte(feature)}},
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("expected a passing suite, status=%d\n%s", status, out.String())
	}

	rep := col.Report(time.Unix(0, 0), 0, false)
	if rep.Total != 1 {
		t.Fatalf("collector got %d scenarios, want 1", rep.Total)
	}
	sr := rep.Scenarios[0]
	if !sr.Pass {
		t.Fatalf("scenario Pass=false, want true\n%s", out.String())
	}
	if len(sr.Qualifiers) == 0 {
		t.Fatalf("ScenarioResult.Qualifiers empty; the bounded qualifier did not reach the report")
	}
	if !strings.Contains(sr.Qualifiers[0], "trace-completeness") || !strings.Contains(sr.Qualifiers[0], "settle 20ms") {
		t.Fatalf("Qualifiers[0] = %q, want the canonical text with the effective settle (20ms)", sr.Qualifiers[0])
	}
}
