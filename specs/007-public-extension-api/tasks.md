# Tasks: Public Extension API — Extensible Without Forking

**Input**: Design documents from `/specs/007-public-extension-api/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/public-surface.md

**Tests**: MANDATORY (constitution Principle V) — red→green pairs via
**go-test-writer**; example module, CI wiring, guides via **go-coder**.

**Dependency note**: feature 003 (registry sealing) must land first; feature
006's file store is the hermetic vehicle for the example and library tests.

## Phase 1: Setup

- [ ] T001 **DEFERRED to T012 (US2 batch).** Original intent — capture a hermetic green `mentat run` golden vs the file store under plain `go test` — is infeasible on the *current* binary: the live CLI injects a random UUID run id (`engine.BuildCorrelator`→`uuid.NewString()`) that the file store (keyed on fixed `runScenario`, `internal/store/filestore.go:180-184`) cannot resolve, and `mentat run` has no run-id pin flag. The pre-recompose reference already exists as `e2e/golden-green.txt`. The hermetic SC-004 golden will instead be captured through `mentat.Run` (deterministic run id + file store) alongside T012's recomposition. Decision by Q, 2026-07-17. (go-coder)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the facade skeleton — every story builds on it.

- [X] T002 Failing external-style test (`package mentat_test`): facade exports compile and alias identity holds — a struct implementing `mentat.Driver` satisfies the internal seam (assignability probe through a registration option) in mentat_external_test.go (go-test-writer, red)
- [X] T003 Create root facade package: type aliases for the six seam interfaces + contract types per data-model.md inventory (nothing more) in mentat.go (go-test-writer, green). Closure forced 4 additions beyond the prose list (RunResult/StoreCaps/JudgeRequest/JudgeVerdict), recorded in data-model.md.

**Checkpoint**: surface exists; stories parallelize.

---

## Phase 3: User Story 1 — Custom adapter without forking (P1) 🎯 MVP

**Goal**: registration options + example extension proving the surface.

**Independent Test**: examples/kafkaecho builds against the facade only, registers a toy driver, runs a feature green in CI.

- [X] T004 [US1] Failing tests: `WithDriver/WithStore/WithComparator/WithJudge` options consumed at composition (before seal); duplicate name → error naming both registrants; registered adapter usable from config by name in mentat_run_test.go (go-test-writer, red). KEY test `TestRunCustomDriverAndStoreGreen` proves a custom driver+store green run via `mentat.Run`.
- [X] T005 [US1] Implement functional options funneling into engine.Build's registration step (pre-seal), duplicate detection in mentat.go, internal/engine/build.go (go-test-writer, green). Funnels via `engine.WithExtra{Driver,Comparator,Judge,Store}` + `applyExtras`; `BuildStore` now variadic.
- [X] T006 [US1] Example module: examples/kafkaecho/{go.mod (replace → repo root), driver.go + store.go (toy driver+store pair — the driver keys its trace on the injected `spec.RunID`, a custom in-mem store serves it; the built-in **file store can't be used** — it snapshots its dir at construction, before the driver runs), testdata/echo.feature, main_test.go running it via `mentat.Run`}. `docs/extending/driver.md` drafted first so the example follows it (SC-005). Green + `-race` clean + zero internal imports; no facade gap needed. (go-coder)
- [X] T007 [US1] CI job: `make example` target (build + run example + import-lint `! grep -rn "mentat/internal" examples/` → SC-001) wired into `ci: lint test cover example`; `.github/workflows/ci.yml` `check` job runs `make example` (separate module — root `go test ./...` never reaches it). (go-coder)

**Checkpoint**: third-party extension demonstrably possible; MVP shippable.

---

## Phase 4: User Story 2 — Library-mode embedding (P1)

**Goal**: `mentat.Run` returns structured results; reentrant; cancellable.

**Independent Test**: external-style test drives a feature file via Run against the file store, asserts verdicts programmatically.

- [X] T008 [US2] **(pulled into MVP — the example's green run requires `Run`)** Failing tests: `Run(ctx, Config, opts...)` returns `Results{Scenarios, Passed, Failed, Interrupted}` with per-scenario name/verdict/reasons matching the report collector; the suite report aggregates (`TotalCost`, and `JudgeTotal` when a scenario made a judge call — nil otherwise, no fabricated zeros) mirror `core.RunReport` (FR-003 "report data"); `LoadConfig` + in-code `Config` both work in mentat_run_test.go (go-test-writer, red). NOTE: `ScenarioResult` now carries `FeatureFile` — `steps.go` passes `scenario.Uri` through `report.Derive` → `core.ScenarioResult` → the facade (post-MVP, Q-blessed 2026-07-17; proof `TestRunScenarioResultCarriesFeatureFile`). Status-equivalence-to-CLI-FAIL proof is T013 (deferred).
- [X] T009 [US2] **(pulled into MVP)** Implement Run (godog execution behind the facade, results adapter from collector) + Results/ScenarioResult types in mentat.go, run.go (go-test-writer, green). ⚠️ Reentrancy across `Run` calls is NOT yet safe: the package-global registry persists custom registrations, so a 2nd `Run` reusing a custom name hits a false collision (tension with US2 acceptance #2 / R3). Clean fix = per-Build registry scoping (wide blast radius across `ResetForTest` tests) → T010/T011 (deferred).
- [X] T010 [US2] Failing tests: two sequential + two concurrent Run calls independent and `-race` clean (no shared registration state); ctx cancellation mid-suite → feature-003 semantics, `Results.Interrupted` set in mentat_run_reentrancy_test.go (go-test-writer, red). Sequential + concurrent were RED on the package-global registry (false store-collision / shared-map race); cancellation was already correct (run.go), so its test went green immediately and now pins the behaviour.
- [X] T011 [US2] Harden Run reentrancy/cancellation as needed. Root fix: the seam registry is now per-engine (`registry.Registry` constructed per `engine.Build`/`BuildStore`, owned by the Engine, sealed at the composition root) instead of package-global — so each Run owns its registrations (no sequential leak, no concurrent race). Reporters stay package-global (post-run rendering, own mutex). Cancellation needed no change. Files: internal/registry/registry.go (struct + methods; reporters split), internal/engine/{build,store,engine,options}.go, internal/comparator/{result,result_span,matchers}.go, internal/judge/judge.go, + test migrations (ResetForTest+global Register → engine.WithExtra* / local registry.New). go-reviewer gate: PASS.
- [ ] T012 [US2] Recompose cmd/mentat over the public path (flags → Config+options → Run → exit-code mapping); golden test: stdout byte-identical to T001 baseline (SC-004; the golden test runs under plain `go test`, file-store hermetic, NOT `//go:build e2e`, so `make ci` enforces parity) in cmd/mentat/main.go, cmd/mentat/main_test.go (go-test-writer, red→green)
- [ ] T013 [US2] L3 meta-test (constitution Principle V, NON-NEGOTIABLE — prove the public run surface goes RED on bad behaviour, not only on cancellation): a scenario that violates its comparators, run through `mentat.Run`, returns `Results` with `Failed > 0`, that scenario's `Pass == false` with non-empty `Reasons`, and an overall status equal to the CLI's FAIL exit in mentat_run_test.go (go-test-writer, red→green). Depends on T009 (Run + Results); independent of T012.

**Checkpoint**: CLI is consumer zero; embedding works; the public surface is proven to fail loudly on bad scenarios.

---

## Phase 5: User Story 3 — Governed surface: manifest, gate, guides (P2)

**Goal**: the one-way door gets a lock.

**Independent Test**: unacknowledged signature change fails the golden surface test naming the symbol.

- [ ] T014 [US3] Failing test: golden surface renderer (go/packages + go/types → canonical exported names/signatures/fields) diffs against contracts/public-surface.golden, failure names the symbol; scratch mutation rehearsal documented in surface_test.go (go-test-writer, red)
- [ ] T015 [US3] Implement renderer + generate initial public-surface.golden (review against data-model.md inventory: every symbol justified, SC-006) in surface_test.go, specs/007-public-extension-api/contracts/public-surface.golden (go-test-writer, green)
- [~] T016 [P] [US3] Seam implementation guides: docs/extending/{driver,store,comparator,judge,evidence}.md — **driver.md DONE** (drafted early for SC-005, alongside T006); store/comparator/judge/evidence remain (deferred to post-MVP-review US3 batch). — contract, constitution obligations (error wrapping, forest, tag-first, evidence-only, judge classification), example walkthrough. docs/extending/driver.md is drafted early (before/alongside T006) so the example follows it (SC-005); the remaining guides may land any time after T003 (go-coder)
- [ ] T017 [P] [US3] Stability policy: pre-1.0 semver process (deliberate/golden-acknowledged/changelogged) in docs/extending/stability.md + README link; changelog entry for the new public surface (go-coder)

**Checkpoint**: surface locked and documented.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T018 [P] Coverage gate `/coverage` ≥80% for the facade package and touched packages (go-coder)
- [ ] T019 [P] Full quickstart.md validation: external tests, example module, golden gate rehearsal, CLI golden diff, `-race` (go-coder)
- [ ] T020 Review pass: confirm no internal type leaks through any public signature (surface golden covers mechanically; human pass for intent, SC-005/SC-006 checklists) (go-coder)

---

## Dependencies & Execution Order

- Feature 003 (sealing) before T005; feature 006 file store before T006/T008 test vehicles.
- Phase 2 blocks everything. US1 (T004–T007) and US2 (T008–T013) share mentat.go/run.go — same go-test-writer chain or serialize greens; US3's gate (T014–T015) lands AFTER US1+US2 stabilize the surface (golden would churn otherwise); guides (T016–T017) [P] anytime after T003.
- Guide ordering (SC-005): the example (T006) must follow the driver guide, so docs/extending/driver.md (part of T016) is drafted before/alongside T006 — not deferred to Phase 5. The rest of T016's guides may land any time after T003.
- T012 (CLI recomposition) is the riskiest task — golden parity is its gate; do it after T009 proves Run on the happy path. T013 (L3 red-path) needs only T009, so it can land as soon as Run exists.
- MVP = Phases 1–3 (US1).

## Parallel Example (after Phase 2)

```text
go-test-writer A: T004→T005→T008→T012→T013 (facade chain + L3 red-path)
go-coder:        driver.md (from T016) → T006→T007 (example module + CI), then T017 + remaining T016 guides in parallel
go-test-writer B: T014→T015 (surface gate, after facade chain stabilizes)
```

## Implementation Strategy

US1 first — the example extension is the proof the whole feature exists for.
US2 makes the CLI consumer zero (T012's golden parity protects users) and proves
the run surface fails loudly (T013's L3 meta-test). US3 last so the golden file
freezes a settled surface, not a moving one. Do NOT tag v1; the stability policy
explicitly defers that decision.

## Out of scope (deferred by decision)

- **Custom-comparator Gherkin invocation** (Q, 2026-07-17): `WithComparator` registers a
  custom comparator and it composes at Build, but invoking it from a `.feature` step needs
  new Gherkin grammar + generic expectation parsing. 007 publishes the registration surface
  only; first-class custom-comparator steps are deferred to a dedicated future spec (008).
  Documented in `run.go` `ComparatorFactory` and `TestRunCustomComparatorAndJudgeCompose`.
  (Custom drivers and stores DO work end-to-end today — see examples/kafkaecho.)
