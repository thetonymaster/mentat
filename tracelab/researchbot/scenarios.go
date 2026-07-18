package researchbot

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

//go:embed scenarios/*.yaml
var scenarioFS embed.FS

func Scenario(name string) (*Plan, error) {
	if name == "" {
		return nil, fmt.Errorf("scenario name required; one of %v", ScenarioNames())
	}
	data, err := scenarioFS.ReadFile("scenarios/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unknown scenario %q; one of %v", name, ScenarioNames())
	}
	return LoadPlan(data)
}

func ScenarioNames() []string {
	var names []string
	_ = fs.WalkDir(scenarioFS, "scenarios", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".yaml") {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(p, "scenarios/"), ".yaml"))
		}
		return nil
	})
	sort.Strings(names)
	return names
}

// LateFlushScenario is the scenario id for the deliberately late-exporting SUT.
// It is NOT an embedded YAML plan (its behaviour is timed export, not a static
// span tree), so it is dispatched by name in cmd/researchbot and does not appear
// in ScenarioNames / the captured fixtures. Use it verbatim as a mentat.yaml
// shell target command argument: ["./bin/researchbot", "--scenario", "late-flush"].
const LateFlushScenario = "late-flush"

// LateFlushDelay is the pause between the decoy batch's force-flush and the
// forbidden delete_record span. It MUST exceed the harness poll config's
// StableFor × interval (mentat.yaml: stableFor 3 × interval 200ms = 600ms) so
// that the feature-002 stability gate ALONE would conclude on the partial
// (decoy-only) forest. That is the whole point of this scenario: it proves the
// 008 settle barrier — not the stability gate — is what holds resolution open
// until the complete forest (including delete_record) is observed. Kept well
// above 600ms to stay past the gate under scheduler jitter.
const LateFlushDelay = 1500 * time.Millisecond

// lateFlushTool is the forbidden tool the delayed batch calls. An absence
// assertion `the tool "delete_record" is never called` MUST FAIL on the
// complete forest once the barrier lets resolution observe this span.
const lateFlushTool = "delete_record"

// lateFlushAnswer is the SUT's final answer, written to stdout after telemetry
// is flushed and shut down (the spawned-target flush-on-exit contract).
const lateFlushAnswer = "Q3 revenue grew 12%"

// EmitLateFlush replays the late-flush scenario into tp's tracer. It emits an
// immediate, well-behaved "decoy" batch (a normal research run) and force-flushes
// it, sleeps for delay so a stability-only gate would conclude on that partial
// forest, then emits a single execute_tool span for the forbidden delete_record
// tool — parented to the (already-ended) decoy root by context so it belongs to
// the same run forest — and force-flushes again. The caller owns the tracer
// provider's shutdown (final flush on exit). delay is a parameter so unit tests
// can run fast; production callers pass LateFlushDelay.
func EmitLateFlush(ctx context.Context, tp *sdktrace.TracerProvider, delay time.Duration) error {
	if tp == nil {
		return fmt.Errorf("emit late-flush: tracer provider is nil")
	}
	tr := tp.Tracer("researchbot")

	// Decoy batch: a normal-looking, well-behaved research run.
	ctx, root := tr.Start(ctx, "invoke_agent researchbot", trace.WithAttributes(
		attribute.String(AttrOp, OpInvokeAgent),
		attribute.String(AttrAgentName, "researchbot"),
		attribute.Int(AttrInTokens, 1200),
		attribute.Int(AttrOutTokens, 600),
		attribute.Float64(AttrCostUSD, 0.018),
	))
	decoy := []struct{ name, args, result string }{
		{"search", "Q3 revenue", "doc-1"},
		{"fetch_doc", "doc-1", "revenue up 12%"},
		{"summarize", "revenue up 12%", lateFlushAnswer},
	}
	for _, d := range decoy {
		_, sp := tr.Start(ctx, "execute_tool "+d.name, trace.WithAttributes(
			attribute.String(AttrOp, OpExecuteTool),
			attribute.String(AttrToolName, d.name),
			attribute.String(AttrToolArgs, d.args),
			attribute.String(AttrToolResult, d.result),
		))
		sp.End()
	}
	root.End()
	if err := tp.ForceFlush(ctx); err != nil {
		return fmt.Errorf("emit late-flush: force-flush decoy batch: %w", err)
	}

	// Barrier defeat: idle past StableFor × interval so a stability-only gate
	// would (wrongly) conclude on the decoy-only forest above.
	time.Sleep(delay)

	// Delayed batch: the forbidden tool call, in the same run forest.
	_, sp := tr.Start(ctx, "execute_tool "+lateFlushTool, trace.WithAttributes(
		attribute.String(AttrOp, OpExecuteTool),
		attribute.String(AttrToolName, lateFlushTool),
		attribute.String(AttrToolArgs, "doc-1"),
		attribute.String(AttrToolResult, "deleted"),
	))
	sp.End()
	if err := tp.ForceFlush(ctx); err != nil {
		return fmt.Errorf("emit late-flush: force-flush %s span: %w", lateFlushTool, err)
	}
	return nil
}

// RunLateFlush executes the late-flush scenario end to end: it builds a tracer
// provider around exp, emits the decoy-then-delayed-forbidden sequence, then
// flushes and shuts the provider down (flush-on-exit, per the spawned-target SUT
// contract) before writing the final answer to stdout. Callers have no handle to
// the provider and must not call Shutdown themselves. delay is the pause between
// the two batches; production callers pass LateFlushDelay.
func RunLateFlush(ctx context.Context, exp sdktrace.SpanExporter, delay time.Duration, stdout, stderr io.Writer) error {
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		return fmt.Errorf("run late-flush: create tracer provider: %w", err)
	}
	if err := EmitLateFlush(ctx, tp, delay); err != nil {
		_ = tp.Shutdown(ctx) // best-effort cleanup; the Emit error is the primary failure
		return fmt.Errorf("run late-flush: emit scenario: %w", err)
	}
	if err := tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("run late-flush: shutdown tracer provider: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, lateFlushAnswer); err != nil {
		return fmt.Errorf("write answer to stdout: %w", err)
	}
	return nil
}

// Strict-mode sentinel scenario ids. Like late-flush, these are NOT embedded
// YAML plans (they stamp the in-trace test.span.count sentinel, which a static
// plan cannot express), so they are dispatched by name in cmd/researchbot and do
// NOT appear in ScenarioNames / the captured fixtures. Use each verbatim as a
// mentat.yaml shell target command argument:
// ["./bin/researchbot", "--scenario", "sentinel-good"].
const (
	// SentinelGoodScenario declares a self-inclusive count equal to the spans it
	// actually emits, with exactly one sentinel span → strict resolution
	// concludes normally.
	SentinelGoodScenario = "sentinel-good"
	// SentinelShortScenario declares MORE spans than it emits (withholds two) →
	// strict resolution times out with the count-short error.
	SentinelShortScenario = "sentinel-short"
	// SentinelDupScenario stamps the sentinel on two spans → strict resolution
	// hard-errors on the ambiguous duplicate declaration.
	SentinelDupScenario = "sentinel-dup"
)

// AttrSpanCount is the strict-mode completeness sentinel attribute key. Its value
// is the total span count of the whole merged run forest — every root — INCLUDING
// the sentinel-bearing span itself (self-inclusive), per contracts §2. It MUST
// match internal/correlate's completenessSentinelKey. Emitted as an integer
// attribute so the correlator reads it via Span.AttrInt.
const AttrSpanCount = "test.span.count"

const (
	// sentinelForestSpans is the number of spans every sentinel scenario's
	// well-behaved research forest actually emits: one invoke_agent root plus
	// three execute_tool children. This is the ACTUALLY-EMITTED count each
	// scenario's declared count is measured against.
	sentinelForestSpans = 4

	// sentinelGoodDeclared is the count sentinel-good stamps: exactly the spans it
	// emits (self-inclusive), so strict resolution reaches equality and concludes.
	sentinelGoodDeclared = sentinelForestSpans

	// sentinelShortDeclared is the count sentinel-short stamps: two MORE than it
	// emits, so the forest never reaches the declared total and strict resolution
	// times out with the count-short error.
	sentinelShortDeclared = sentinelForestSpans + 2

	// sentinelDupDeclared is the count carried by EACH of sentinel-dup's two
	// sentinel spans. The value is well-formed on its own — the hard error is the
	// duplication (two declarations), not the count.
	sentinelDupDeclared = sentinelForestSpans
)

// sentinelAnswer is the SUT's final answer for the sentinel scenarios, written to
// stdout after telemetry is flushed and shut down (the spawned-target
// flush-on-exit contract).
const sentinelAnswer = "Q3 revenue grew 12%"

// EmitSentinel replays a strict-mode sentinel scenario into tp's tracer. It emits
// one well-behaved research forest — an invoke_agent root plus three execute_tool
// children (sentinelForestSpans spans, one run) — and stamps the strict-mode
// test.span.count sentinel on the first len(declared) spans, span i carrying
// value declared[i]. len(declared) sets sentinel cardinality: one entry → a
// single well-formed declaration (good/short); two → the duplicate-declaration
// case (dup). The forest is force-flushed; the caller owns provider shutdown
// (final flush on exit). declared is variadic so a single emitter drives all
// three scenarios from their named count constants.
func EmitSentinel(ctx context.Context, tp *sdktrace.TracerProvider, declared ...int) error {
	if tp == nil {
		return fmt.Errorf("emit sentinel: tracer provider is nil")
	}
	if len(declared) > sentinelForestSpans {
		return fmt.Errorf("emit sentinel: %d sentinels requested but forest has only %d spans", len(declared), sentinelForestSpans)
	}
	tr := tp.Tracer("researchbot")

	ctx, root := tr.Start(ctx, "invoke_agent researchbot", trace.WithAttributes(
		attribute.String(AttrOp, OpInvokeAgent),
		attribute.String(AttrAgentName, "researchbot"),
		attribute.Int(AttrInTokens, 1200),
		attribute.Int(AttrOutTokens, 600),
		attribute.Float64(AttrCostUSD, 0.018),
	))
	tools := []struct{ name, args, result string }{
		{"search", "Q3 revenue", "doc-1"},
		{"fetch_doc", "doc-1", "revenue up 12%"},
		{"summarize", "revenue up 12%", sentinelAnswer},
	}
	// Collect spans in forest order (root first) so the sentinel can be stamped on
	// specific spans by index. Children are parented to the root by ctx, so the
	// whole run is one forest.
	spans := []trace.Span{root}
	for _, tl := range tools {
		_, sp := tr.Start(ctx, "execute_tool "+tl.name, trace.WithAttributes(
			attribute.String(AttrOp, OpExecuteTool),
			attribute.String(AttrToolName, tl.name),
			attribute.String(AttrToolArgs, tl.args),
			attribute.String(AttrToolResult, tl.result),
		))
		spans = append(spans, sp)
	}

	// Stamp the strict-mode sentinel (self-inclusive declared count) on the first
	// len(declared) spans, as an integer attribute the correlator reads via
	// Span.AttrInt.
	for i, d := range declared {
		spans[i].SetAttributes(attribute.Int(AttrSpanCount, d))
	}

	// End children first, then the root — well-formed nesting.
	for i := len(spans) - 1; i >= 0; i-- {
		spans[i].End()
	}
	if err := tp.ForceFlush(ctx); err != nil {
		return fmt.Errorf("emit sentinel: force-flush forest: %w", err)
	}
	return nil
}

// runSentinel executes a strict-mode sentinel scenario end to end: it builds a
// tracer provider around exp, emits the forest with the given declared counts,
// then flushes and shuts the provider down (flush-on-exit, per the spawned-target
// SUT contract) before writing the final answer to stdout. Callers have no handle
// to the provider and must not call Shutdown themselves.
func runSentinel(ctx context.Context, exp sdktrace.SpanExporter, stdout io.Writer, declared ...int) error {
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		return fmt.Errorf("run sentinel (declared %v): create tracer provider: %w", declared, err)
	}
	if err := EmitSentinel(ctx, tp, declared...); err != nil {
		_ = tp.Shutdown(ctx) // best-effort cleanup; the Emit error is the primary failure
		return fmt.Errorf("run sentinel (declared %v): emit scenario: %w", declared, err)
	}
	if err := tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("run sentinel (declared %v): shutdown tracer provider: %w", declared, err)
	}
	if _, err := fmt.Fprintln(stdout, sentinelAnswer); err != nil {
		return fmt.Errorf("write answer to stdout: %w", err)
	}
	return nil
}

// RunSentinelGood runs the sentinel-good scenario: a complete forest whose
// self-inclusive span count equals its single declared test.span.count → strict
// resolution concludes normally. stderr matches the RunLateFlush dispatch shape.
func RunSentinelGood(ctx context.Context, exp sdktrace.SpanExporter, stdout, stderr io.Writer) error {
	return runSentinel(ctx, exp, stdout, sentinelGoodDeclared)
}

// RunSentinelShort runs the sentinel-short scenario: it declares
// sentinelShortDeclared spans but emits only sentinelForestSpans → strict
// resolution times out with the count-short error.
func RunSentinelShort(ctx context.Context, exp sdktrace.SpanExporter, stdout, stderr io.Writer) error {
	return runSentinel(ctx, exp, stdout, sentinelShortDeclared)
}

// RunSentinelDup runs the sentinel-dup scenario: two spans each carry
// test.span.count → strict resolution hard-errors on the ambiguous duplicate.
func RunSentinelDup(ctx context.Context, exp sdktrace.SpanExporter, stdout, stderr io.Writer) error {
	return runSentinel(ctx, exp, stdout, sentinelDupDeclared, sentinelDupDeclared)
}
