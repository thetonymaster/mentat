# Tasks: Trace Completeness Contract — Flush Barrier for Sound Absence Assertions

**Input**: Design documents from `/specs/008-trace-completeness/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md,
contracts/completeness-contract.md, quickstart.md — all present. Feature 002
(verdict integrity) MUST be merged first (spec Assumptions, plan risk table).

**Tests**: This project's constitution mandates Test-First / TDD as
NON-NEGOTIABLE (Principle V). Every behaviour task below has its failing test
written first; pin tests of existing behaviour are marked as pins (expected
green immediately, kept as regression guards).

**Organization**: Grouped by user story; each story is an independently testable
increment. Routing follows CLAUDE.md: behaviour tasks → **go-test-writer**;
scaffolding/mocks/docs → **go-coder**; pre-commit audit → **go-reviewer**.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no dependency on an incomplete task)
- **[Story]**: US1 (spawned settle barrier), US2 (request-scoped qualifier), US3 (strict sentinel)

## Phase 1: Setup (Prerequisite Gate)

**Purpose**: enforce the declared ordering dependency before any work starts

- [ ] T001 Verify feature 002 is merged: `internal/correlate/correlate.go` must hard-error on deadline-with-unstable-spans (no `return merged, nil` best-effort path at the deadline). If absent, STOP — 008 must not start (plan risk table).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the contract types, config surface, and seam change every story builds on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [ ] T002 [P] Write failing table-driven tests for the per-target `completeness` config block in `internal/config/config_test.go`: mode parse ("settle" default, "strict", unknown → load error naming target+value), settle duration parse (Go duration, negative → error, zero allowed), kind defaults (2s shell/mcp, 5s http/grpc) per contracts §4 error shapes
- [ ] T003 Implement `config.Completeness` on `Target` (mode, settle, validation, kind defaults) in `internal/config/config.go` — green for T002
- [ ] T004 Add `core.CompletenessContract` + `core.ResolveRequest` types, additive `Verdict.Qualifiers []string`, and change `Correlator.Resolve(ctx, store, req ResolveRequest)` in `internal/core/core.go` per data-model.md
- [ ] T005 Regenerate gomock mocks after the seam change: `go generate ./...`, commit `internal/core/mocks/mock_core.go` (go-coder)
- [ ] T006 Update all `Resolve` callers to the new signature, compile-green with behaviour unchanged: `internal/engine/engine.go` (contract zero-value for now), `internal/ctl/replay.go`, `internal/ctl/diff.go`, `cmd/mentat/main.go`, `cmd/mentatctl/main.go` — ctl historical paths pass `Contract{KnownComplete: true}`; fix all existing tests to compile
- [ ] T007 Write engine contract tests in `internal/engine/engine_test.go`: (a) pin drive-before-resolve ordering with gomock Driver+Correlator (pin — guards FR-001); (b) failing tests that the engine derives `CompletenessContract` from target adapter kind + config (shell→spawned/2s default, http→request/5s, explicit settle and strict mode pass through)
- [ ] T008 Implement contract construction at drive time in `internal/engine/engine.go` — green for T007

**Checkpoint**: `go build ./...` and all existing suites green; contract flows engine→correlator but changes no behaviour yet

---

## Phase 3: User Story 1 - Spawned-Process Runs Judge Only Complete Evidence (Priority: P1) 🎯 MVP

**Goal**: settle-window barrier anchored at drive-return; late-flushing SUT can never produce a green absence verdict

**Independent Test**: quickstart §2 — the late-flush meta-scenario goes RED on every repetition

### Tests for User Story 1 (Test-First) ⚠️

- [ ] T009 [P] [US1] Write failing table-driven tests for settle-mode `Resolve` in `internal/correlate/correlate_test.go`: (a) stable count before settle elapsed → keeps polling; (b) settle elapsed + stability satisfied → concludes; (c) spans arriving late-but-within-settle are included in the forest; (d) deadline with settle unmet → hard error naming the barrier and values (contracts §4 shapes, pinned); (e) `KnownComplete` → exactly one Query/GetByID round, no polling (gomock store call counts)

### Implementation for User Story 1

- [ ] T010 [US1] Implement settle-mode barrier + `KnownComplete` fast path in `internal/correlate/correlate.go`: elapsed measured from `Resolve` entry (= drive-return, engine calls it synchronously); termination = settle elapsed AND 002 stability gate; 002 semantics untouched — green for T009
- [ ] T011 [P] [US1] Add the `late-flush` scenario to `tracelab/researchbot/scenarios.go` (+ unit test in `tracelab/researchbot/scenarios_test.go`): decoy batch + force-flush, sleep past `StableFor × interval` of the harness `mentat.yaml` poll config, then a `delete_record` execute_tool span, flush, exit
- [ ] T012 [US1] Add L3 meta-feature `features/meta/late_flush_bad.feature` (asserts `the tool "delete_record" is never called` → must FAIL) and the late-flush target entry in `mentat.yaml`
- [ ] T013 [US1] Write e2e meta-test `e2e/completeness_meta_test.go` (`//go:build e2e`, prebuilt `mentatBin`, `t.Parallel()` top + per-subtest): drives the late-flush meta-feature N times (N≥3 in CI; SC-001's 20-run proof documented in quickstart §2) asserting non-zero exit + verdict-fail reason naming `delete_record` every time — zero green outcomes

**Checkpoint**: US1 fully functional — the false-green vector on spawned targets is dead; MVP deliverable

---

## Phase 4: User Story 2 - Request-Scoped Runs State Their Weaker Guarantee Honestly (Priority: P2)

**Goal**: ingestion-window qualifier on completeness-sensitive verdicts for bounded (request-scoped, non-strict) runs, rendered in all reports

**Independent Test**: quickstart §4 — orderflow absence verdicts carry the canonical qualifier text; strict target drops it

### Tests for User Story 2 (Test-First) ⚠️

- [ ] T014 [P] [US2] Write failing tests in `internal/steps/steps_test.go`: absence (`never called`), exact-count, budget, error-count, and CEL-aggregate steps mark their expectation completeness-sensitive; presence/`contains`/semantic steps do not
- [ ] T015 [P] [US2] Write failing tests in `internal/engine/engine_test.go`: bounded contract (request kind, settle mode) + sensitive expectation → `Verdict.Qualifiers` contains the canonical text from contracts §3 with the target's effective settle value, on pass AND fail; spawned contract → no qualifier; non-sensitive expectation → no qualifier
- [ ] T018 [P] [US2] Write failing reporter tests in `internal/report/` (console + JUnit reporter test files): verdicts with `Qualifiers` render them verbatim in both formats, pass and fail alike

### Implementation for User Story 2

- [ ] T016 [US2] Implement the sensitivity flag where step expectations are built in `internal/steps/steps.go` — green for T014
- [ ] T017 [US2] Implement qualifier attachment at comparator invocation in `internal/engine/engine.go` (join: sensitivity × bounded contract; canonical text single-sourced as a constant) — green for T015
- [ ] T019 [US2] Implement qualifier rendering in the console and JUnit reporters under `internal/report/` — green for T018
- [ ] T020 [US2] Extend the orderflow e2e (`e2e/orderflow_test.go` or `e2e/completeness_e2e_test.go`): an absence assertion against the http orderflow target produces report output carrying the qualifier with the effective settle value

**Checkpoint**: US1 and US2 independently green; bounded verdicts are visibly bounded

---

## Phase 5: User Story 3 - Strict Mode: The Run Declares Its Own Span Count (Priority: P3)

**Goal**: opt-in exact completeness via the `test.span.count` sentinel; any mismatch is a distinct hard error, never a verdict

**Independent Test**: quickstart §3 — sentinel-good green, sentinel-short hard-errors naming declared/observed, sentinel-dup hard-errors

### Tests for User Story 3 (Test-First) ⚠️

- [ ] T021 [P] [US3] Write failing table-driven tests for the strict state machine in `internal/correlate/correlate_test.go` (data-model.md table): 0 sentinels → keep polling then timeout error; sentinel in a late poll round → picked up (no premature missing-sentinel error); ≥2 → immediate error naming span ids; observed<declared → poll then timeout error naming run id/declared/observed/elapsed; observed==declared → conclude (settle window superseded); observed>declared → immediate error; all messages pinned to contracts §4 shapes

### Implementation for User Story 3

- [ ] T022 [US3] Implement strict mode in `internal/correlate/correlate.go`: per-round sentinel scan of the merged forest for `test.span.count` (self-inclusive count), equality termination, five distinct hard errors — green for T021
- [ ] T023 [P] [US3] Add `sentinel-good`, `sentinel-short` (declares N+2, emits N), and `sentinel-dup` scenarios to `tracelab/researchbot/scenarios.go` + unit tests in `tracelab/researchbot/scenarios_test.go`
- [ ] T024 [US3] Write failing test + implement FR-009 in `internal/engine/engine_test.go` + `internal/engine/engine.go`: strict-mode contract suppresses the qualifier on any adapter kind, including request-scoped
- [ ] T025 [US3] Add strict-mode features and targets: `features/meta/strict_short_bad.feature`, `features/meta/strict_dup_bad.feature`, a passing strict feature under `features/`, and the strict target entries in `mentat.yaml`
- [ ] T026 [US3] Write e2e strict tests in `e2e/strict_meta_test.go` (`//go:build e2e`, prebuilt binary, `t.Parallel()`): good → exit 0, no qualifier in output; short → non-zero exit with declared/observed counts in the error (a resolution error, not a comparator verdict); dup → non-zero exit naming the ambiguity

**Checkpoint**: all three stories independently functional

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T027 [P] Document the SUT completeness contract in `README.md` (new section linking `specs/008-trace-completeness/contracts/completeness-contract.md`): flush-on-exit obligation, per-adapter guarantee table, sentinel format, qualifier meaning, grandchild-process limitation until feature 003 (go-coder)
- [ ] T028 [P] Coordinate the seam change with feature 007: update `specs/007-public-extension-api/contracts/public-surface.md` (or its manifest source if already generated) with the `ResolveRequest`-shaped `Correlator` and `Verdict.Qualifiers` so the public API freezes on the new shape (plan risk table)
- [ ] T029 [P] Add ctl regression tests in `internal/ctl/` test files: replay and diff resolve historical traces with `KnownComplete` (gomock store proves single fetch, no polling) — locks in the audit-C4 win
- [ ] T030 Run the `/coverage` skill; every touched package (`core`, `config`, `correlate`, `engine`, `steps`, `report`, `ctl`, `tracelab/researchbot`) ≥80%; add tests where short
- [ ] T031 Run `make ci` (gofmt, vet, golangci-lint, full test suite) clean, then a **go-reviewer** `gate` audit of the staged diff — PASS verdict required before PR
- [ ] T032 Execute quickstart.md sections 1–5 against `make harness-up` and record observed results (including the SC-001 repeat proof and SC-005 overhead timing) in the PR description

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: gate only — blocks everything if 002 is absent
- **Foundational (Phase 2)**: T002→T003; T004→T005→T006; T007→T008 (T006 before T007's engine tests compile). BLOCKS all user stories
- **US1 (Phase 3)**: after Phase 2. T009→T010; T011→T012→T013 (T013 also needs T010)
- **US2 (Phase 4)**: after Phase 2; independent of US1 code paths (different files except `engine.go` — coordinate T017 with T008/T024). T014→T016; T015→T017; T018→T019; T020 last
- **US3 (Phase 5)**: after Phase 2; T021→T022; T023→T025→T026 (T026 also needs T022); T024 after T017 if US2 done first, else standalone
- **Polish (Phase 6)**: T027/T028/T029 anytime after Phase 2; T030–T032 after all desired stories

### Parallel Opportunities

- Phase 2: T002 ∥ T004 (different files); T005/T006 serial after T004
- US1: T009 ∥ T011 (correlate tests vs tracelab)
- Across stories: US1 (correlate settle), US2 (steps/report), US3 (tracelab scenarios) touch mostly disjoint files — parallel-safe except the shared `internal/engine/engine.go` (T008/T017/T024) and `internal/correlate/correlate.go` (T010/T022): serialize those pairs
- Polish: T027 ∥ T028 ∥ T029

### Parallel Example: after Phase 2 completes

```bash
# Developer/agent A (US1): T009, T011 in parallel, then T010, T012, T013
# Developer/agent B (US2): T014, T015, T018 in parallel, then T016, T017, T019, T020
# Developer/agent C (US3): T021, T023 in parallel, then T022, T024, T025, T026
# Coordinate: engine.go edits (T017 after T008; T024 after T017), correlate.go edits (T022 after T010)
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1 gate + Phase 2 foundational
2. Phase 3 (US1): the late-flush false-green vector is dead on spawned targets
3. **STOP and VALIDATE**: quickstart §1–§2 — this alone justifies the feature
4. US2 (honesty qualifier) and US3 (strict mode) as follow-on increments

### Incremental Delivery

Each story lands as its own reviewed PR (Conventional Commits, no AI
attribution, files staged individually): `feat(correlate): settle-window
completeness barrier` → `feat(report): ingestion-window qualifier` →
`feat(correlate): strict span-count sentinel`.

---

## Notes

- Behaviour tasks (T002/3, T007/8, T009/10, T014–T019, T021/22, T024) route to **go-test-writer**; T005, T006, T011, T023, T025, T027 to **go-coder**; T031 to **go-reviewer**
- Verify each failing test is RED before implementing (VERIFY: ran test — FAIL) per constitution V
- `t.Parallel()` in new table-driven tests unless `t.Setenv`/`t.Chdir`; REQUIRED in the new e2e files
- Commit after each task or story checkpoint; `git add` files individually
