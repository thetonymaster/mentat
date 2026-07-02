# Feature Specification: Correlation Performance — Remove Fixed and Redundant Resolution Costs

**Feature Branch**: `004-correlation-performance`

**Created**: 2026-07-01

**Status**: Draft

**Input**: User description: "Correlation performance — cut the fixed and redundant costs of trace resolution. The 2026-07-01 audit (docs/audits/2026-07-01-codebase-audit.md, cluster C, findings C1–C6) measured where Mentat wastes wall-clock: redundant re-fetch/re-decode every poll round plus a fixed ~600ms stability sleep per run; the per-target concurrency gate held through trace resolution serializing parallel multi-run ingestion waits; serial per-trace fetches; historical replay/diff paying the live stability poll; per-span attribute copying; per-span matcher recompilation. Constraint: the correctness guarantees of feature 002 (stability gate, complete-or-loud search) must be preserved exactly."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Multi-run scenarios finish in overlapped, not summed, time (Priority: P1)

A test author uses `@runs(10, parallel)` for a statistical assertion over an agent
SUT. Today each run's trace-ingestion wait is serialized behind the per-target
concurrency gate — the gate is meant to bound how many SUTs run at once, but it is
also held while Mentat merely waits for the trace store to ingest (audit C2). After
this feature, the concurrency gate bounds only SUT execution; ingestion waits
overlap, so a parallel batch's wall time approaches the slowest single run instead
of the sum of all waits.

**Why this priority**: This is the single largest measured waste (estimated 16–36s
per parallel multi-run scenario) and it defeats the documented purpose of
`parallel`.

**Independent Test**: With a stub store that delays trace availability by a fixed
lag, a parallel 10-run batch completes in ~1 lag rather than ~10 lags (asserted
with generous bounds in a hermetic test).

**Acceptance Scenarios**:

1. **Given** `@runs(10, parallel)` and a per-target concurrency limit of 1,
   **When** the batch executes, **Then** SUT executions remain serialized (limit
   respected) but trace-resolution waits overlap (batch wall time reflects
   overlap, verified hermetically with delayed stub stores).
2. **Given** any single-run scenario, **When** it executes, **Then** behaviour and
   verdicts are unchanged.

---

### User Story 2 - Resolution stops paying for work already done (Priority: P2)

Every scenario waits on the stability poll. Today each poll round re-fetches and
re-decodes every trace in full just to count spans, and even an already-complete
trace pays the full stability window of sleep before resolution returns (audit
C1); within a round, traces are fetched one at a time (C3); and each fetch re-copies
resource attributes onto every span (C5). After this feature, per-round change
detection is cheap (no full re-decode of unchanged traces), per-round fetches
overlap, and the full decode happens once. The stability *guarantee* is unchanged:
evidence is still only released after the configured number of stable
observations.

**Why this priority**: A fixed ~600ms + redundant decode tax on every single run
in every suite; smaller per-instance than US1 but paid universally.

**Independent Test**: A hermetic store counting fetch/decode calls shows: N poll
rounds perform at most 1 full decode per trace plus N cheap checks, and multi-trace
rounds fetch concurrently.

**Acceptance Scenarios**:

1. **Given** a trace already fully ingested, **When** resolution runs, **Then**
   the store's full trace payload is decoded once (not once per round), while the
   stability gate still performs its configured observations.
2. **Given** a run spanning several traces, **When** a poll round fetches them,
   **Then** fetches overlap rather than execute serially (hermetic timing/count
   assertion).
3. **Given** feature 002's instability hard-error conditions, **When** they occur,
   **Then** the same errors fire — no weakening of the gate.

---

### User Story 3 - Inspecting saved runs is instant (Priority: P2)

An operator uses `mentatctl agent replay`, `format`, or `diff` on runs that
completed in the past. Today each command pays the live stability poll (≥600ms of
sleep and repeated fetches) for traces that cannot change, and `diff` pays it twice
(audit C4). After this feature, known-historical resolution fetches once — no
stability sleep — while still failing loudly if the trace is absent.

**Why this priority**: Interactive commands with sub-second potential feel broken
at 1.2s+ of artificial delay; fix is contained.

**Independent Test**: `mentatctl agent diff` on two saved runs completes with no
stability-wait (hermetic: zero sleep observed; live: sub-second excluding network).

**Acceptance Scenarios**:

1. **Given** a saved/historical run id, **When** replay/format/diff resolves it,
   **Then** resolution performs no stability sleep and returns after one complete
   fetch pass.
2. **Given** a historical id with no stored trace, **When** resolution runs,
   **Then** it fails with the same descriptive not-found error as today.

---

### User Story 4 - Assertion evaluation scales with span count (Priority: P3)

A test author asserts `every call to tool X matches schema: ...` over traces with
hundreds of matched spans. Today the pattern/schema is recompiled for every
matched span (audit C6). After this feature, each expectation compiles its
matcher once and reuses it across all spans it evaluates.

**Why this priority**: Only measurable on large traces; correctness untouched;
cheap to fix.

**Independent Test**: A compile-counting test proves one compilation per
expectation regardless of matched-span count.

**Acceptance Scenarios**:

1. **Given** a quantified expectation over M matched spans, **When** it evaluates,
   **Then** pattern/schema compilation happens exactly once and all M evaluations
   reuse it, with identical verdicts to today.

---

### Edge Cases

- Change detection must never mistake "different content, same size" for
  stability — the cheap check must be at least as sensitive as the current
  span-count comparison (the stability gate's observation semantics are the
  feature-002 contract and must not weaken).
- Overlapped per-round fetches must preserve complete-or-loud: any fetch error
  fails the round exactly as the serial version did.
- Releasing the SUT-execution gate before resolution must not allow unbounded
  concurrent resolution — resolution concurrency is bounded separately (store
  protection) but generously enough not to re-serialize batches.
- A trace that grows *between* the last stability observation and the final
  decode must be caught (decode-once must not create a new partial-evidence
  window; the final decode result must satisfy the stability gate's contract).
- Historical fast-path must be impossible to trigger accidentally for live runs —
  it is an explicit mode used only by commands operating on saved run ids.
- Matcher reuse must be safe under parallel scenarios evaluating the same
  expectation objects (no shared mutable compile state races).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The per-target concurrency limit MUST bound only SUT execution;
  trace resolution MUST NOT hold the SUT-execution slot. *(C2)*
- **FR-002**: During stability polling, full trace payload decode MUST happen at
  most once per trace per resolution (plus cheap per-round change checks whose
  sensitivity is at least the current span-count comparison). *(C1, C5)*
- **FR-003**: Within a poll round, per-trace fetches MUST overlap; a failure in
  any fetch MUST fail resolution with the same error contract as serial fetching.
  *(C3)*
- **FR-004**: Resolution MUST offer a known-complete mode that performs a single
  complete fetch pass with no stability sleep, used by the historical-inspection
  commands (replay/format/diff); absence still errors descriptively. Live scenario
  resolution MUST NOT use this mode. *(C4)*
- **FR-005**: Quantified matchers MUST compile their pattern/schema once per
  expectation and reuse it across all evaluated spans, race-free under parallel
  scenarios. *(C6)*
- **FR-006**: All stability-gate and complete-or-loud guarantees from feature 002
  MUST be preserved bit-for-bit: same pass/fail decisions, same error classes, on
  the entire existing test corpus.
- **FR-007**: The performance effects MUST be verifiable hermetically:
  fetch/decode-count assertions and overlap-timing assertions with stub stores —
  not only by live-harness wall-clock measurements.

### Key Entities

- **SUT-execution slot**: the unit the per-target concurrency limit governs;
  after this feature, held only while the SUT runs.
- **Change check**: the cheap per-round observation the stability gate counts,
  replacing full re-decode.
- **Known-complete resolution**: fetch-once mode for immutable historical runs.
- **Compiled expectation**: a matcher compiled once per expectation, reused per
  span.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The parallel multi-run e2e aggregate suite's wall time drops by at
  least 40% against the pre-feature baseline (recorded before/after on the same
  machine/harness).
- **SC-002**: Hermetic counters prove: ≤1 full decode per trace per resolution;
  per-round fetches overlap; zero stability sleep in known-complete mode.
- **SC-003**: `mentatctl agent diff` between two saved runs completes in under
  200ms excluding network round-trips (no fixed sleep component remains).
- **SC-004**: Zero verdict changes: full unit + e2e suites (including the
  feature-002 falsification tests) pass with unchanged outcomes.
- **SC-005**: Total e2e suite wall clock improves or stays equal (never worse).

## Assumptions

- Feature 002 (verdict integrity) lands first; its stability-gate and
  complete-or-loud error contracts are the correctness baseline this feature must
  preserve. If 002 is re-scoped, this feature re-baselines against the then-current
  resolve contract.
- A store-side cheap change signal (e.g. span count from search metadata, payload
  size/hash) is available or derivable without full decode; the exact mechanism is
  implementation-defined behind the resolution seam, as long as FR-002's
  sensitivity requirement holds.
- Resolution concurrency in overlapped mode is bounded by a store-protection
  limit that is not user-facing configuration in this feature (a generous fixed
  bound is acceptable).
- The 40% target (SC-001) assumes the audit's cost model (ingestion wait dominates
  parallel batches); if the live harness contradicts it, the number is renegotiated
  with evidence — the hermetic guarantees (SC-002) stand regardless.
- Findings A*, B*, D*, E*, G1 are out of scope (separate features).
