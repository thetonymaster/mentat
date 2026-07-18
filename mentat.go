// Package mentat is the public extension surface of the Mentat trace-behaviour
// test framework. It re-exports — via zero-cost type aliases — exactly the seam
// interfaces and evidence/contract types a third-party module needs to implement
// a custom adapter or embed Mentat as a library, without importing anything
// under internal/.
//
// Because every symbol here is a Go type alias to the underlying internal type,
// a value satisfying a facade interface satisfies the internal seam by identity
// — no adapters, no conversions. Everything not aliased here stays internal on
// purpose: the surface is deliberately the minimum viable set, since widening it
// later is easy and narrowing it is a breaking change.
//
// The package provides four things:
//
//   - The seam interfaces you implement to extend Mentat: Driver, TraceStore,
//     Comparator and Judge, plus the Correlator and Reporter types their
//     contracts reference.
//   - The evidence and contract vocabulary those seams exchange — Evidence,
//     Output, Verdict, RunSpec, RunResult, the trace forest (Trace, Span) and the
//     span status/kind constants.
//   - The mentat.yaml configuration surface (Config and its nested types),
//     constructible in code or loaded from disk with LoadConfig.
//   - The library entry point: Run executes a suite and returns Results, and the
//     With* Options configure it — WithFeatures, WithOutput, WithVerbosity,
//     WithConcurrency, WithTags, WithFailFast and WithReports for suite setup, and
//     WithDriver, WithStore, WithComparator and WithJudge to register custom
//     adapters under a name that config and feature files can then reference.
//
// Run and the Options live in run.go; this file holds the aliases and constants.
package mentat

import (
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// --- Seam interfaces (aliases to internal/core) ---

// Driver is the SUT-driving seam: a registrable adapter (WithDriver hook).
type Driver = core.Driver

// TraceStore is the trace-backend seam: a registrable adapter (WithStore hook).
type TraceStore = core.TraceStore

// Comparator is the behaviour-assertion seam: a registrable adapter
// (WithComparator hook). It reads Evidence only — never a store or a driver —
// which is what keeps comparators portable across agents and microservices.
type Comparator = core.Comparator

// Judge is the semantic-verdict seam: a registrable adapter (WithJudge hook).
type Judge = core.Judge

// Correlator is the tag-first correlation seam. Exposed as a type because
// contracts reference it; it deliberately has no registration hook yet, because
// no concrete external demand for one has appeared.
type Correlator = core.Correlator

// Reporter is the report-rendering seam. Exposed as a type because contracts
// reference it; it deliberately has no registration hook yet, for the same reason
// as Correlator.
type Reporter = core.Reporter

// --- Evidence & contract types (aliases to internal/core) ---

// Evidence is everything a comparator may inspect about one run: the Comparator
// contract's input, and the boundary that keeps comparators portable.
type Evidence = core.Evidence

// Output is the driver-captured boundary result carried by Evidence and
// RunResult; a comparator reads it, a driver returns it.
type Output = core.Output

// Verdict is a comparator's pass/fail result (the Comparator contract's output).
type Verdict = core.Verdict

// Expectation is the comparator-specific config (= any); the second Compare arg.
type Expectation = core.Expectation

// RunSpec is the driver input (the Driver.Run and Correlator.Inject argument).
type RunSpec = core.RunSpec

// RunResult is the driver output (the Driver.Run return value).
type RunResult = core.RunResult

// CompletenessContract is the per-run trace-completeness barrier set the engine
// derives from a target (adapter kind + completeness config) and carries in a
// ResolveRequest. Re-exported so an external Correlator implementation can name it.
type CompletenessContract = core.CompletenessContract

// ResolveRequest is the live Correlator.Resolve argument: the run's correlation tag
// plus its CompletenessContract. Re-exported so an external Correlator can name the
// parameter it must accept and a caller can construct the request.
type ResolveRequest = core.ResolveRequest

// TraceQuery is the tag-first store lookup (the TraceStore.Query argument).
type TraceQuery = core.TraceQuery

// TraceRef is a store-side trace reference (the TraceStore.Query result element).
type TraceRef = core.TraceRef

// StoreCaps is a store's capability descriptor (the TraceStore.Caps result).
type StoreCaps = core.StoreCaps

// JudgeRequest is the matter to be judged (the Judge.Judge argument).
type JudgeRequest = core.JudgeRequest

// JudgeVerdict is the semantic verdict (the Judge.Judge return value).
type JudgeVerdict = core.JudgeVerdict

// JudgeUsage is a summable judge-token ledger row (calls + tokens + derived cost).
// It is transitively required by Results/ScenarioResult, which carry the suite- and
// scenario-level judge ledgers a library caller inspects.
type JudgeUsage = core.JudgeUsage

// --- Config surface (aliases to internal/config) ---
//
// Config aliases the internal config so the mentat.yaml surface is constructible
// in code AND loadable from disk (LoadConfig) with no duplicate or conversion type
// that could drift. The nested types below are the reachable "mentat.yaml surface":
// an external caller must be able to NAME them to build a Config literal (e.g. a
// Targets map), which an alias to Config alone does not provide.

// Config is the whole mentat.yaml configuration, constructible in code or via LoadConfig.
type Config = config.Config

// Target is one SUT target entry of Config.Targets (adapter, command, http, extract).
type Target = config.Target

// HTTP is a target's http-adapter request config (Target.HTTP).
type HTTP = config.HTTP

// ExtractConfig is a target's answer-extraction policy (Target.Extract).
type ExtractConfig = config.ExtractConfig

// Endpoint is an endpoint holder (Config.Tempo).
type Endpoint = config.Endpoint

// PollSpec is the trace stability-poll configuration (Config.Poll).
type PollSpec = config.PollSpec

// Pricing maps a model name to its per-million-token rate (Config.Pricing).
type Pricing = config.Pricing

// ModelRate is a single model's input/output price (a Pricing value).
type ModelRate = config.ModelRate

// JudgeConfig configures the semantic LLM-judge result matcher (Config.Judge).
type JudgeConfig = config.JudgeConfig

// RunBudget is a resolved per-run lifecycle bound (Config.Budget / Target.Budget).
type RunBudget = config.RunBudget

// --- Trace forest types (aliases to internal/trace) ---

// Trace is the run's trace forest (Evidence.Trace; the TraceStore.DecodePayload
// result). It is a forest, not a tree: one run may span more than one root trace
// (multi-turn or sub-agent runs), so never assume a single root.
type Trace = trace.Trace

// Span is a single span within a Trace forest (the unit a store decoder builds
// and a comparator walks).
type Span = trace.Span

// --- FailureKind constants (from internal/core) ---

// FailureKindDriver is Evidence.FailureKind when the driver invocation failed.
const FailureKindDriver = core.FailureKindDriver

// FailureKindResolve is Evidence.FailureKind when trace resolution failed.
const FailureKindResolve = core.FailureKindResolve

// --- Canonical span status vocabulary (from internal/trace) ---

// StatusUnset is Span.Status when no status was set.
const StatusUnset = trace.StatusUnset

// StatusOk is Span.Status for an OK span.
const StatusOk = trace.StatusOk

// StatusError is Span.Status for an errored span.
const StatusError = trace.StatusError

// --- Canonical span kind vocabulary (from internal/trace) ---

// KindInternal is Span.Kind for an internal span.
const KindInternal = trace.KindInternal

// KindServer is Span.Kind for a server span.
const KindServer = trace.KindServer

// KindClient is Span.Kind for a client span.
const KindClient = trace.KindClient

// KindProducer is Span.Kind for a producer span.
const KindProducer = trace.KindProducer

// KindConsumer is Span.Kind for a consumer span.
const KindConsumer = trace.KindConsumer

// KindUnspecified is Span.Kind when the kind is unspecified (the empty string).
const KindUnspecified = trace.KindUnspecified
