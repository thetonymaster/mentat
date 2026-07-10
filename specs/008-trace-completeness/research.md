# Phase 0 Research: Trace Completeness Contract

All unknowns from the plan's Technical Context resolved. Findings are grounded in
the current code (`internal/correlate/correlate.go`, `internal/driver/shell.go`,
`internal/driver/http.go`, `internal/core/core.go`, `internal/config/config.go`)
and the sibling feature plans (002, 003, 004, 007).

## R1 — Where the completeness barrier lives

**Decision**: Inside `Correlator.Resolve`, parameterized per run. The seam evolves
from `Resolve(ctx, store, runID)` to `Resolve(ctx, store, req core.ResolveRequest)`
where `ResolveRequest` carries `RunID` and a `CompletenessContract` (kind, mode,
settle window). The engine constructs the contract from the target's config at
drive time and passes it through.

**Rationale**: The settle window is a *minimum observation period* interacting with
the stability loop's termination condition, and strict mode *replaces* the
termination condition (exact count instead of stability) — both are inside the
poll loop, so the correlator must know the contract. The alternative of keeping
the seam frozen and wrapping barriers around `Resolve` in the engine can only
express "sleep, then resolve", which turns the settle window into a fixed
additive sleep — exactly the fixed-cost pattern feature 004 exists to remove.

**Alternatives considered**:
- *Engine-side sleep before Resolve*: rejected — additive latency for every run,
  cannot express strict-count termination, fights feature 004.
- *Contract via `PollConfig` at construction time*: rejected — the contract is
  per-target/per-run (a suite mixes shell and http targets), while the correlator
  is built once at the composition root.
- *A second `StrictResolve` method*: rejected — two methods with 90% shared loop
  logic; the request struct keeps one code path and is open for extension
  (feature 004 can add known-complete replay hints to the same struct).

**Sequencing constraint**: this signature change must land before feature 007
freezes the public API manifest (or amend 007's `contracts/public-surface.md` in
the same PR). `cmd/mentatctl` replay/diff paths (audit C4) get a
`KnownComplete: true` contract that skips barriers and stability for historical
traces — a free step toward C4 without owning it.

## R2 — What signals process exit for spawned targets

**Decision**: The driver's `Run` return *is* the exit signal. `shell.Run` executes
`cmd.Run()` synchronously and the engine calls `Resolve` only after `drv.Run`
returns, so FR-001 already holds structurally. The work is to make it
*contractual*: (a) a unit test pinning drive-before-resolve ordering in the
engine, (b) the completeness contract's observation clock starts at drive-return,
(c) the documented SUT contract requires telemetry flush/shutdown before exit.

**Rationale**: No new IPC or process-watching is needed; the synchronous driver
already provides the barrier. What is missing is the guarantee being written
down, tested, and built upon (settle window anchored at drive-return).

**Alternatives considered**:
- *Explicit exit timestamp in `RunResult`*: deferred — drive-return time observed
  by the engine is sufficient; a field can be added when an async driver appears.
- *Waiting on process-group exit (grandchildren)*: out of scope — feature 003 owns
  process-group lifecycle (audit B1). Documented limitation until 003 lands; the
  settle window is the interim mitigation.

## R3 — Settle window semantics and defaults

**Decision**: The settle window is a **minimum observation period measured from
drive-return** during which resolution keeps polling regardless of stability. The
loop may conclude only when `elapsed(sinceDriveReturn) >= settle` AND the
(feature-002-hardened) stability gate is satisfied. It is NOT a sleep-then-poll:
polling starts immediately, so for runs whose ingestion takes longer than the
settle window (the common case today) the added wall-clock is zero.

Defaults (config-overridable per target, validated at load):
- **spawned (shell, future mcp): 2s** — covers collector batch timeout (~200ms in
  `deploy/otel-collector.yaml` class of config) plus Tempo ingest-to-queryable
  latency, given the SDK already flushed at process exit.
- **request-scoped (http, grpc): 5s** — covers the OTel SDK BatchSpanProcessor
  default schedule delay (5s), the worst-case gap between response-return and the
  SUT's exporter even sending the spans.
- **Zero is permitted** but documented as the weakest configuration (barrier
  reduces to drive-return + stability gate).

**Rationale**: The two defaults encode the two different worst-case export
pipelines. Making the window a minimum-observation period (not a sleep) keeps
SC-005 honest and avoids re-introducing the fixed-sleep tax feature 004 removes.

**Alternatives considered**:
- *One shared default*: rejected — 5s on every spawned run is pure waste; 2s on
  request-scoped runs silently under-covers the 5s SDK default, which is the
  exact false-green this feature kills.
- *Post-stability re-check (settle after stability, then re-poll once)*: rejected —
  equivalent guarantees to minimum-observation but with a harder-to-explain
  contract and a mandatory extra poll round for every run.

## R4 — Sentinel representation (strict mode)

**Decision**: A span attribute `test.span.count` (integer as string, consistent
with OTLP attribute handling in the stores) carried on exactly one span of the
run, counting **all spans of the merged forest including the sentinel-bearing
span itself**. Strict mode is enabled per target in config; resolution scans the
merged forest each round for spans bearing the attribute:
- 0 sentinels → keep polling (the sentinel itself may be in a late batch);
  timeout → hard error "strict mode: no test.span.count sentinel found".
- ≥2 sentinels → immediate hard error naming the span ids.
- 1 sentinel → termination condition becomes `len(Spans) == declared`; timeout
  with `observed < declared` → hard error naming run id, declared, observed,
  elapsed; `observed > declared` → immediate hard error (declaration violated).

Strict mode **supersedes** the settle window (exact count is stronger than a time
bound) but still respects the stability gate's poll interval; the stability
requirement itself is bypassed only in the sense that equality is a stronger,
exact condition — deadline behaviour and zero-span hard errors are unchanged.

**Rationale**: A span attribute travels the existing pipeline (SUT → collector →
Tempo → store decode) with zero new transport; `test.*` matches the existing
correlation-tag namespace (`test.run.id`, `test.scenario`). Self-inclusive
counting makes the check a plain equality against the merged forest size.

**Alternatives considered**:
- *Resource attribute*: rejected — resource attrs are per-process and stamped on
  every span; "exactly one declaration" and late-emission semantics don't fit.
- *Dedicated span name (`mentat.sentinel`)*: rejected as the detection key — names
  are user-space; an attribute key is a narrower, collision-safer contract. The
  harness will still *name* its sentinel span clearly for humans.
- *Out-of-band declaration (file/stdout)*: rejected — breaks Evidence-only
  downstream consumption and adds a second transport; in-trace keeps the
  declaration inside the same evidence the verdict is about.

## R5 — Qualifier plumbing (request-scoped honesty)

**Decision**: `core.Verdict` gains an additive `Qualifiers []string` field. The
**engine** — which owns both the contract and the comparator invocation — appends
the ingestion-window qualifier (exact text in `contracts/completeness-contract.md`)
to verdicts from **completeness-sensitive** comparisons when the run's contract is
bounded (request-scoped, non-strict). Sensitivity is declared where expectations
are built (the steps layer knows whether a step is an absence/aggregate assertion:
`never called`, `exactly N`, budgets, error counts, CEL aggregates), carried as a
boolean on the comparator invocation, not inferred by the comparator itself.
Reporters render qualifiers for both pass and fail verdicts.

**Rationale**: Comparators must not learn adapter kinds (Constitution I). The
steps layer already parses each step and is the single place that knows an
assertion's absence/aggregate nature; the engine is the single place that knows
the run's contract. Joining the two at invocation time keeps both boundaries
clean.

**Alternatives considered**:
- *Scenario-level annotation only*: rejected — FR-004 requires the qualifier on
  the affected verdicts; a scenario banner over-marks presence-only assertions.
- *Comparator self-classification (`Comparator.Kind()`)*: rejected — widens a
  public seam (007 freeze) for what is expectation-level, not comparator-level,
  information (the same result comparator is sensitive for "exactly" and not for
  "contains").

## R6 — Late-flush harness SUT and L3 proof

**Decision**: `tracelab/researchbot` gains scenarios:
- `late-flush`: emit a decoy batch (force-flush), sleep past `StableFor ×
  interval` (config-matched so the pre-fix stability gate would conclude), then
  emit a span calling forbidden tool `delete_record`, flush, exit.
- `sentinel-good`: normal run + correct `test.span.count` sentinel.
- `sentinel-short`: sentinel declares N+2 but the run emits only N (simulates
  spans lost past the timeout).
- `sentinel-dup`: two sentinel-bearing spans.

L3 meta-features under `features/meta/` assert: late-flush + `never called` →
scenario RED (verdict fail on complete forest — never green); strict short-count →
hard error naming declared/observed; dup → hard error. e2e tests follow the
existing convention: prebuilt `mentatBin`, `t.Parallel()` top and per-subtest.

**Rationale**: The late-flush SUT is the empirical proof FR-012 demands — it is
exactly the trace-shape that today's `Resolve` (pre-002, pre-008) would judge
green against a partial forest. Building it in the harness keeps the proof
deterministic and repeatable (SC-001's 20-run check).

**Alternatives considered**:
- *Fixture-only proof*: rejected — audit finding A1's lesson is that fixtures
  sharing the code's assumptions mask exactly this class of bug; the proof must
  ride the live export pipeline (Constitution V's L3 spirit, audit F5).
