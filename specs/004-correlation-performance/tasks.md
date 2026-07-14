# Tasks: Correlation Performance — Remove Fixed and Redundant Resolution Costs

**Input**: Design documents from `/specs/004-correlation-performance/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/resolution-modes.md

**Tests**: MANDATORY (constitution Principle V). Hermetic counter/overlap
assertions are the merge gate (FR-007); live wall-clock numbers are evidence
only. Red→green pairs → **go-test-writer**; measurement/docs → **go-coder**.

**Dependency note**: feature 002 must be merged first — its resolve error
contracts (stability gate, complete-or-loud) are the regression baseline here.

## Phase 1: Setup

- [X] T001 Record live baseline per research R6: 3× median wall time of `go test -tags e2e ./e2e/ -run TestAggregate` and one `@runs(10, parallel)` scenario, into specs/004-correlation-performance/baseline.md (go-coder — requires harness)
- [X] T002 [P] Counting/delayed stub-store test helpers (gomock `DoAndReturn` wrappers counting Query/GetByID/decodes; configurable availability lag) in internal/correlate/counterstore_test.go (go-coder — test infrastructure)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: none beyond setup — stories are mutually independent; Phase 2 intentionally empty.

**Checkpoint**: stories can start immediately after Setup.

---

## Phase 3: User Story 1 — Parallel multi-run waits overlap (P1) 🎯 MVP

**Goal**: C2 dead — semaphore bounds SUT execution only.

**Independent Test**: delayed stub store, `@runs(10, parallel)`, limit 1 → batch wall time ≪ 10× lag; SUT drives still serialized.

- [X] T003 [US1] Failing tests: with per-target limit 1 and 300ms resolve lag, batch completes < 4× lag (overlap) while drive order stays serialized (drive-time assertions via mock); resolve concurrency capped at 8 in internal/engine/engine_test.go (go-test-writer, red)
- [X] T004 [US1] Release semaphore immediately after `drv.Run` (all paths); add internal resolve-bound (const 8) in internal/engine/engine.go (go-test-writer, green)

**Checkpoint**: the largest measured waste is gone; MVP shippable.

---

## Phase 4: User Story 2 — Resolution stops paying for finished work (P2)

**Goal**: C1 + C3 + C5 dead — decode-once, cheap change checks, fan-out fetches.

**Independent Test**: counting store shows ≤1 full decode per trace per resolution; per-round fetches overlap; 002's error contracts unchanged.

- [X] T005 [US2] Failing tests: N-round stability poll performs ≤1 decode per trace + N cheap checks (payload len+hash); changed payload → decode + stability reset; byte-change-same-spancount counts as instability; observation-parity replay — the existing corpus poll sequences (growing 1,2,3,3,3; strictly-growing; constant-trace) produce the same per-round stable/reset decisions as the span-count baseline (FR-006 guard); unstable-at-deadline error names byte-change-at-constant-span-count; returned forest is the last-decoded one in internal/correlate/correlate_test.go (go-test-writer, red)
- [X] T006 [US2] Implement per-ref `{len, hash, forest}` cache + decode-once loop in internal/correlate/correlate.go. Seam change (research R1, investigation N1 — the resolve loop cannot see bytes today): split fetch from decode on `TraceStore` (raw-payload accessor + decode) in internal/core/core.go (`go generate` mocks); Tempo returns the exact /api/traces body in internal/store/tempo.go; InMemStore derives a deterministic canonical serialization of its stored forest in internal/store/filestore.go (go-test-writer, green)
- [X] T007 [US2] Failing tests: multi-ref rounds fetch concurrently (timing/count assertion via delayed store); first fetch error fails resolution with the existing wrapped error; merge order deterministic in internal/correlate/correlate_test.go (go-test-writer, red)
- [X] T008 [US2] Implement errgroup per-round fan-out with ref-order merge in internal/correlate/correlate.go (go-test-writer, green)
- [X] T009 [US2] Regression guard: re-run the feature-002 guard tests unchanged — `TestResolveDeadlineUnstableSpansIsHardError`, `TestResolveTimeoutZeroSpans` (internal/correlate/correlate_test.go), `TestTempoQueryTruncationGuard` (internal/store/tempo_test.go) — no edits expected, verify green. Known exception (investigation N1): `TestResolveStablePollsUntilCountStable` pins GetByID calls==5 and must be rewritten against the new store-call pattern while still proving the stability-path exit, not the timeout path (go-test-writer). COMPLETED 2026-07-13 with one recorded deviation: `TestResolveDeadlineUnstableSpansIsHardError` could not pass unedited — its gomock stub encodes the old GetByID seam (same exception class as F5's call-count pin); stub block swapped to FetchPayload/DecodePayload, every assertion line byte-identical to HEAD; other two guards zero-edit green; the known-exception guard `TestResolveStablePollsUntilCountStable` was rewritten against the new FetchPayload/DecodePayload call pattern as this task predicted, still proving the stability-path exit (not the timeout path)

**Checkpoint**: universal per-run tax removed; 002 contracts intact.

---

## Phase 5: User Story 3 — Historical inspection is instant (P2)

**Goal**: C4 dead — `ResolveComplete` fetch-once mode for ctl.

**Independent Test**: mentatctl diff on saved runs: zero stability sleep observed hermetically; <200ms + RTTs live.

- [X] T010 [US3] Failing tests: `ResolveComplete` — one query + one fan-out fetch pass, zero sleeps (counting store + elapsed bound), absent trace → existing not-found error in internal/correlate/correlate_test.go (go-test-writer, red)
- [X] T011 [US3] Add `ResolveComplete` to the `Correlator` seam (internal/core/core.go, `go generate` mocks) and implement in internal/correlate/correlate.go (go-test-writer, green)
- [X] T012 [US3] Failing tests: replay/format/diff call `ResolveComplete` (mock asserts no stability polling); diff resolves both runs concurrently in internal/ctl/ctl_test.go, internal/ctl/diff_test.go (go-test-writer, red)
- [X] T013 [US3] Switch the shared historical resolve helper `ctl.Resolve` → `ResolveComplete` in internal/ctl/ctl.go (this covers the format/diff call sites in cmd/mentatctl/main.go:119/129/139, which all route through `ctl.Resolve`) + make diff resolve both runs concurrently in internal/ctl/diff.go. Do NOT switch the live drive path internal/ctl/run.go — it has no historical resolve and FR-004 forbids it. NOTE: replay resolves via the engine (`ReplayFeature(ctx, eng, …)`, not `ctl.Resolve`), so routing replay through known-complete needs an engine resolve path — see U1 (resolve in plan before implementing) (go-test-writer, green). COMPLETED 2026-07-13 with one discovered-and-fixed deviation from U1's premise: U1 assumed replay reaches `Drive`'s pinned branch, but the godog steps ALWAYS call `DriveN` (steps.go:161), and `DriveN(n=1)` on a pinned engine bypassed the pinned branch into `driveOnce`'s live path (re-drove the SUT, resolved a FRESH injected run id) — a pre-existing regression from the @runs steps switch (Drive→DriveN), masked in ctl tests by idFn rigged to the pinned id. Fixed: `DriveN` routes pinned n=1 through `Drive`'s pinned branch (engine.go), pinned by `TestDriveNPinnedSingleRunResolvesStoredRunWithoutDriving`. U1's decision (pinned branch → ResolveComplete) stands unchanged; only its call-chain premise needed repair.

**Checkpoint**: interactive commands sub-second.

---

## Phase 6: User Story 4 — Matchers compile once (P3)

**Goal**: C6 dead.

**Independent Test**: compile counter: 1 compilation per expectation over 500 matched spans; identical verdicts; `-race` clean.

- [X] T014 [P] [US4] Failing tests: regex/schema compile exactly once per expectation regardless of matched-span count (counter hook); compile error surfaces at construction (authoring time), not at Match; golden verdicts unchanged; parallel-scenario `-race` clean in internal/comparator/matchers_test.go, internal/comparator/result_span_test.go (go-test-writer, red)
- [X] T015 [US4] Hoist compilation to expectation construction (same lifecycle as CEL precompile) in internal/comparator/matchers.go, internal/comparator/result_span.go (go-test-writer, green)

**Checkpoint**: all stories green hermetically.

---

## Phase 7: Polish & Cross-Cutting

- [X] T016 [P] Coverage gate `/coverage` ≥80% touched packages (go-coder)
- [X] T017 Live measurement per quickstart.md: aggregate-suite median vs T001 baseline (target ≥40%, SC-001); `mentatctl diff` timing (SC-003); full e2e wall clock ≤ baseline (SC-005); append results to specs/004-correlation-performance/baseline.md (go-coder — requires harness). COMPLETED 2026-07-13: SC-001b −47.6% (MET), SC-003 ≈10–15ms excl. RTT (MET), SC-005 +0.8% equal-within-noise, SC-004 zero verdict changes; SC-001a (aggregate suite) −6.8% MISSED — fixture is @runs(2) sequential where US1 overlap cannot apply; renegotiation evidence in baseline.md per spec Assumptions
- [X] T018 `make ci` verdict-parity gate (SC-004) + sync contracts/resolution-modes.md if any contract wording drifted (go-coder). COMPLETED 2026-07-13: make ci EXIT=0; contract checked against implementation — no drift, file unchanged

---

## Dependencies & Execution Order

- Feature 002 merged first (baseline contracts). No intra-feature foundational phase.
- US1 (engine) is file-disjoint from US2/US3 (correlate) and US4 (comparator) → all four stories parallelizable after Setup.
- Within correlate: US2 (T005–T009) before US3 (T010–T013) — same file, and ResolveComplete reuses the fan-out fetch pass from T008.
- T006 and T011 touch core.go/mocks (raw-payload seam split; ResolveComplete) — coordinate with any concurrent feature editing core.
- MVP = Phase 3 (US1) alone: biggest win, smallest diff.

## Parallel Example (after Setup)

```text
go-test-writer A: T003→T004 (engine, US1)     |  go-test-writer B: T005→T013 (correlate chain, US2→US3)
go-test-writer C: T014→T015 (matchers, US4)   |  go-coder:        T001 baseline (harness)
```

## Implementation Strategy

Ship US1 first and re-measure — it may alone hit the 40% target. US2/US3 next
(shared file, natural chain), US4 anytime. Never trade a 002 error contract for
speed; T009 exists to prove it.
