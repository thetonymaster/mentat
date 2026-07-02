# Tasks: Run Lifecycle — Bounded Runs, Clean Cancellation, No Orphaned SUTs

**Input**: Design documents from `/specs/003-run-lifecycle/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/lifecycle-config.md

**Tests**: MANDATORY (constitution Principle V) — red→green pairs via
**go-test-writer**; config/Makefile/mock scaffolding via **go-coder**. POSIX only.

## Phase 1: Setup

- [ ] T001 Baseline: `make ci` green; record e2e wall time (regression bound SC-006) in specs/003-run-lifecycle/baseline-note.md (go-coder)
- [ ] T002 [P] Helper-process scaffolding: `TestHelperProcess` re-exec pattern (never-exits, grandchild-holds-pipe, ignores-SIGTERM modes) in internal/driver/helper_test.go (go-coder — test infrastructure, no behaviour yet)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: budget config + core plumbing every story reads.

- [ ] T003 Failing tests: `run_timeout`/`kill_grace` parsing — defaults (5m/10s), per-target override, `"unbounded"` opt-in, typo/zero-grace → parse error; resolved `Duration`+`Unbounded bool` (no magic zero) in internal/config/config_test.go (go-test-writer, red)
- [ ] T004 Implement config fields + resolution in internal/config/config.go (go-test-writer, green)
- [ ] T005 Add `RunSpec.KillGrace` to internal/core/core.go + `go generate ./...` mocks (go-coder)

**Checkpoint**: budgets resolvable; stories can start in parallel.

---

## Phase 3: User Story 1 — A hung SUT fails the scenario, tree reaped (P1) 🎯 MVP

**Goal**: B1 dead — timeout + process-group kill + WaitDelay.

**Independent Test**: sleep-forever target with child, 2s budget → fails <2s+grace, `pgrep` finds no survivor.

- [ ] T006 [US1] Failing helper-process tests: never-exits killed at ctx deadline with whole pgid gone after grace; grandchild-holds-pipe → `Run` returns after `WaitDelay` with captured output preserved; ignores-SIGTERM → SIGKILL escalation in internal/driver/shell_test.go (go-test-writer, red)
- [ ] T007 [US1] Implement `Setpgid` process group, `cmd.Cancel` = SIGTERM→SIGKILL(-pgid) after grace, `WaitDelay = KillGrace` in internal/driver/shell.go (go-test-writer, green)
- [ ] T008 [US1] Failing test: engine derives per-run `context.WithTimeout` from scenario ctx + target budget; timeout failure names target/phase/elapsed; "unbounded" skips the timeout in internal/engine/engine_test.go (go-test-writer, red)
- [ ] T009 [US1] Implement run-budget context + phase-attributed failure wrapping in internal/engine/engine.go (go-test-writer, green)
- [ ] T010 [US1] Failing e2e: hung-SUT meta feature (`features/meta/hung_sut.feature`, `run_timeout: 2s`) → red within budget+grace, no surviving pgid member, in e2e/hung_sut_meta_test.go (mentatBin, t.Parallel) (go-test-writer, red→green with T007/T009)

**Checkpoint**: hangs impossible; MVP shippable.

---

## Phase 4: User Story 2 — Interrupt-safe: tree reaped, reports preserved (P1)

**Goal**: B3 dead — signal handling, interrupted reports, atomic writes.

**Independent Test**: SIGTERM mid-suite → reports exist with completed results + interrupted marker, exit 130, no orphan.

- [ ] T011 [P] [US2] Failing tests: collector/report `Interrupted` marker rendered in JSON field, HTML banner, JUnit property; temp+rename atomic emission in internal/report/collector_test.go, internal/report/report_test.go (go-test-writer, red)
- [ ] T012 [US2] Implement marker + atomic writes in internal/report/collector.go, internal/report/json.go, internal/report/html.go, internal/report/junit-path in cmd/mentat/main.go (go-test-writer, green)
- [ ] T013 [US2] Failing test: `signal.NotifyContext` wiring — suite ctx cancelled on SIGTERM, reports still emitted, exit 130, second signal force-exits; child-process test driving a built `mentat` binary in cmd/mentat/signal_test.go (go-test-writer, red)
- [ ] T014 [US2] Implement signal handling + always-emit reports + exit codes in cmd/mentat/main.go (go-test-writer, green)

**Checkpoint**: CI cancellation is safe.

---

## Phase 5: User Story 3 — Scenario budget reaches everything (P2)

**Goal**: B2 dead — godog ctx threaded end-to-end.

**Independent Test**: scenario timeout < SUT runtime → phase-attributed failure; judge stall cancelled.

- [ ] T015 [US3] Failing tests: `world` carries scenario ctx from `sc.Before`; Drive/Compare/Aggregate receive it (mock observes deadline); judge-phase timeout attributed in internal/steps/steps_test.go (go-test-writer, red)
- [ ] T016 [US3] Convert step defs to context-aware signatures; delete every `context.Background()` in internal/steps/steps.go (go-test-writer, green)
- [ ] T017 [US3] Failing test: `ctl.ReplayFeature` caller cancellation reaches the steps (previously dead path) in internal/ctl/replay_test.go (go-test-writer, red)
- [ ] T018 [US3] Verify/fix ctx flow through DefaultContext in internal/ctl/replay.go (go-test-writer, green)
- [ ] T019 [US3] Guard test: grep-style test asserting no `context.Background()` in internal/steps (non-test files) in internal/steps/ctx_guard_test.go (go-test-writer)

**Checkpoint**: one budget bounds drive, resolve, assert, judge.

---

## Phase 6: User Story 4 — Doomed batches stop; registries sealed (P3)

**Goal**: B4 + B5 dead.

**Independent Test**: parallel batch with structural failure drives <N; post-seal Register panics.

- [ ] T020 [P] [US4] Failing test: parallel `@runs(N)` — structural error cancels not-yet-started iterations (drive-count < N asserted via mock) in internal/engine/engine_test.go (go-test-writer, red)
- [ ] T021 [US4] Implement batch `context.WithCancel` + pre-drive checks in internal/engine/engine.go (go-test-writer, green)
- [ ] T022 [P] [US4] Failing tests: `registry.Seal()` — post-seal Register panics with sealed message; mutex-guarded maps race-clean under `-race`; `ResetForTest(t)` reopens in internal/registry/registry_test.go (go-test-writer, red)
- [ ] T023 [US4] Implement Seal/mutex/ResetForTest in internal/registry/registry.go; call Seal at end of engine.Build/BuildStore in internal/engine/build.go, internal/engine/store.go; migrate the serialized tests noted at internal/steps/steps_test.go:1087 to ResetForTest (go-test-writer, green)

**Checkpoint**: all stories green.

---

## Phase 7: Polish & Cross-Cutting

- [ ] T024 [P] Coverage gate `/coverage` ≥80% touched packages (go-coder)
- [ ] T025 [P] Full quickstart.md validation incl. `-race`; e2e wall clock within +5% of T001 baseline (go-coder)
- [ ] T026 Sync contracts/lifecycle-config.md (defaults, exit codes, message shapes) with implementation; README/config docs for `run_timeout`/`kill_grace`/unbounded (go-coder)

---

## Dependencies & Execution Order

- Phase 2 (T003–T005) blocks US1 (budget config) and US3 (ctx composition).
- US1: T006→T007 (driver) and T008→T009 (engine) can interleave; T010 last.
- US2: independent of US1 except exit-code composition — can run in parallel after Phase 2; T011 [P] with T013's red.
- US3: after Phase 2; touches steps.go — serialize with any US2 steps edits (none) and with feature-002 steps work if concurrent.
- US4: T020/T022 [P] (different packages); T023 touches build.go (serialize with nothing here).
- MVP = Phases 1–3.

## Parallel Example (after Phase 2)

```text
go-test-writer A: T006→T007→T008→T009 (US1)   |  go-test-writer B: T011→T014 (US2)
go-test-writer C: T022→T023 (registry, US4)   |  go-coder:        T002 helpers (done in Phase 1)
```

## Implementation Strategy

US1 first (hangs are the expensive failure), US2 second (CI cancellation is
routine), then US3 (makes budgets total), US4 last (narrow triggers). Validate
the SC-006 wall-clock bound at each checkpoint, not only at the end.
