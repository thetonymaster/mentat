# Mentat Reporter Seam — Design

**Date:** 2026-06-20
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`
**Related:** `2026-06-18-mentat-seam-registries-design.md` (§6 deferred the reporter
registry "until a non-godog reporter is wanted (e.g. a JSON/HTML report)" — this spec
is that trigger firing) and `2026-06-19-mentat-multirun-runs-design.md` (§2 deferred a
"first-class `Reporter` seam / report artifact" to a future separate spec — this one).

> **Dependency (added 2026-06-20):** the `AggregateDetail` / `Verdict.Detail`
> computed-vs-expected contract referenced in §4 was found *not* to exist in the v1
> aggregate comparator (it returns `bool` only) and was an explicitly deferred
> refinement (multirun spec §287). It is now its **own companion feature** — the CEL
> aggregate-scalar spec — which **produces** `Verdict.Detail`. This reporter spec only
> **consumes** it (copies `v.Detail` into `ScenarioResult.Aggregate`, nil otherwise).
> The "one comparator change" framing in §4.2/§5 is superseded by that companion
> feature and is **out of scope here**. Build order: the CEL aggregate-scalar feature
> first, this reporter second.

## 1. Purpose

Architecture invariant #3 says **"every seam is an interface, wired at one composition
root via per-seam registries."** The reporter is one of the seven designed seams that
has no registry today — verdicts surface **only** through godog's own pretty/junit
formatters, and the structured data a report would want (cost numbers, per-run tables,
tool sequences) is `fmt.Sprintf`'d into `Verdict.Reasons []string` inside the
comparators and the numbers are then thrown away.

The seam-registries spec (§6) deliberately left the reporter wired directly because a
registry with one member is an empty abstraction (YAGNI). The trigger it named —
**"a non-godog reporter is wanted (e.g. a JSON/HTML report)"** — is now real: we want a
machine-readable **JSON** run report (CI ingestion, trend tracking, dashboards) and a
human-browsable **HTML** view rendered over the same data. Two concrete reporters
through one registry is what earns the seam.

godog's pretty/junit output **stays exactly as-is**. The new reporters run *beside* it,
not instead of it. This keeps the mandatory L3 meta-test's stdout substrings intact
(`e2e/meta_test.go` greps combined output for `"exceed budget"`, `"forbidden tool"`,
etc., produced by stringifying `Verdict.Reasons`).

## 2. Scope

**In scope:**

- `core.Reporter` interface + a `RunReport`/`ScenarioResult` data contract in `core`;
  `registry.RegisterReporter`/`registry.Reporter` mirroring the matcher seam.
- Two registered reporters in a new `internal/report` package: `json` and `html`.
- A run-scoped `report.Collector` the steps `world` appends to at scenario end, and a
  `report.Derive` projection that turns captured `Evidence` + `Verdict` into a
  `ScenarioResult`.
- A `comparator.CostOrZero(trace, pricing)` helper — extracted from `budgets.go`'s
  existing cost math via a shared `deriveCost` — that the reporter calls so cost is
  derived identically to `budgets`/`cel`/`aggregate_cel` (behaviour-preserving for them;
  the reporter treats absent cost as `0` and errors only on corruption).
- One **optional, nullable** `Verdict.Detail *AggregateDetail` field, populated **only**
  by the aggregate (`@runs(N)`) comparator — the single place `Evidence` cannot fully
  reconstruct the computed-vs-expected numbers.
- `cmd/mentat` flags `--report-json <path>` / `--report-html <path>` and the
  post-`suite.Run()` wiring that folds the `Collector` into a `RunReport` and invokes
  the selected reporters.

**Out of scope (deferred):**

- Replacing or wrapping godog's pretty/junit output — those stay native. The reporters
  are additive artifacts, not a new console format.
- Enriching **every** comparator's `Verdict` with structured detail. Only the aggregate
  comparator changes; all others derive from `Evidence`. (See §5 — Hybrid decision.)
- A `correlator` or `judge` registry — still single-impl / Phase 4 (unchanged from the
  seam-registries spec §6).
- Trend/history storage across runs, diffing two reports, a hosted dashboard. The JSON
  artifact is the data model that *enables* those later; building them is separate.
- `r.body` parsed-JSON access, percentile sample gates — unchanged deferrals from the
  multirun spec.

## 3. The seam

### 3.1 Interface (defined in `core`, beside the other seams)

A reporter renders one **whole run** to a writer. JSON and HTML are run-level artifacts
(one file per run), so the unit is `RunReport`, not the per-scenario `ScenarioResult`
sketched in the foundational design §7 (`Report(r ScenarioResult) error`, line 323) —
this spec refines that sketch.

```go
// internal/core/core.go
type Reporter interface {
    Report(rep RunReport, w io.Writer) error
}
```

Defining it in `core` (where `Comparator`, `Driver`, `TraceStore`, `Correlator`,
`Matcher` live) keeps `registry` importing only `core` — no cycle. `report` imports
`core`; `core` imports nothing back.

### 3.2 Registry (instance-based)

A reporter writes to an `io.Writer` passed at call time and holds no per-run state, so
it is **stateless** — registered as an *instance*, like comparators/matchers, not a
factory like the store (per the two documented patterns in the seam-registries spec §5):

```go
// internal/registry/registry.go
var reporters = map[string]core.Reporter{}
func RegisterReporter(name string, r core.Reporter) { reporters[name] = r }
func Reporter(name string) (core.Reporter, bool)    { r, ok := reporters[name]; return r, ok }
```

### 3.3 Registration at the composition root

`engine.Build` (`build.go`) registers the built-ins alongside the existing comparator /
matcher / store registrations, via a `report.RegisterBuiltins()` helper so the set has
one home:

```go
report.RegisterBuiltins() // json, html
```

A third reporter (e.g. JUnit-native, or a future SaaS sink) then registers through the
same seam with no change to `cmd/mentat`'s selection logic.

## 4. The data contract

```go
// internal/core/core.go — pure data; reporters render this, comparators do not touch it.
type RunReport struct {
    Scenarios []ScenarioResult
    Total     int
    Passed    int
    Failed    int
    TotalCost float64
    // StartedAt time.Time and Duration are stamped by cmd/mentat (it owns a real clock;
    // workflow/engine code does not).
    StartedAt time.Time
    Duration  time.Duration
}

type ScenarioResult struct {
    Name      string
    Tags      []string
    Pass      bool
    Reasons   []string          // from Verdict.Reasons, carried verbatim
    Cost      float64           // DERIVED: comparator.CostOrZero(trace, pricing)
    Sequence  []string          // DERIVED: ctl/format helpers over the Evidence forest
    Runs      []RunRecord       // from []Evidence; len 0 or 1 for single-run scenarios
    Aggregate *AggregateDetail  // non-nil only for @runs(N) scenarios
}

type RunRecord struct {
    RunID       string
    Passed      bool
    FailureKind string
    LatencyMS   int64
    Cost        float64
}

type AggregateDetail struct {
    Expr     string  // the CEL aggregate expression evaluated
    Computed float64
    Expected float64
    Op       string  // ">=", "<=", "==", ...
}
```

### 4.1 Derivation — `Evidence` in, `ScenarioResult` out

```go
// internal/report/derive.go
func Derive(name string, tags []string, v core.Verdict, evs []core.Evidence, p core.Pricing) core.ScenarioResult
```

- `Pass`, `Reasons` ← `Verdict` verbatim.
- `Cost`, per-run `Cost` ← `comparator.CostOrZero(ev.Trace, p)` (absent cost → 0;
  malformed/ambiguous still error).
- `Sequence` ← `ctl/format` helpers over `ev.Trace` (tool calls for agents, service
  hops for microservices).
- `Runs` ← one `RunRecord` per `Evidence` (`RunID`, `Failed`→`Passed`, `FailureKind`,
  root-span duration → `LatencyMS`).
- `Aggregate` ← copied from `v.Detail` when present.

### 4.2 The aggregate detail (the one comparator change)

`internal/comparator/aggregate_cel.go` currently renders the computed-vs-expected line
into `Verdict.Reasons` only. It additionally populates `Verdict.Detail` with the
discrete `{Expr, Computed, Expected, Op}`. Every other comparator leaves `Detail` nil.
`Verdict.Reasons` is unchanged, so the L3 substring gate is unaffected.

### 4.3 Cost extraction (behaviour-preserving)

`budgets.go`'s `costSum` is unexported and shared by **three** comparators —
`budgets`, `cel` (`cel.go:139`), and `aggregate_cel` (`aggregate_cel.go:124`) — so it is
*not* moved to `core` (that would drag `tokenSum`/`traceModel`/`tokenAttr` along and
churn all three). Instead:

- Extract the per-span loop into `deriveCost(t *trace.Trace, p core.Pricing) (cost float64, seen bool, err error)`
  inside `budgets.go`. `costSum` becomes a thin wrapper: `if !seen { return 0, <existing
  "cost not available" error> }`, so `budgets`/`cel`/`aggregate_cel` behaviour and reason
  strings are **unchanged**.
- Add exported `comparator.CostOrZero(t *trace.Trace, p core.Pricing) (float64, error)`:
  returns `(cost, nil)` even when cost is absent (a cost-less scenario reports `0`), but
  still propagates malformed / ambiguous-model / out-of-range errors (no silent fallback
  on corruption). This is what the reporter calls.

`report` imports `comparator` (acyclic — `comparator` never imports `report`). Exercised
by the existing `budgets`/`cel`/`aggregate_cel` tests (proving the extraction is
behaviour-preserving) plus a new `CostOrZero`/`deriveCost` table test (present→cost,
absent→`(0,nil)`, malformed→error).

## 5. Architecture decisions (with rationale)

- **JSON model + HTML view, two reporters through one registry** — the concrete second
  (and third) consumer that the seam-registries spec §6 required before building the
  seam. JSON is the canonical data; HTML renders the same `RunReport`. (Approved.)
- **Reporters run beside godog, after `suite.Run()`** — not as a godog formatter. A
  godog formatter only receives the flattened step-error *string*, never the structured
  `Evidence`/`Verdict`, so it cannot produce a structured report. Running after the
  suite over an accumulated `Collector` gives full structured access, and leaves godog's
  pretty/junit output (and the L3 stdout substrings) untouched. (Approved.)
- **Hybrid input contract — derive from `Evidence`; structured detail only on the
  aggregate comparator** — cost, sequences, and per-run rows are reconstructable from
  the `Evidence` forest (Evidence-only invariant #1), so those comparators stay
  untouched. The single thing `Evidence` cannot reconstruct is the `@runs(N)`
  computed-vs-expected scalar (it is the output of evaluating the CEL aggregate
  expression), so only that comparator hands it back, via one optional nullable
  `Verdict.Detail` field. This avoids the blast radius of enriching every comparator and
  the risk to the L3 gate. (Approved.)
- **Reporter registers as an instance, not a factory** — stateless (writer passed at
  call time), matching the comparator/matcher pattern; the store's factory pattern is
  for stateful seams. (Approved.)
- **`RunReport` is the report unit, refining the §7 `ScenarioResult` sketch** — JSON
  and HTML are one-file-per-run artifacts; per-scenario reporting would fragment them.
  (Approved.)
- **CLI flags `--report-json` / `--report-html`, each optional** — mirrors the existing
  `--junit` flag; unset means godog output only (backward-compatible). A future
  `reporters:` config field can layer on later without changing the seam. (Approved.)

## 6. Error handling

Per invariant #4 (no silent fallbacks), every failure is a hard, wrapped error:

- `Report(...)` returns `fmt.Errorf("writing %s report to %s: %w", kind, path, err)` on
  file create, `html/template` execution, JSON marshal, or close failure — each names
  the path. `cmd/mentat` treats any reporter error as **fatal** (distinct non-zero
  exit), matching today's junit-close rigor (`main.go:84-90`).
- Both reporters run in sequence; the first failure returns immediately. A report-write
  failure is an infra error with its own non-zero exit, separate from the suite's
  pass/fail verdict — never swallowed.
- **Empty run** (0 scenarios) is legitimate data, not an error — reporters emit a
  well-formed empty report (`Total: 0`).
- **`Derive` and absent traces:** a *failed* run legitimately has no trace; it is
  recorded as `RunRecord{Passed: false, FailureKind: …, Cost: 0, Sequence: empty}` —
  correct data, not a swallowed error. A *missing* trace from broken correlation is
  already a hard error upstream in the engine and never reaches `Derive`. `Derive`
  records absence; it never invents data.

## 7. Testing (TDD, ≥80% per package)

**L1 unit — table-driven, hermetic (inmem/otlp-file fixtures + gomock, no network):**

- `report.Derive` — single-run `Evidence` → row; `@runs(N)` `[]Evidence` → `Runs[]` +
  `Aggregate`; failed run → `RunRecord` with `FailureKind`, cost 0, empty sequence.
- `comparator.CostOrZero` / `deriveCost` — span fixtures × pricing: present→cost,
  absent→`(0, nil)`, malformed/ambiguous→error; and the existing
  `budgets`/`cel`/`aggregate_cel` tests still pass after the `deriveCost` extraction
  (behaviour-preserving).
- `report.json` reporter — golden JSON for a fixed `RunReport` (stable marshal; assert
  discrete fields, not just substrings).
- `report.html` reporter — render a fixed `RunReport`; assert key substrings present
  (scenario names, cost, per-run rows, reasons) — content, not pixels.
- `registry` — register + lookup + unknown-name returns `false` / hard error at the call
  site.
- `aggregate_cel` — asserts it now sets `Verdict.Detail{Computed, Expected, Op, Expr}`
  while `Reasons` is unchanged.
- `cmd/mentat` wiring — via the generated **gomock `Reporter`**: invoked once with the
  expected `RunReport`; a write error propagates as a fatal non-zero exit. No disk I/O.

**L3 meta-test (mandatory — prove Mentat goes red on bad behaviour):**

- The existing stdout-substring greps stay green (godog output is unchanged). ✓
- **New obligation:** run a known-bad scenario with `--report-json`, parse the emitted
  JSON, and assert the failing scenario has `Pass: false` plus the expected reason /
  aggregate detail — i.e. *the report artifact itself reflects failure*, not just the
  console.
- An unwritable `--report-json` path exits non-zero (proves the no-silent-fallback
  contract end-to-end).

**Hermetic by default:** all unit tests in-memory; the e2e meta-test that exercises the
real `cmd/mentat` binary + report files is `//go:build e2e` and uses the existing
fixtures / `make harness-up` stack. Coverage of the new `internal/report` package and
every touched package stays ≥80%.

## 8. Routing

This is behaviour-adding (a new seam, two new output artifacts, and it **extends the L3
meta-test**) → **go-test-writer** owns the red→green→refactor loop. The mechanical
plumbing — the `registry` get/put functions, the `report.RegisterBuiltins` wiring in
`engine.Build`, mock regeneration for the new `Reporter` interface — can route to
**go-coder** once the behaviour is pinned by tests. **go-reviewer** gates before commit
(Evidence-only comparators, no silent fallbacks, ≥80% coverage, L3 present,
Conventional Commits, no AI attribution).
