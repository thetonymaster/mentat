# Data Model: Seam-Type Nameability (010)

**Date**: 2026-09-09 | **Spec**: [spec.md](./spec.md) | **Research**: [research.md](./research.md)

**Revised for D5.** An earlier version of this document specified facade-**owned mirror
structs** in package `mentat`. That was an import cycle
([research R8](./research.md)); the shapes below are the D5 target state.

This feature adds no new domain concepts. It changes which existing types an external module
can write, and relocates the result types so one reporting seam can serve built-in and custom
reporters alike.

---

## 1. Newly nameable types (aliases)

Three `core` types gain a facade name. All three are **closed** — their fields reference only
builtins and one standard-library pointer — so naming them pulls nothing else onto the
surface. Verified at `f2afdda`.

| Facade name | Declared | Fields | Reached from |
|---|---|---|---|
| `mentat.ExtractPolicy` | `core.go:292` | `Mode string`, `Marker string`, `Pattern *regexp.Regexp` | `RunSpec.Extract` |
| `mentat.HTTPSpec` | `core.go:140` | `URL string`, `Method string`, `Headers map[string]string` | `RunSpec.HTTP` |
| `mentat.AggregateDetail` | `core.go:90` | `Expr string`, `Macro string`, `Op string`, `Computed float64`, `Expected float64`, `PerRun []float64` | `Verdict.Detail`, `ScenarioResult.Aggregate` |

```go
type ExtractPolicy   = core.ExtractPolicy
type HTTPSpec        = core.HTTPSpec
type AggregateDetail = core.AggregateDetail
```

**Golden effect**: one alias line **plus** one `field (X)[nn]` line per field — struct
aliases are expanded (boundary 3 / `surface-golden-v2` rule 3). Budget 3 alias lines + 12
field lines.

**Validation rules**: unchanged. These are the same types the engine already validates.
`ExtractPolicy.Pattern` must still carry at least one capture group in `ExtractPattern` mode
(`core.go:325-329`) — an error path an external constructor now becomes able to trigger, and
therefore must be covered.

**`Pattern` is a shared pointer.** `*regexp.Regexp` is documented safe for concurrent use, so
two runs sharing one `ExtractPolicy` value is sound. The spec flagged this as presumed; it is
confirmed, and carried by a test rather than a comment.

---

## 2. `internal/result` — new leaf package

D5 moves four types out of `internal/core` into a new package, where internal consumers can
name them and the facade can alias them.

```text
internal/result/result.go
    Results         (was core.RunReport)
    ScenarioResult  (was core.ScenarioResult, + RunIDs)
    RunRecord       (was core.RunRecord)
    Reporter        (was core.Reporter, re-shaped)
```

**Imports**: `internal/core` (for `AggregateDetail` and `JudgeUsage`), plus `io` and `time`.
`core` imports nothing back — verified: nothing inside `core` references these four; their
only consumers are `internal/report`, `internal/registry`, the root package and the generated
mock (`core.go:334-399` call sites).

**Facade**:

```go
type Results        = result.Results
type ScenarioResult = result.ScenarioResult
type RunRecord      = result.RunRecord
type Reporter       = result.Reporter
```

**Why aliases and not declarations**: root already imports `internal/report`,
`internal/engine` and `internal/registry` (`run.go:11-17`), so a type any of them consumes
cannot be declared at the root. Every other public type on the facade is an alias for exactly
this reason. See [research R8](./research.md).

**Golden effect**: `Results` and `ScenarioResult` stop rendering as single inline declarations
(`public-surface.golden:169`, `:173`) and become aliases, expanding to one line per field.
Expect roughly twenty added lines where the pre-D5 plan expected two rewritten ones. Finer
freezing, not a regression — but any earlier line-count expectation is void.

---

## 3. `Results` — the collapsed report type

Formerly `core.RunReport`, moved verbatim. Field set, order and tags are **unchanged from
today's `core.RunReport`**, which is what makes the emitted JSON identical by construction.

```go
type Results struct {
	Scenarios   []ScenarioResult
	Total       int
	Passed      int
	Failed      int
	TotalCost   float64
	StartedAt   time.Time
	Duration    time.Duration
	Interrupted bool             `json:"interrupted,omitempty"`
	JudgeTotal  *core.JudgeUsage `json:"judgeTotal,omitempty"`
}

func (r Results) ExitCode() int   // moves with the type
```

**What changes for a `Run` caller**: `Results` gains `Total`, `StartedAt` and `Duration`
(additive), and its **field order changes** relative to today's `mentat.Results`. Keyed
literals — the only form in this repo — are unaffected; unkeyed literals break, and an unkeyed
literal that still compiled would mean something different. The migration note must say both.

**What changes on the wire**: nothing. Same struct, same order, same tags.

**Invariants preserved**: `JudgeTotal` stays `nil` when no scenario made a judge call — no
fabricated zero totals (006 FR-006). `Total` is the count of scenarios that *completed*, so
for an interrupted run it is less than the suite size; it is the report's own value, never
recomputed as `Passed + Failed`.

---

## 4. `ScenarioResult` — collapsed, plus one hidden field

Formerly `core.ScenarioResult`, moved verbatim, with `RunIDs` appended so existing `Run`
callers keep compiling.

```go
type ScenarioResult struct {
	Name           string
	FeatureFile    string                `json:"FeatureFile,omitempty"`
	Tags           []string
	Pass           bool
	Reasons        []string
	Qualifiers     []string              `json:"qualifiers,omitempty"`
	Cost           float64
	Sequence       []string
	Runs           []RunRecord
	Aggregate      *core.AggregateDetail
	DerivationNote string                `json:"DerivationNote,omitempty"`
	Judge          *core.JudgeUsage      `json:"judge,omitempty"`
	RunIDs         []string              `json:"-"`
}
```

**`RunIDs` is the only addition**, and it is excluded from serialization. It stays derived
from `Runs` — one entry per run, in order — so the two can never disagree. The derivation
moves out of `toResults` (`run.go:491-495`) to wherever the record is built, and a test pins
the correspondence.

**Net effect on the public type**: a `Run` caller gains `Tags`, `Qualifiers`, `Sequence`,
`Runs` and `Aggregate`, and keeps everything they had. Same unkeyed-literal caveat as §3.

**Net effect on the wire**: nothing — `RunIDs` is the only new field and it is `json:"-"`.

---

## 5. `RunRecord`

Formerly `core.RunRecord`, moved verbatim and untagged.

```go
type RunRecord struct {
	RunID       string
	Passed      bool
	FailureKind string
	LatencyMS   int64
	Cost        float64
}
```

Closed — five builtin-typed fields, no onward reachability.

---

## 6. `Reporter` — re-shaped seam

```go
// before  (internal/core)
type Reporter interface{ Report(rep RunReport, w io.Writer) error }
// after   (internal/result), aliased as mentat.Reporter
type Reporter interface {
	Report(res Results, w io.Writer) error
}
```

**Breaking**: the parameter type is renamed and relocated. `RunReport` ceases to exist as a
name, which is how SC-002 reaches zero for it.

**One interface** (FR-012). The three built-ins implement *this* type; there is no privileged
internal variant. Under D5 that is true by construction rather than by discipline — there is
only one type to implement against, so SC-008 is satisfied by the reporters compiling.

**The reporters barely change.** They read the same fields off the same struct; the edit is
the parameter type's name and import. This is the largest single reduction in risk D5 buys.

### Registration

```go
type ReporterFactory func(Config) (Reporter, error)   // mirrors ComparatorFactory (run.go:201)
func WithReporter(name string, f ReporterFactory) Option
```

Reporters move onto the per-engine `*Registry` beside the other seams; the package-global map
(`registry.go:190-193`) and its obsolete rationale comment are deleted (R2, FR-015).
Collision discipline matches `WithDriver`: a name already taken fails loudly at build.

`WithReports(map[string]string)` is unchanged and still maps a reporter *name* to an output
path — a custom reporter is selected exactly as a built-in one is.

**`EmitReports` must reach the per-engine registry.** It resolves reporters through the
package-global today (`emit.go:36`); once the global is gone its signature takes both the
`Results` and the registry (or a resolved reporter set). Its caller is `run.go:457`, which
holds both.

---

## 7. Reachable-set definition — widened

From

> transitive closure of exported struct types reachable from `mentat.Config` and
> `mentat.Results`

to additionally include

> every parameter and result type appearing in a published seam's method set, and their
> transitive closure.

This is the root-cause fix (US4): the four gaps exist because the sweep walked outward from
data types but never through seam signatures.

---

## Entity relationships

```
internal/core        AggregateDetail, JudgeUsage, Verdict, RunSpec, Evidence, …
       ▲
       │ (result imports core; core imports nothing back)
       │
internal/result      Results ──┬─ Scenarios []ScenarioResult
                               │        ├─ Runs []RunRecord
                               │        └─ Aggregate *core.AggregateDetail
                               └─ consumed by Reporter.Report

mentat (root)        aliases all of the above; imports report/engine/registry/steps/core/config
```

No internal package imports the root. That is the invariant D5 restores, and the one the
pre-D5 design broke.
