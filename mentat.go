// Package mentat is the public extension surface of the Mentat trace-behaviour
// test framework. It re-exports — via zero-cost type aliases — exactly the seam
// interfaces and evidence/contract types a third-party module needs to implement
// a custom adapter or embed Mentat as a library, without importing anything
// under internal/ (spec 007 FR-001; audit finding G1).
//
// Because every symbol here is a Go type alias to the underlying internal type,
// a value satisfying a facade interface satisfies the internal seam by identity
// — no adapters, no conversions. Everything not aliased here stays internal on
// purpose: the surface is the minimum viable set (one-way door), and each alias
// below is individually justified per SC-006.
//
// This file is the skeleton (feature 007, tasks T002–T003): types and constants
// only. Registration options (With*), the Run entry point, Config, and Results
// arrive in later tasks.
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
// (WithComparator hook); reads Evidence only (Constitution I).
type Comparator = core.Comparator

// Judge is the semantic-verdict seam: a registrable adapter (WithJudge hook).
type Judge = core.Judge

// Correlator is the tag-first correlation seam. Exposed as a type because
// contracts reference it; deliberately has no registration hook yet
// (three-examples rule — no external demand, spec Assumptions).
type Correlator = core.Correlator

// Reporter is the report-rendering seam. Exposed as a type because contracts
// reference it; deliberately has no registration hook yet (three-examples rule).
type Reporter = core.Reporter

// --- Evidence & contract types (aliases to internal/core) ---

// Evidence is everything a comparator may inspect about one run (the Comparator
// contract's input; Constitution I portability boundary).
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
// scenario-level judge ledgers a library caller inspects (spec 007 FR-003).
type JudgeUsage = core.JudgeUsage

// --- Config surface (aliases to internal/config) ---
//
// Config aliases the internal config so the mentat.yaml surface is constructible
// in code AND loadable from disk (LoadConfig) with no duplicate/conversion type to
// drift (FR-003; research R2). The nested types below are the reachable "mentat.yaml
// surface": an external caller must be able to NAME them to build a Config literal
// (e.g. a Targets map), which an alias to Config alone does not provide.

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
// result). A run may span >1 root (Constitution II — never assume a single root).
type Trace = trace.Trace

// Span is a single span within a Trace forest (the unit a store decoder builds
// and a comparator walks).
type Span = trace.Span

// --- FailureKind constants (from internal/core) ---

// FailureKindDriver is Evidence.FailureKind when the driver invocation failed.
const FailureKindDriver = core.FailureKindDriver

// FailureKindResolve is Evidence.FailureKind when trace resolution failed.
const FailureKindResolve = core.FailureKindResolve

// --- Canonical span status vocabulary (feature 002; from internal/trace) ---

// StatusUnset is Span.Status when no status was set.
const StatusUnset = trace.StatusUnset

// StatusOk is Span.Status for an OK span.
const StatusOk = trace.StatusOk

// StatusError is Span.Status for an errored span.
const StatusError = trace.StatusError

// --- Canonical span kind vocabulary (feature 002; from internal/trace) ---

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
