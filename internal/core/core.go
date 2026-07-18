package core

//go:generate mockgen -source=core.go -destination=mocks/mock_core.go -package=mocks

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

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
	// resolution). A failed run carries no Trace. On a RESOLVE failure it still
	// RETAINS the real driver Output (the driver succeeded); on a DRIVER failure the
	// Output is zero. FailureKind is "" when not failed, else "driver" or "resolve"
	// (classified by which engine call failed, §6).
	Failed      bool
	FailureKind string
	// FailureMsg is the wrapped error text from the failing engine call; non-empty
	// iff Failed.
	FailureMsg string
}

type Verdict struct {
	Pass    bool
	Reasons []string
	// Detail is the structured computed-vs-expected result of a canonical aggregate
	// (@runs) comparison. Non-nil only for that case; every other comparator leaves it nil.
	Detail *AggregateDetail
	// Judge is the summed judge-token ledger for this verdict — non-nil only when the
	// comparator actually issued judge calls (the semantic matcher sums it across its
	// best-of-N votes). A comparator that makes no judge call leaves it nil: absence
	// of usage is not zero usage (no fabricated zeros — judge-ledger contract, FR-006).
	Judge *JudgeUsage
}

// JudgeUsage is a summable ledger row of judge-model token consumption (US6). It is a
// value type: zero value is the additive identity, and rows sum field-wise (Model is
// the judge model id, required so the later cost step prices per-model via the pricing
// table). Calls is the number of judge calls the row accounts for (1 for a single call;
// N after summing N votes). Never fabricate a row for a call that did not happen.
//
// CostUsd is the derived cost in USD. It is 0 until priced at the render/budget
// boundary (report.Price / report.Budget) — the matcher and judge never set it,
// because pricing is not their concern (Constitution I). The json tags render the
// judge-ledger contract shape (calls/inputTokens/outputTokens/costUsd/model); the
// suite total omits model (left empty so `omitempty` drops the key).
type JudgeUsage struct {
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUsd      float64 `json:"costUsd"`
	Model        string  `json:"model,omitempty"`
}

// AggregateDetail is the structured result of a canonical aggregate comparison
// ("‹macro›(r, proj) ‹op› ‹const›"). PerRun is positionally aligned with the runs order;
// predicate macros (rate/count) contribute 1.0/0.0.
type AggregateDetail struct {
	Expr     string
	Macro    string
	Op       string
	Computed float64
	Expected float64
	PerRun   []float64
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
	// KillGrace is the grace period between the polite termination signal and the
	// forceful kill of the SUT process tree, and the pipe WaitDelay (feature 003).
	// The engine resolves it from the target's run budget and passes it here so a
	// driver honours the lifecycle policy without importing config. Zero means the
	// driver applies no extra grace/WaitDelay (the pre-feature-003 behaviour).
	KillGrace time.Duration
	// Extract is the answer-extraction policy the driver applies to stdout (US8).
	// The zero value is ExtractWhole (TrimSpace), so a spec built without it keeps
	// today's behaviour byte-for-byte. Only the shell adapter consults it — an
	// http adapter's answer is its response body, not a stdout concept.
	Extract ExtractPolicy
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
	// FetchPayload returns the store's raw payload bytes for a trace id — the
	// per-round change-detection signal of the stability poll (feature 004,
	// FR-002). Tempo: the exact /api/traces/{id} response body. Stores with no
	// wire payload (InMemStore, mocks): a deterministic canonical serialization
	// of the stored forest (content-identical ⇒ byte-identical). A store that
	// cannot produce payload bytes returns an error, never (nil, nil).
	FetchPayload(ctx context.Context, id string) ([]byte, error)
	// DecodePayload decodes payload bytes previously returned by FetchPayload
	// for the same id into a Trace forest. It must not fetch: the hashed bytes
	// and the decoded bytes are the same fetch (no partial-evidence window).
	DecodePayload(id string, payload []byte) (*trace.Trace, error)
	Query(ctx context.Context, q TraceQuery) ([]TraceRef, error)
	Caps() StoreCaps
}

type Correlator interface {
	Inject(ctx context.Context, spec *RunSpec) (runID string)
	Resolve(ctx context.Context, store TraceStore, runID string) (*trace.Trace, error)
	// ResolveComplete is the known-complete resolution mode for saved/historical
	// runs (feature 004, FR-004, audit C4): one tag query + one concurrent fetch
	// pass, no stability loop, no sleep. An absent trace is the same descriptive
	// not-found error as live mode. Live scenario resolution MUST NOT call it —
	// it is a separate seam method (not a flag) precisely so accidental live use
	// is a compile-time impossibility (research R4).
	ResolveComplete(ctx context.Context, store TraceStore, runID string) (*trace.Trace, error)
}

// Matcher is one strategy inside the result comparator. It reads the run's
// Output (selected by target for value matchers; Body/Status for structural
// matchers) and returns a Verdict. Matchers are stateless and registered as
// shared instances at the composition root.
type Matcher interface {
	Name() string
	Match(ctx context.Context, ev Evidence, want, target string) (Verdict, error)
}

// Judge renders a single semantic verdict over two strings. It is a seam: the
// default backend calls Claude, but it is swappable and gomock-able. It receives
// NO Evidence / TraceStore / Driver — only the candidate and the expected meaning.
type Judge interface {
	Judge(ctx context.Context, req JudgeRequest) (JudgeVerdict, error)
}

// JudgeRequest is the matter to be judged. Plain strings keep the Judge transport-
// and Evidence-free (Constitution I): the matcher extracts Candidate from
// Evidence.Output and Expected from the expectation.
type JudgeRequest struct {
	Candidate string // the run's result content (Evidence.Output.Answer)
	Expected  string // the author's expected meaning (from `the result means "..."`)
}

// JudgeVerdict is the structured answer — exactly match + reason (no confidence in v1).
type JudgeVerdict struct {
	Match  bool
	Reason string // human-readable rationale; flows into Verdict.Reasons on a fail (FR-008)
	// Usage is this single call's token ledger (Calls=1) with the judge model id,
	// captured from the backend response. The semantic matcher sums it across votes
	// into Verdict.Judge (US6). Zero value on non-metered/error paths.
	Usage JudgeUsage
}

// Extraction modes (US8, data-model "Answer extraction"). The zero value ("")
// is treated as ExtractWhole so a hand-built RunSpec and a target with no
// `extract` config keep today's TrimSpace behaviour byte-for-byte.
const (
	ExtractWhole   = "whole"   // trimmed full stdout (default; never fails)
	ExtractMarker  = "marker"  // text after the LAST occurrence of Marker, trimmed
	ExtractPattern = "pattern" // first capture group of the first Pattern match
)

// ExtractPolicy parameterizes ExtractAnswer (US8). It is a value type: the zero
// value (Mode == "") behaves as ExtractWhole. Marker is required for ExtractMarker;
// Pattern is a precompiled regexp with at least one capture group, required for
// ExtractPattern. The config layer compiles Pattern once at load and rides the
// compiled regexp here so extraction never recompiles per run.
type ExtractPolicy struct {
	Mode    string
	Marker  string
	Pattern *regexp.Regexp
}

// ExtractAnswer derives the run's answer from stdout under policy. The default
// (whole) mode never fails. A marker or pattern that cannot be resolved is a hard,
// descriptive error naming the offending marker/pattern — never a silent empty
// answer (Constitution IV): an unresolvable extraction is a run failure, not an
// empty-string success.
func ExtractAnswer(stdout string, policy ExtractPolicy) (string, error) {
	switch policy.Mode {
	case "", ExtractWhole:
		return strings.TrimSpace(stdout), nil
	case ExtractMarker:
		if policy.Marker == "" {
			return "", fmt.Errorf("extract: marker mode requires a non-empty marker")
		}
		idx := strings.LastIndex(stdout, policy.Marker)
		if idx < 0 {
			return "", fmt.Errorf("extract: marker %q not found in output", policy.Marker)
		}
		return strings.TrimSpace(stdout[idx+len(policy.Marker):]), nil
	case ExtractPattern:
		if policy.Pattern == nil {
			return "", fmt.Errorf("extract: pattern mode requires a compiled pattern")
		}
		m := policy.Pattern.FindStringSubmatch(stdout)
		if m == nil {
			return "", fmt.Errorf("extract: pattern \"%s\" found no match in output", policy.Pattern.String())
		}
		if len(m) < 2 {
			return "", fmt.Errorf("extract: pattern \"%s\" has no capture group", policy.Pattern.String())
		}
		return m[1], nil
	default:
		return "", fmt.Errorf("extract: unknown mode %q (want %q, %q, or %q)", policy.Mode, ExtractWhole, ExtractMarker, ExtractPattern)
	}
}

// RunReport is the whole-run artifact a Reporter renders. Pure data.
type RunReport struct {
	Scenarios []ScenarioResult
	Total     int
	Passed    int
	Failed    int
	TotalCost float64
	StartedAt time.Time
	Duration  time.Duration
	// Interrupted marks a run that a SIGINT/SIGTERM cancelled before it ran to
	// completion (feature 003, FR-006). The report then carries the scenarios that
	// finished plus this explicit marker; omitted from a clean run's JSON.
	Interrupted bool `json:"interrupted,omitempty"`
	// JudgeTotal is the suite-wide judge-token ledger, summed field-wise across the
	// scenarios that made judge calls (US6). Non-nil ONLY when at least one scenario
	// issued a judge call — absence of usage is not a fabricated all-zero total
	// (judge-ledger contract, FR-006). Its Model is intentionally empty (the total is
	// not attributed to one model); CostUsd is filled by report.Price at render time.
	JudgeTotal *JudgeUsage `json:"judgeTotal,omitempty"`
}

// ScenarioResult is one scenario's outcome, derived from its Evidence + Verdict.
type ScenarioResult struct {
	Name string
	// FeatureFile is the source .feature file this scenario was parsed from (godog's
	// scenario Uri), so scenarios can be told apart by origin, not just by Name.
	FeatureFile string `json:"FeatureFile,omitempty"`
	Tags        []string
	Pass        bool
	Reasons     []string
	Cost        float64
	Sequence    []string
	Runs        []RunRecord
	Aggregate   *AggregateDetail
	// DerivationNote is a non-fatal, human-readable note recorded when report
	// derivation (sequence/cost) could not be completed for this scenario — e.g. a
	// span missing service.name. It is an observer artifact: it never changes Pass
	// (verdicts come only from step results, audit A8) but stays visible in the JSON
	// and HTML report so the degradation is surfaced, not swallowed. Empty when
	// derivation was clean.
	DerivationNote string `json:"DerivationNote,omitempty"`
	// Judge is this scenario's summed judge-token ledger (US6), carried from the
	// semantic matcher's Verdict.Judge through report.Derive. Non-nil ONLY when the
	// scenario made a judge call — a scenario with no `the result means` step leaves
	// it nil (no fabricated zeros, FR-006). CostUsd is 0 until report.Price fills it.
	Judge *JudgeUsage `json:"judge,omitempty"`
}

// RunRecord is one run within a scenario (one element per @runs iteration).
type RunRecord struct {
	RunID       string
	Passed      bool
	FailureKind string
	LatencyMS   int64
	Cost        float64
}

// Reporter renders a whole RunReport to a writer. Stateless; registered as an instance.
type Reporter interface {
	Report(rep RunReport, w io.Writer) error
}
