# Mentat Multi-Run Aggregate Assertions — Design

**Date:** 2026-06-19
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md` (§9 Non-determinism),
`2026-06-18-mentat-cel-engine-design.md`

## 1. Purpose

A single run of a non-deterministic system-under-test (an LLM agent) proves nothing
about its *typical* behaviour. The agent might call the search tool on this run and
skip it on the next; latency and cost vary run to run. The behaviour worth asserting
is **statistical**: "the agent consults search in ≥80% of runs", "p95 latency stays
under 2 s", "no run errors".

The foundational design reserved a hook for this in §9 — a `@runs(N)` scenario tag
that "causes the engine to execute the scenario N times and apply a pass policy" —
but nothing implements it. `Engine.Drive` is one run, `world` holds one `Evidence`,
and `Comparator.Compare` consumes a single `Evidence`. This spec builds that reserved
hook and, rather than the originally-narrow "pass policy over per-run verdicts",
implements the more general form: **aggregate statistical assertions over the sample
of N runs**, of which pass-policy is a special case.

The work is purely additive. The single-run path (`the run satisfies`, the
`sequence`/`budgets`/`result`/`cel` comparators) is not touched.

## 2. Scope

**In scope**

- A `@runs(N)` / `@runs(N,parallel)` scenario tag parsed at scenario start, driving
  the engine to produce N `Evidence` values for one scenario.
- A new engine method `DriveN` that owns the repeat loop on top of the existing
  per-run `Drive`, serial by default and parallel on opt-in under the existing
  per-target semaphore.
- A new **`AggregateComparator`** seam (`Aggregate(ctx, []Evidence, Expectation)`)
  added to `core`, registered in its own registry map and wired at `engine.Build`.
- A `cel`-backed aggregate comparator (`internal/comparator/aggregate_cel.go`) and a
  godog grammar step `the runs satisfy "<cel expr>"`, exposing a `runs` list binding
  and per-run record `r`, plus aggregate helper functions (`rate`, `count`, `mean`,
  `sum`, `min`, `max`, `p50`, `p95`, `p99`, `stddev`).
- Failed iterations recorded as **typed, visible samples** (`r.failed`,
  `r.failureKind`) — never silently dropped.
- Aggregate failure detail (computed-vs-expected + per-run table including
  `test.run.id`) carried in `Verdict.Reasons`.
- L1 table-driven unit tests for the aggregate comparator and macro expansion
  (≥80% coverage on new packages) and the mandatory **L3 meta-test** proving Mentat
  goes red on bad statistical behaviour.

**Out of scope**

- A dedicated `Reporter` seam / report artifact. Aggregate detail rides in
  `Verdict.Reasons` through godog's existing JUnit/pretty output (§10 of the
  foundational design). A first-class `Reporter` remains a future, separate spec.
- A minimum-sample gate for percentiles (see §9.3 — deliberately not added).
- New CEL *scalar* variables beyond today's schema. The per-run record reuses the
  existing single-run binding vocabulary; the only additions are the multi-run
  metadata fields (`runId`, `failed`, `failureKind`).
- `r.body` (parsed-JSON access) on the per-run record. v1 exposes `r.bodyText` (raw
  string) only — eagerly parsing every run's body would fail an aggregate on a single
  non-JSON run even when `body` is unreferenced. Parsed-JSON aggregates can be added
  later behind reference-aware binding.
- Multi-run against pinned/replayed traces (see §9.2 — a hard error, not a feature).
- Natural-language per-assertion Gherkin steps. CEL + helper macros is the chosen
  surface; English-sugar steps may be layered over this later if they earn it.

**New dependency**

None. The work reuses `github.com/google/cel-go` (already a direct dependency via the
CEL engine) and the existing OTel/Tempo stack.

## 3. The `@runs(N)` lifecycle

```
@runs(10)  /  @runs(10,parallel)
   │  (read in sc.Before; default N=1, parallel=false when the tag is absent)
   ▼
world{ n, parallel, evs []core.Evidence }     # world gains run-config + a slice
   │
   ▼  When-step  ──▶  engine.DriveN(ctx, target, args, n, parallel) ([]Evidence, error)
       │
       └─ loops engine.Drive (internal/engine/engine.go) N times
            • each iteration mints its own fresh test.run.id (cor.Inject)
            • serial: one Drive to completion before the next
            • parallel: each goroutine acquires the existing per-target semaphore,
              results written into a pre-sized []Evidence by index (stable ordering)
            • a per-iteration HARNESS failure becomes a failed Evidence (see §6),
              not an aborted batch
   │
   ▼  Then-step  ──▶  AggregateComparator.Aggregate(ctx, evs, expectation) (Verdict, error)
   │
   ▼  Verdict.Reasons (computed-vs-expected + per-run table)
        → godog step error → JUnit / pretty (§8)
```

- **`DriveN` owns the loop**, keeping run orchestration in the engine exactly as
  single-run `Drive` does today. It does not re-implement driving — it calls `Drive`
  per iteration so correlation, tag injection, and the per-target semaphore are
  reused verbatim.
- **`world`** (`internal/steps/steps.go`) gains `n int`, `parallel bool`, and
  `evs []core.Evidence`. Un-tagged scenarios keep `n == 1` and continue down the
  existing single-`Evidence` path with the existing comparators — the aggregate path
  only engages for the `the runs satisfy` step.
- **Single-run steps under `@runs(N>1)` are rejected.** A single-run comparator step
  (`the run satisfies`, budgets, sequence, result) only inspects one `Evidence`, so
  using one inside a multi-run scenario is ambiguous. Rather than silently evaluating
  the first run, `world.check` hard-errors when `n > 1`, directing the author to
  `the runs satisfy` (no-silent-fallback; §9.7). Mixed single+aggregate assertions in
  one scenario are a possible future extension.

## 4. Grammar

A new godog step:

```gherkin
Then the runs satisfy "<cel expr>"
```

inline or as a docstring, mirroring the single-run `the run satisfies` step. The CEL
expression is compiled fail-fast (type-checked) at scenario start in `sc.Before`,
alongside the existing single-run expression compilation.

### 4.1 Bindings

- **`runs`** — a `list` of per-run records. This is the raw substrate; any assertion
  expressible with CEL's built-in list macros (`.filter`, `.map`, `.all`, `.exists`)
  is available directly, e.g.
  `size(runs.filter(r, 'search' in r.tools)) >= size(runs) * 0.8`.
- Each record **`r`** exposes the **same vocabulary as the single-run CEL bindings**
  (`internal/comparator/cel.go`), plus run/failure metadata:

  | field | type | source |
  | --- | --- | --- |
  | `r.runId` | string | the iteration's `test.run.id` |
  | `r.status` | int | `Output.Status` |
  | `r.exitCode` | int | `Output.ExitCode` |
  | `r.bodyText` | string | `Output.Body` as text |
  | `r.answer` | string | `Output.Answer` |
  | `r.tokens` | int | trace token sum |
  | `r.cost` | double | trace cost sum (pricing) |
  | `r.errors` | int | trace error-span count |
  | `r.latencyMs` | int | trace envelope duration |
  | `r.tools` | list&lt;string&gt; | tool-call sequence |
  | `r.services` | list&lt;string&gt; | service-call sequence |
  | `r.failed` | bool | harness-level failure on this iteration (§6) |
  | `r.failureKind` | string | `""` (ok) / `"driver"` / `"resolve"` — classified by which engine call failed (§6) |

  For a successful run, the boundary fields (`runId`/`failed`/`failureKind`/`status`/
  `exitCode`/`bodyText`/`answer`) are always present, and each **trace-derived** field
  (`tokens`/`cost`/`errors`/`latencyMs`/`tools`/`services`) is computed and bound
  **only when the expression references it** — exactly as the single-run `bindVars`
  does (§6 of the CEL spec; the referenced fields are extracted by walking the
  compiled expression's AST for field selections). This is not merely an optimization:
  `costSum` (with no pricing and no emitted `gen_ai.usage.cost_usd`) and
  `serviceSequence` (on an agent trace whose spans carry no `service.name`) are *hard
  errors* when computed, so binding them unconditionally would fail an aggregate that
  never mentions `cost`/`services` — which is the common agent case. A **failed**
  iteration has no trace, so none of the six trace-derived keys are bound regardless
  of references. Referencing a key that was not bound — a trace metric on a failed
  run, or `cost` when there is no cost data — is a hard CEL/binding error, never a
  silent zero. An author asserting on metrics scopes past failures, which CEL's
  short-circuit `&&` makes clean: `rate(r, !r.failed && r.latencyMs < 2000) >= 0.9` —
  when `r.failed` is true the missing-key side is never evaluated.

### 4.2 Helper functions

Readability sugar over the `runs` list. Each is a **CEL macro** that expands the
`(r, <expr>)` form into an ordinary `runs` comprehension plus, where needed, a real
list function — the same mechanism CEL's own `all`/`exists`/`filter` use. The raw
`runs` form remains available for anything the helpers do not cover.

| helper | expands to | result |
| --- | --- | --- |
| `rate(r, P)` | `double(size(runs.filter(r, P))) / double(size(runs))` | double in `[0,1]` |
| `count(r, P)` | `size(runs.filter(r, P))` | int |
| `mean(r, X)` | `__mean__(runs.map(r, X))` | double |
| `sum(r, X)` | `__sum__(runs.map(r, X))` | double |
| `min(r, X)` | `__min__(runs.map(r, X))` | double |
| `max(r, X)` | `__max__(runs.map(r, X))` | double |
| `p50/p95/p99(r, X)` | `__percentile__(runs.map(r, X), q)` | double |
| `stddev(r, X)` | `__stddev__(runs.map(r, X))` | double |

`P` is a boolean per-run predicate; `X` is a numeric per-run projection. The
underlying `__*__` functions are plain CEL functions over an already-evaluated
`list<double>`; the macros handle the `(r, …)` → comprehension rewrite and coerce the
projection to double in the expansion (`runs.map(r, double(X))`), so int-valued fields
like `latencyMs` and `tokens` work directly without the author writing `double(...)`.

A **per-run predicate is the same expression an author writes single-run today**, so
the originally-reserved pass-policy is just a rate threshold:
`rate(r, <single-run check>) >= 0.8` (threshold), or `runs.all(r, <check>)` (`all`),
or `count(r, <check>) * 2 > size(runs)` (`majority`).

### 4.3 Examples

```gherkin
@runs(10)
Scenario: search is usually consulted and the agent is reliable
  When I run the agent with "find recent papers on retrieval-augmented generation"
  Then the runs satisfy "rate(r, 'search' in r.tools) >= 0.8"
  And  the runs satisfy "p95(r, r.latencyMs) < 2000.0"
  And  the runs satisfy "count(r, r.failed) == 0"
  And  the runs satisfy "mean(r, r.cost) < 0.05"
```

## 5. The `AggregateComparator` seam

```go
// internal/core/core.go — additive; existing Comparator is untouched.
type AggregateComparator interface {
    Aggregate(ctx context.Context, evs []Evidence, e Expectation) (Verdict, error)
}
```

- This is a sibling of `Comparator`, not a replacement. The single-Evidence
  `Comparator.Compare` and every existing comparator keep their exact signatures, and
  the Evidence-only / one-run-per-Evidence boundary is preserved.
- `internal/comparator/aggregate_cel.go` implements `AggregateComparator`. It reuses
  the CEL engine and the per-run `bindVars` logic to build each `r`, binds `runs` to
  the list of records, registers the macros + `__*__` functions in the CEL
  environment, then compiles-and-evaluates the expression once over the sample.
- A new registry map for aggregate comparators is added to `internal/registry`,
  following the existing comparator/driver/matcher/store registry pattern. It is wired
  once in `engine.Build`, the single composition root, alongside the others.
- A `gomock` mock for `AggregateComparator` is generated next to the `core`
  interfaces (`//go:generate`), per the testing conventions.

## 6. Failed-run policy

A failed iteration is **a sample, recorded explicitly** — chosen so resilience can be
asserted (`the runs satisfy "rate(r, r.failed) < 0.1"`) — but it is made *typed and
visible* so it never becomes a silent gap in the denominator:

- **SUT behaviour errors** (the agent ran but emitted error spans / a tool failed /
  non-zero result) already flow through the trace as `r.errors`; they are ordinary
  data and need no special handling.
- **Harness-level failures** produce an `Evidence` flagged `failed = true` with a
  `failureKind` classified by **which engine call failed**: `"driver"` (the adapter's
  `drv.Run` returned an error) or `"resolve"` (trace correlation failed — not found
  within the poll timeout, a query/fetch error, or context cancellation).
  Classification is by call-site, not by parsing error strings, so it stays robust and
  guess-free. The correlator merges every trace tagged with a run id, so there is no
  "ambiguous match" failure mode to classify. The failure is surfaced (not swallowed):
  it appears in the per-run table (§8) and is assertable via `r.failed` /
  `r.failureKind`.

This lets an author either lump failures (`rate(r, r.failed) < 0.1`) or separate a
trace-fetch flake from a real invocation failure
(`rate(r, r.failureKind == "resolve")`). Because a failed iteration has no trace,
referencing a trace-derived metric on it is a hard error (§4.1), which keeps metric
assertions honest: you must scope them to non-failed runs.

## 7. Concurrency

- **Serial by default.** Each iteration runs to completion before the next, reusing
  the per-target semaphore as-is. Deterministic ordering; cost-bounded (one agent at a
  time); simplest to reason about.
- **`@runs(N,parallel)`** runs iterations concurrently. Each goroutine acquires the
  **existing per-target semaphore** before driving, so the concurrency ceiling is the
  one already configured per target: agent targets (default limit 1) still effectively
  serialize, microservice targets fan out to their limit. Results are written into a
  pre-sized `[]Evidence` indexed by iteration, so the sample order is stable
  regardless of completion order. No new concurrency primitive is introduced beyond
  the existing semaphore plus a `WaitGroup`.

## 8. Reporting

No new seam. On a failed aggregate the comparator returns
`Verdict{Pass:false, Reasons:[...]}` whose reason carries the failing **expression**,
the **sample size**, and a **compact per-run table** — iteration index,
`test.run.id`, `failed`, and `failureKind`:

```
aggregate false: "rate(r, 'search' in r.tools) >= 0.8"  (10 runs)
  run  test.run.id            failed  kind
  0    a1b2c3..               false
  1    c3d4e5..               false
  ...
  9    e5f6a7..               true    resolve
```

This surfaces through godog's existing JUnit and pretty formatters exactly like a
single-run `Verdict.Reasons` (§10 of the foundational design). The `test.run.id`
values are copy-pasteable into the `/traces` skill to inspect the offending run.

*Future refinement (deferred):* the failing assertion as **computed-vs-expected**
(e.g. `rate = 0.60, want >= 0.80`) and a per-run **value column** for the asserted
quantity. Both require surfacing the aggregate's numeric result and evaluating the
macro's inner per-run sub-expression, neither of which the v1 bool-returning program
exposes; the run-id table already preserves the key affordance (jump to the offending
run's trace).

## 9. Error handling / edge cases

Per the no-silent-fallbacks invariant, each of these is a hard, descriptive failure
rather than a guess:

1. **Malformed tag.** `@runs(0)`, `@runs(-1)`, `@runs(abc)`, `@runs(3,foo)`, or a
   non-integer N → error at scenario start naming the offending tag value. N must be a
   positive integer; the only modifier is `parallel`. The tag is a single,
   whitespace-free Gherkin token, so the modifier is comma-separated with **no space**
   (`@runs(10,parallel)`, never `@runs(10, parallel)` — a space would split it into two
   tags).
2. **Pinned / replayed trace + `@runs(N>1)`** → hard error: "cannot multi-run a pinned
   scenario; replay is deterministic". Multi-run is only meaningful against a live,
   re-driven SUT; N identical replays would be a meaningless sample. `@runs(1)` on a
   pinned scenario is allowed (equivalent to the existing replay path).
3. **Percentiles** use **nearest-rank** (no interpolation), are defined for any N≥1,
   and are computed over the *actual* sample — never fabricated or padded. A minimum-N
   gate is **deliberately not added**: it would be an arbitrary policy, and the
   author owns the statistical meaningfulness of `p95` at small N. The per-run table
   in failure output makes the sample size explicit.
4. **`@runs(1)` / no tag** → the existing single-run path with the single-`Evidence`
   comparators; nothing changes. The aggregate path engages only for the
   `the runs satisfy` step.
5. **Empty / malformed CEL expression** in `the runs satisfy` → fail-fast compile
   error at scenario start, identical to the single-run `cel` comparator's behaviour.
6. **Metric aggregate over a sample containing failed runs.** `rate`/`count` take a
   per-run predicate and can scope past failures (`!r.failed && …`). The metric
   helpers (`mean`/`sum`/`min`/`max`/`p50`/`p95`/`p99`/`stddev`) map their projection
   over the *full* `runs` list, so a failed run's missing trace key makes an unscoped
   metric aggregate a hard "no such key" error. This is intentional — a percentile of
   latency is undefined when some runs produced no latency. An author expecting
   possible failures gates first: `count(r, r.failed) == 0` as a separate assertion,
   or asserts only rate-style properties. In-macro scoping for metric helpers is a
   future extension.
7. **Single-run step inside `@runs(N>1)`.** A single-run comparator step (`the run
   satisfies`, budgets, sequence, result) inspects one `Evidence` only, so its meaning
   in a multi-run scenario is ambiguous. `world.check` hard-errors when `n > 1`
   (`"single-run step in a @runs(N) scenario evaluates only the first run; use \"the
   runs satisfy\" …"`) rather than silently evaluating the first run. Pure-aggregate
   scenarios (only `the runs satisfy`) and ordinary single-run scenarios (`n == 1`) are
   unaffected.

## 10. Testing strategy

- **L1 unit (no infra).** Table-driven tests for `aggregate_cel.go` over golden
  `[]Evidence` fixtures: rate/count/percentile/mean correctness, failed-sample
  handling (`r.failed`/`r.failureKind`, and the hard error when a metric is referenced
  on a failed run), and malformed-expression errors. Separate table-driven tests for
  macro expansion (`rate`/`count`/`p95`/… → comprehension). `gomock` for the new
  `AggregateComparator` interface where call/arg verification matters. ≥80% coverage on
  the new package.
- **Engine.** Tests that `DriveN` produces N `Evidence`, mints N distinct
  `test.run.id`s, records a harness failure as a failed sample (serial and parallel),
  and that the parallel path preserves index ordering and respects the per-target
  semaphore.
- **L3 meta-test (mandatory).** A meta-feature that drives a **known-bad statistical
  SUT** — one that exhibits the asserted behaviour only ~50% of the time — and asserts
  that Mentat goes **red** against `the runs satisfy "rate(r, ...) >= 0.8"`. A test
  framework must prove it fails on bad behaviour, not merely pass on good. A companion
  known-good SUT proves the green path.

## 11. Routing

Net-new behaviour (a repeat loop, a new seam + comparator, a new grammar step, and an
L3 meta-feature). TDD owns it: **go-test-writer** drives the red→green→refactor loop;
**go-coder** handles the additive scaffolding (the new registry map, `engine.Build`
wiring, mock regeneration). The `core.AggregateComparator` interface addition is
additive (no existing signature changes), so it is not a breaking one-way door.
