package researchbot

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func toolNames(p *Plan) []string {
	var n []string
	for _, s := range p.Steps {
		if s.Tool != nil {
			n = append(n, s.Tool.Name)
		}
	}
	return n
}

func TestScenariosCoverPassAndFailPaths(t *testing.T) {
	all := ScenarioNames()
	if len(all) != 5 {
		t.Fatalf("want 5 scenarios, got %v", all)
	}

	happy, err := Scenario("happy")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got := toolNames(happy); strings.Join(got, ",") != "search,fetch_doc,summarize" {
		t.Fatalf("happy tools = %v", got)
	}
	if happy.Tokens.Input+happy.Tokens.Output >= 5000 {
		t.Fatal("happy should be under budget")
	}
	if !strings.Contains(happy.Output, "Q3 revenue") {
		t.Fatalf("happy output = %q", happy.Output)
	}

	extra, err := Scenario("extra_tool")
	if err != nil {
		t.Fatalf("extra_tool: %v", err)
	}
	if !contains(toolNames(extra), "delete_record") {
		t.Fatal("extra_tool must call delete_record")
	}

	wrong, err := Scenario("wrong_order")
	if err != nil {
		t.Fatalf("wrong_order: %v", err)
	}
	tn := toolNames(wrong)
	if indexOf(tn, "summarize") > indexOf(tn, "search") {
		t.Fatal("wrong_order must summarize before search")
	}

	over, err := Scenario("over_budget")
	if err != nil {
		t.Fatalf("over_budget: %v", err)
	}
	if over.Tokens.Input+over.Tokens.Output < 5000 {
		t.Fatal("over_budget must exceed 5000 tokens")
	}

	bad, err := Scenario("bad_answer")
	if err != nil {
		t.Fatalf("bad_answer: %v", err)
	}
	if strings.Contains(bad.Output, "Q3 revenue") {
		t.Fatal("bad_answer output must not contain the good answer")
	}
}

// TestLateFlushEmitsDecoyAndForbiddenSpan pins that the complete emission of the
// late-flush scenario is correct: BOTH the immediate decoy batch AND the delayed
// delete_record span are produced, in one run forest. Resolution soundness (the
// barrier that forces this whole forest to be observed) is proven by the e2e
// meta-test; this test only guards that the SUT actually emits the forbidden
// span, so a green absence verdict can only be a barrier bug, never a missing
// span. A tiny delay keeps it fast; production uses LateFlushDelay.
func TestLateFlushEmitsDecoyAndForbiddenSpan(t *testing.T) {
	ctx := context.Background()
	exp := tracetest.NewInMemoryExporter()
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	if err := EmitLateFlush(ctx, tp, time.Millisecond); err != nil {
		t.Fatalf("EmitLateFlush: %v", err)
	}
	// Collect BEFORE Shutdown: InMemoryExporter.Shutdown resets its store.
	spans := exp.GetSpans()
	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	idx := byNameUniq(t, spans)

	// The decoy batch: a normal-looking research run (root + three tool spans).
	root, ok := idx["invoke_agent researchbot"]
	if !ok {
		t.Fatal("missing decoy root span invoke_agent researchbot")
	}
	for _, decoy := range []string{"execute_tool search", "execute_tool fetch_doc", "execute_tool summarize"} {
		if _, ok := idx[decoy]; !ok {
			t.Fatalf("missing decoy span %q", decoy)
		}
	}

	// The delayed batch: the forbidden tool. This is the span that makes an
	// absence assertion FAIL on the complete forest.
	forbiddenName := "execute_tool " + lateFlushTool
	forbidden, ok := idx[forbiddenName]
	if !ok {
		t.Fatalf("missing forbidden span %q; late-flush must emit the delayed %s call", forbiddenName, lateFlushTool)
	}
	if got := requireAttrString(t, forbidden, AttrOp); got != OpExecuteTool {
		t.Fatalf("forbidden span %s: want %q, got %q", AttrOp, OpExecuteTool, got)
	}
	if got := requireAttrString(t, forbidden, AttrToolName); got != lateFlushTool {
		t.Fatalf("forbidden span %s: want %q, got %q", AttrToolName, lateFlushTool, got)
	}

	// Both batches share one run forest: the forbidden span is a child of the
	// decoy root, so an Evidence-only absence comparator sees it.
	if forbidden.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatalf("forbidden span parent=%v, want decoy root %v",
			forbidden.Parent.SpanID(), root.SpanContext.SpanID())
	}

	// Guard the barrier contract the next task tunes its settle window around:
	// the delay MUST exceed the harness StableFor × interval so a stability-only
	// gate would conclude on the decoy-only forest. mentat.yaml: 3 × 200ms.
	const stableForTimesInterval = 3 * 200 * time.Millisecond
	if LateFlushDelay <= stableForTimesInterval {
		t.Fatalf("LateFlushDelay=%v must exceed StableFor×interval=%v", LateFlushDelay, stableForTimesInterval)
	}
}

// TestRunLateFlushFlushesOnExitAndWritesAnswer pins the SUT contract: RunLateFlush
// exports the whole forest (decoy batch + forbidden span) and shuts the provider
// down (flush-on-exit) before writing the answer to stdout.
func TestRunLateFlushFlushesOnExitAndWritesAnswer(t *testing.T) {
	var out, errBuf bytes.Buffer
	rec := newRecordingExporter()
	if err := RunLateFlush(context.Background(), rec, time.Millisecond, &out, &errBuf); err != nil {
		t.Fatalf("RunLateFlush: %v", err)
	}
	if out.String() == "" {
		t.Fatal("RunLateFlush wrote no answer to stdout")
	}
	// decoy root + 3 decoy tools + 1 forbidden = 5 spans exported.
	if got := rec.received.Load(); got < 5 {
		t.Fatalf("expected ≥5 spans exported (decoy batch + forbidden), got %d", got)
	}
	if !rec.shutdownCalled.Load() {
		t.Fatal("provider Shutdown was not called — flush-on-exit contract violated")
	}
}

func TestEmitLateFlushNilProviderReturnsError(t *testing.T) {
	if err := EmitLateFlush(context.Background(), nil, time.Millisecond); err == nil {
		t.Fatal("expected error for nil tracer provider, got nil")
	}
}

func TestRunLateFlushPropagatesStdoutWriteError(t *testing.T) {
	var errBuf bytes.Buffer
	rec := newRecordingExporter()
	if err := RunLateFlush(context.Background(), rec, time.Millisecond, failingWriter{}, &errBuf); err == nil {
		t.Fatal("expected error when stdout write fails, got nil")
	}
	// RunLateFlush shuts the provider down before writing the answer, so cleanup
	// must have happened even though the write itself failed.
	if !rec.shutdownCalled.Load() {
		t.Fatal("provider Shutdown was not called on the stdout-write error path — resource leak")
	}
}

func TestScenarioRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "nonexistent"} {
		p, err := Scenario(name)
		if err == nil {
			t.Fatalf("Scenario(%q): want error, got nil", name)
		}
		if p != nil {
			t.Fatalf("Scenario(%q): want nil plan on error, got %+v", name, p)
		}
	}
}

// retainingExporter keeps every exported span stub, and remembers whether
// Shutdown was called, so a test can inspect the full emitted forest AFTER the
// provider is shut down (unlike InMemoryExporter, which resets on Shutdown). This
// lets the sentinel tests assert emission by driving the real Run* wrappers,
// which own the provider lifecycle (flush + shutdown) end to end.
type retainingExporter struct {
	mu             sync.Mutex
	spans          tracetest.SpanStubs
	shutdownCalled bool
}

func (r *retainingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, tracetest.SpanStubsFromReadOnlySpans(spans)...)
	return nil
}

func (r *retainingExporter) Shutdown(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdownCalled = true
	return nil
}

func (r *retainingExporter) snapshot() tracetest.SpanStubs {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(tracetest.SpanStubs, len(r.spans))
	copy(out, r.spans)
	return out
}

func (r *retainingExporter) didShutdown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shutdownCalled
}

// sentinelValues returns the test.span.count value of every sentinel-bearing span
// in the forest, so tests can assert both cardinality (len) and the declared
// counts (values).
func sentinelValues(spans tracetest.SpanStubs) []int64 {
	var vals []int64
	for _, s := range spans {
		for _, kv := range s.Attributes {
			if string(kv.Key) == AttrSpanCount {
				vals = append(vals, kv.Value.AsInt64())
			}
		}
	}
	return vals
}

// TestSentinelScenariosEmitDeclaredCounts pins the strict-mode sentinel emission
// for all three scenarios, driven through the real Run* wrappers (so the named
// scenario → declared-count wiring is exercised, not just EmitSentinel). It
// asserts the total forest span count (self-inclusive, fixed), the sentinel
// cardinality (good/short = 1, dup = 2), and the test.span.count value(s), plus
// the flush-on-exit contract and the stdout answer. The strict resolution
// OUTCOMES (good concludes, short times out, dup hard-errors) are proven by the
// correlate unit tests and the e2e strict meta-tests; this test guards that the
// SUT actually emits what those outcomes depend on.
func TestSentinelScenariosEmitDeclaredCounts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		run           func(context.Context, sdktrace.SpanExporter, io.Writer, io.Writer) error
		wantSentinels int
		wantDeclared  int64
	}{
		{
			name:          SentinelGoodScenario,
			run:           RunSentinelGood,
			wantSentinels: 1,
			wantDeclared:  sentinelGoodDeclared,
		},
		{
			name:          SentinelShortScenario,
			run:           RunSentinelShort,
			wantSentinels: 1,
			wantDeclared:  sentinelShortDeclared,
		},
		{
			name:          SentinelDupScenario,
			run:           RunSentinelDup,
			wantSentinels: 2,
			wantDeclared:  sentinelDupDeclared,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out, errBuf bytes.Buffer
			rec := &retainingExporter{}
			if err := tt.run(context.Background(), rec, &out, &errBuf); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}

			// Flush-on-exit contract (spawned-target SUT): the provider must be
			// shut down before the answer is written.
			if !rec.didShutdown() {
				t.Fatal("provider Shutdown was not called — flush-on-exit contract violated")
			}
			if out.String() != sentinelAnswer+"\n" {
				t.Fatalf("stdout = %q, want %q", out.String(), sentinelAnswer+"\n")
			}

			spans := rec.snapshot()

			// Total forest span count is self-inclusive and fixed across scenarios;
			// only the DECLARED count varies. For sentinel-short this proves the
			// forest is genuinely short of its declaration.
			if len(spans) != sentinelForestSpans {
				t.Fatalf("emitted %d spans, want %d", len(spans), sentinelForestSpans)
			}

			// Sentinel cardinality: good/short declare once, dup declares twice.
			vals := sentinelValues(spans)
			if len(vals) != tt.wantSentinels {
				t.Fatalf("%s: found %d %s sentinels, want %d", tt.name, len(vals), AttrSpanCount, tt.wantSentinels)
			}
			for _, v := range vals {
				if v != tt.wantDeclared {
					t.Fatalf("%s: sentinel %s = %d, want declared %d", tt.name, AttrSpanCount, v, tt.wantDeclared)
				}
			}
		})
	}
}

// TestSentinelDeclaredCountsMatchIntent locks the declared-vs-emitted relationship
// each scenario's strict outcome depends on, independent of emission plumbing:
// good declares exactly what it emits, short declares strictly more (so it can
// never reach equality), and dup's per-span value is well-formed on its own.
func TestSentinelDeclaredCountsMatchIntent(t *testing.T) {
	if sentinelGoodDeclared != sentinelForestSpans {
		t.Fatalf("sentinel-good must declare exactly the %d spans it emits, declares %d", sentinelForestSpans, sentinelGoodDeclared)
	}
	if sentinelShortDeclared <= sentinelForestSpans {
		t.Fatalf("sentinel-short must declare MORE than the %d spans it emits, declares %d", sentinelForestSpans, sentinelShortDeclared)
	}
	if sentinelDupDeclared != sentinelForestSpans {
		t.Fatalf("sentinel-dup per-span declaration should be well-formed (%d), got %d", sentinelForestSpans, sentinelDupDeclared)
	}
}

func TestRunSentinelPropagatesStdoutWriteError(t *testing.T) {
	var errBuf bytes.Buffer
	rec := newRecordingExporter()
	if err := RunSentinelGood(context.Background(), rec, failingWriter{}, &errBuf); err == nil {
		t.Fatal("expected error when stdout write fails, got nil")
	}
	// runSentinel shuts the provider down before writing the answer, so cleanup
	// must have happened even though the write itself failed.
	if !rec.shutdownCalled.Load() {
		t.Fatal("provider Shutdown was not called on the stdout-write error path — resource leak")
	}
}

func TestEmitSentinelNilProviderReturnsError(t *testing.T) {
	if err := EmitSentinel(context.Background(), nil, sentinelGoodDeclared); err == nil {
		t.Fatal("expected error for nil tracer provider, got nil")
	}
}

func TestEmitSentinelRejectsMoreSentinelsThanSpans(t *testing.T) {
	ctx := context.Background()
	tp, err := NewTracerProvider(ctx, tracetest.NewInMemoryExporter())
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })
	tooMany := make([]int, sentinelForestSpans+1)
	if err := EmitSentinel(ctx, tp, tooMany...); err == nil {
		t.Fatalf("expected error requesting %d sentinels for a %d-span forest, got nil", len(tooMany), sentinelForestSpans)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
