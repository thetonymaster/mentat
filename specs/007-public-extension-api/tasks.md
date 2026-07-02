# Tasks: Public Extension API — Extensible Without Forking

**Input**: Design documents from `/specs/007-public-extension-api/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/public-surface.md

**Tests**: MANDATORY (constitution Principle V) — red→green pairs via
**go-test-writer**; example module, CI wiring, guides via **go-coder**.

**Dependency note**: feature 003 (registry sealing) must land first; feature
006's file store is the hermetic vehicle for the example and library tests.

## Phase 1: Setup

- [ ] T001 Capture the pre-recomposition CLI golden: green `mentat run` stdout → cmd/mentat/testdata/pre-recompose-golden.txt (SC-004 baseline) (go-coder)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the facade skeleton — every story builds on it.

- [ ] T002 Failing external-style test (`package mentat_test`): facade exports compile and alias identity holds — a struct implementing `mentat.Driver` satisfies the internal seam (assignability probe through a registration option) in mentat_external_test.go (go-test-writer, red)
- [ ] T003 Create root facade package: type aliases for the six seam interfaces + contract types per data-model.md inventory (nothing more) in mentat.go (go-test-writer, green)

**Checkpoint**: surface exists; stories parallelize.

---

## Phase 3: User Story 1 — Custom adapter without forking (P1) 🎯 MVP

**Goal**: registration options + example extension proving the surface.

**Independent Test**: examples/kafkaecho builds against the facade only, registers a toy driver, runs a feature green in CI.

- [ ] T004 [US1] Failing tests: `WithDriver/WithStore/WithComparator/WithJudge` options consumed at composition (before seal); duplicate name → error naming both registrants; registered adapter usable from config by name in mentat_run_test.go (go-test-writer, red)
- [ ] T005 [US1] Implement functional options funneling into engine.Build's registration step (pre-seal), duplicate detection in mentat.go, internal/engine/build.go (go-test-writer, green)
- [ ] T006 [US1] Example module: examples/kafkaecho/{go.mod (replace → repo root), driver.go (toy driver per docs/extending/driver.md), feature file, fixture, main_test.go running it via mentat.Run + file store} (go-coder)
- [ ] T007 [US1] CI job: build + run example module + import lint (`grep -r "mentat/internal" examples/` must be empty → SC-001) in Makefile ci target / CI config (go-coder)

**Checkpoint**: third-party extension demonstrably possible; MVP shippable.

---

## Phase 4: User Story 2 — Library-mode embedding (P1)

**Goal**: `mentat.Run` returns structured results; reentrant; cancellable.

**Independent Test**: external-style test drives a feature file via Run against the file store, asserts verdicts programmatically.

- [ ] T008 [US2] Failing tests: `Run(ctx, Config, opts...)` returns `Results{Scenarios, Passed, Failed, Interrupted}` with per-scenario name/verdict/reasons matching the report collector; `LoadConfig` + in-code `Config` both work in mentat_run_test.go (go-test-writer, red)
- [ ] T009 [US2] Implement Run (fresh sealed composition root per call, godog execution behind the facade, results adapter from collector) + Results/ScenarioResult types in mentat.go, run.go (go-test-writer, green)
- [ ] T010 [US2] Failing tests: two sequential + two concurrent Run calls independent and `-race` clean (no shared registration state); ctx cancellation mid-suite → feature-003 semantics, `Results.Interrupted` set in mentat_run_test.go (go-test-writer, red)
- [ ] T011 [US2] Harden Run reentrancy/cancellation as needed in run.go (go-test-writer, green)
- [ ] T012 [US2] Recompose cmd/mentat over the public path (flags → Config+options → Run → exit-code mapping); golden test: stdout byte-identical to T001 baseline (SC-004) in cmd/mentat/main.go, cmd/mentat/main_test.go (go-test-writer, red→green)

**Checkpoint**: CLI is consumer zero; embedding works.

---

## Phase 5: User Story 3 — Governed surface: manifest, gate, guides (P2)

**Goal**: the one-way door gets a lock.

**Independent Test**: unacknowledged signature change fails the golden surface test naming the symbol.

- [ ] T013 [US3] Failing test: golden surface renderer (go/packages + go/types → canonical exported names/signatures/fields) diffs against contracts/public-surface.golden, failure names the symbol; scratch mutation rehearsal documented in surface_test.go (go-test-writer, red)
- [ ] T014 [US3] Implement renderer + generate initial public-surface.golden (review against data-model.md inventory: every symbol justified, SC-006) in surface_test.go, specs/007-public-extension-api/contracts/public-surface.golden (go-test-writer, green)
- [ ] T015 [P] [US3] Seam implementation guides: docs/extending/{driver,store,comparator,judge,evidence}.md — contract, constitution obligations (error wrapping, forest, tag-first, evidence-only, judge classification), example walkthrough (go-coder)
- [ ] T016 [P] [US3] Stability policy: pre-1.0 semver process (deliberate/golden-acknowledged/changelogged) in docs/extending/stability.md + README link; changelog entry for the new public surface (go-coder)

**Checkpoint**: surface locked and documented.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T017 [P] Coverage gate `/coverage` ≥80% for the facade package and touched packages (go-coder)
- [ ] T018 [P] Full quickstart.md validation: external tests, example module, golden gate rehearsal, CLI golden diff, `-race` (go-coder)
- [ ] T019 Review pass: confirm no internal type leaks through any public signature (surface golden covers mechanically; human pass for intent, SC-005/SC-006 checklists) (go-coder)

---

## Dependencies & Execution Order

- Feature 003 (sealing) before T005; feature 006 file store before T006/T008 test vehicles.
- Phase 2 blocks everything. US1 (T004–T007) and US2 (T008–T012) share mentat.go/run.go — same go-test-writer chain or serialize greens; US3's gate (T013–T014) lands AFTER US1+US2 stabilize the surface (golden would churn otherwise); guides (T015–T016) [P] anytime after T003.
- T012 (CLI recomposition) is the riskiest task — golden parity is its gate; do it after T009 proves Run on the happy path.
- MVP = Phases 1–3 (US1).

## Parallel Example (after Phase 2)

```text
go-test-writer A: T004→T005→T008→T012 (facade chain)
go-coder:        T006→T007 (example module + CI), then T015/T016 (guides) in parallel
go-test-writer B: T013→T014 (surface gate, after facade chain stabilizes)
```

## Implementation Strategy

US1 first — the example extension is the proof the whole feature exists for.
US2 makes the CLI consumer zero (T012's golden parity protects users). US3 last
so the golden file freezes a settled surface, not a moving one. Do NOT tag v1;
the stability policy explicitly defers that decision.
