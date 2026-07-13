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
