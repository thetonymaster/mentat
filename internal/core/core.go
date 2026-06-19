package core

//go:generate mockgen -source=core.go -destination=mocks/mock_core.go -package=mocks

import (
	"context"
	"strings"

	"github.com/thetonymaster/mentat/internal/trace"
)

// Output is the driver-captured boundary result of a run.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int    // shell adapters
	Status   int    // http adapters (HTTP status)
	Body     []byte // http adapters
	Answer   string // extracted result (see ExtractAnswer)
}

// FailureKind classifies a harness-level run failure by which engine call failed
// (§6). It is the value of Evidence.FailureKind for a failed run.
const (
	FailureKindDriver  = "driver"  // driver invocation failed
	FailureKindResolve = "resolve" // trace resolution failed
)

// Evidence is everything a comparator may inspect about a single run.
type Evidence struct {
	RunID  string
	Trace  *trace.Trace
	Output Output
	// Failed marks a harness-level failure for this run (driver invocation or trace
	// resolution). A failed run carries no Trace. FailureKind is "" when not failed,
	// else "driver" or "resolve" (classified by which engine call failed, §6).
	Failed      bool
	FailureKind string
}

type Verdict struct {
	Pass    bool
	Reasons []string
}

// Expectation is comparator-specific config; each comparator type-asserts its own.
type Expectation = any

type Comparator interface {
	Name() string
	Compare(ctx context.Context, ev Evidence, e Expectation) (Verdict, error)
}

// AggregateComparator asserts a property across the N Evidence values of a
// multi-run (@runs) scenario. It is a sibling of Comparator, not a replacement:
// the single-Evidence Comparator and every existing comparator are unchanged.
type AggregateComparator interface {
	Name() string
	Aggregate(ctx context.Context, evs []Evidence, e Expectation) (Verdict, error)
}

// RunSpec is the driver input. The adapter applies RunID/Tags via its transport.
type RunSpec struct {
	Target  string
	Adapter string
	Command []string // shell adapter argv; http adapter parses --scenario from it
	Env     map[string]string
	Input   string // prompt / request body
	HTTP    HTTPSpec
	RunID   string
	Tags    map[string]string // test.run.id, test.scenario, test.case
}

// HTTPSpec is the http adapter's per-target request config (mirrors config.HTTP,
// kept in core so the driver has no dependency on the config layer).
type HTTPSpec struct {
	URL     string
	Method  string
	Headers map[string]string
}

// ModelRate is a per-model price in USD per million tokens (spec §4.1). Mirrors
// config.ModelRate, kept in core so the comparator layer never imports config.
type ModelRate struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Pricing maps a gen_ai.request.model value to its rate. Used to derive cost
// from token counts when a span carries no emitted gen_ai.usage.cost_usd (§4.3).
type Pricing map[string]ModelRate

type RunResult struct {
	RunID string
	// PrimaryTraceID is reserved for a future traceparent complement (spec §5):
	// a clean primary trace id for when a SUT adopts an injected traceparent. It
	// is intentionally left unset under the baggage-only correlation path that
	// ships today — baggage tag-first correlation is the invariant (it survives
	// the SUT rooting its own trace, which traceparent alone cannot), and nothing
	// in correlate.Resolve consumes this field. A second correlator (the
	// traceparent complement) will populate it and add a fast-path in Resolve;
	// until that consumer exists, injecting it would be a feature with no reader
	// (YAGNI).
	PrimaryTraceID string
	Output         Output
}

type Driver interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

type TraceQuery struct {
	Tag   string // e.g. "test.run.id"
	Value string
}

type TraceRef struct{ TraceID string }

type StoreCaps struct{ StructuralQuery bool }

type TraceStore interface {
	GetByID(ctx context.Context, id string) (*trace.Trace, error)
	Query(ctx context.Context, q TraceQuery) ([]TraceRef, error)
	Caps() StoreCaps
}

type Correlator interface {
	Inject(ctx context.Context, spec *RunSpec) (runID string)
	Resolve(ctx context.Context, store TraceStore, runID string) (*trace.Trace, error)
}

// Matcher is one strategy inside the result comparator. It reads the run's
// Output (selected by target for value matchers; Body/Status for structural
// matchers) and returns a Verdict. Matchers are stateless and registered as
// shared instances at the composition root.
type Matcher interface {
	Name() string
	Match(ctx context.Context, ev Evidence, want, target string) (Verdict, error)
}

// ExtractAnswer applies the project-wide convention: stdout is the result.
func ExtractAnswer(stdout string) string { return strings.TrimSpace(stdout) }
