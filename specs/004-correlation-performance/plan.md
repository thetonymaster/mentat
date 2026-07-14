# Implementation Plan: Correlation Performance — Remove Fixed and Redundant Resolution Costs

**Branch**: `004-correlation-performance` | **Date**: 2026-07-01 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/004-correlation-performance/spec.md`

## Summary

Remove the measured wall-clock waste in trace resolution (audit cluster C, C1–C6)
without weakening any feature-002 correctness contract: release the per-target
semaphore after `drv.Run` so `@runs(N, parallel)` overlaps ingestion waits (C2);
restructure `correlate.Resolve` to keep the last decoded forest and use cheap
per-round change checks, decoding fully once (C1/C5); fan out per-round `GetByID`
fetches with errgroup (C3); add a known-complete resolve mode (StableFor
short-circuit, no sleep) for `mentatctl` replay/format/diff (C4); and hoist
regex/JSON-Schema compilation out of the per-span quantifier loop (C6). Every
change is proven by hermetic counters/timing before any live measurement.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: `golang.org/x/sync/errgroup` (already a transitive
dependency via the semantic matcher), gomock stub stores with call counters,
existing Tempo HTTP client

**Storage**: no schema/config changes required (an internal store-protection
concurrency bound is a constant); poll config semantics unchanged

**Testing**: hermetic gomock/counter tests for decode/fetch counts and overlap;
`go test -race` for compiled-matcher reuse; e2e wall-clock baseline recorded
before/after on the aggregate suite

**Target Platform**: developer workstations + Linux CI

**Project Type**: single Go module — CLI over library packages

**Performance Goals**: ≥40% wall-time drop on the parallel multi-run e2e aggregate
suite (SC-001); `mentatctl diff` < 200ms excluding RTT (SC-003); e2e suite never
slower (SC-005)

**Constraints**: bit-for-bit verdict preservation (SC-004), proven by the
observation-parity regression; stability-gate sensitivity is byte-level
(payload length + hash), strictly stronger than the previous span-count
comparison (FR-002, clarified 2026-07-11); complete-or-loud fetch errors
unchanged (FR-003)

**Scale/Scope**: 5 packages touched (`engine`, `correlate`, `core` + `store` for
the raw-payload seam split fetch/decode, `comparator` matchers) + `internal/ctl`
call sites; ~7 red→green pairs + 1 baseline measurement task

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS (no violations).*

- **I. Evidence-Only Comparators**: PASS. Matcher compile-once stays inside
  comparators; no comparator gains I/O.
- **II. Trace Is a Forest, Tag-First**: PASS. Merge-all-tagged-traces semantics
  unchanged; fan-out fetching preserves order-independent merging.
- **III. Seams Are Interfaces, Wired Once**: PASS. Known-complete mode is a
  `Correlator` capability wired at the composition roots; no new global state.
- **IV. No Silent Fallbacks**: PASS. Change-check failures and fetch errors keep
  their hard-error contracts; the fast path is explicit, never inferred.
- **V. Test-First & Hermetic**: PASS. Counter/overlap assertions are hermetic and
  land red first; live wall-clock numbers are measurements, not the test gate.

## Project Structure

### Documentation (this feature)

```text
specs/004-correlation-performance/
├── plan.md              # This file
├── research.md          # Phase 0: change-check design, semaphore scope, fast path
├── data-model.md        # Phase 1: resolve modes, counters, matcher cache shape
├── quickstart.md        # Phase 1: validation + baseline measurement guide
├── contracts/
│   └── resolution-modes.md   # live vs known-complete resolution contract
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/
├── engine/         # engine.go: semaphore released after drv.Run (C2); resolve outside slot
├── core/           # core.go: TraceStore raw-payload seam (fetch/decode split) + regenerated mocks
├── correlate/      # correlate.go: decode-once + byte-level change check (C1/C5), errgroup fan-out (C3),
│                   #   known-complete mode (C4)
├── store/          # tempo.go: raw /api/traces body accessor; filestore.go: canonical hermetic payload
├── comparator/     # matchers.go / result_span.go: compile-once per expectation (C6)
└── ctl/            # run.go / diff.go / replay call sites use known-complete resolution

cmd/mentatctl/      # wires known-complete mode for historical ids
e2e/                # baseline + after measurement notes for the aggregate suite
```

**Structure Decision**: existing layout; the semaphore-scope change is engine-only.
The polling redesign needs one seam change — `TraceStore` splits fetch from decode
so the resolve loop can hash payload bytes without decoding (research R1 seam
consequence); comparators are untouched.

## Complexity Tracking

No constitution violations — table intentionally empty.

## Resolved Open Questions

### U1 — how replay reaches known-complete resolution (resolved 2026-07-13)

Problem (from the 2026-07-11 analyze session, referenced by tasks.md T013):
`mentatctl agent replay` resolves via the engine (`ctl.ReplayFeature` →
`eng.PinRun(runID)` → `Drive`'s pinned branch at `internal/engine/engine.go:60`
calling `e.cor.Resolve`), not via `ctl.Resolve` — so switching `ctl.Resolve` to
`ResolveComplete` (T013) covers format/diff but not replay.

**Decision**: the engine's pinned branch calls `ResolveComplete` instead of
`Resolve`. A pinned run is by definition saved/historical — `PinRun`'s only
production caller is `ctl.ReplayFeature` (verified 2026-07-13:
`grep -rn PinRun`, replay.go:21 is the sole non-test caller). Live paths
(`Drive` unpinned, `DriveN`) keep the stability-gated `Resolve`, so the fast
path stays unreachable for live scenarios by construction (spec edge case,
research R4) — the pinned branch is only entered via `PinRun`, which only the
historical replay command invokes.

**Alternatives rejected**:
- Correlator adapter (Resolve→ResolveComplete) wired at mentatctl's composition
  root for the replay engine: scopes the fast path to an engine *instance*
  instead of the pinned *branch*; any live Drive on that instance would silently
  fast-path — exactly the accidental-use risk R4's separate-method design exists
  to prevent. More moving parts for a weaker guarantee.
- Replay bypasses the engine (pre-resolve Evidence via `ResolveComplete`, inject
  into steps): large refactor of the steps/engine seam for no contract benefit.

**Consequence for T012/T013**: the replay red test asserts the pinned engine
calls `ResolveComplete` (mock Correlator expects `ResolveComplete`, forbids
`Resolve`); T013's engine-side diff is the one-line pinned-branch switch plus
that test.

**Premise correction (found during T013, 2026-07-13)**: the original U1 analysis
assumed replay reaches `Drive`'s pinned branch. In fact the godog steps always
call `DriveN` (steps.go:161), and `DriveN(n=1)` on a pinned engine bypassed the
pinned branch into `driveOnce`'s live path — production replay re-drove the SUT
and resolved a fresh injected run id (pre-existing regression from the @runs
steps switch Drive→DriveN, masked in ctl tests by an idFn rigged to the pinned
id). Fixed under T013: `DriveN` routes pinned n=1 through `Drive`'s pinned
branch (no Inject, no SUT execution, known-complete resolve), guarded by
`TestDriveNPinnedSingleRunResolvesStoredRunWithoutDriving`. The U1 decision
(pinned branch → `ResolveComplete`) is unchanged; full account in tasks.md T013. Known accepted edge (inherent to FR-004 naming replay as a
known-complete caller): replaying a run whose trace is still mid-ingestion
evaluates the fetch-once snapshot rather than waiting for stability — accepted
at spec time (US3, FR-004 name replay explicitly as historical).
