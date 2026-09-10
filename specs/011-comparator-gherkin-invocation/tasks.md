---

description: "Task list for 011 — custom-comparator Gherkin invocation"
---

# Tasks: Custom-Comparator Gherkin Invocation

**Input**: Design documents from `specs/011-comparator-gherkin-invocation/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md)

**Tests**: The constitution mandates Test-First / TDD as NON-NEGOTIABLE (Principle V). Every
test task below MUST be written and observed FAILING before its implementation. Test tasks are
not optional.

**Organization**: Grouped by user story so each is independently implementable and testable.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1–US4, mapping to the spec's user stories
- Exact file paths in every description

---

## Phase 1: Setup (Baseline)

**Purpose**: Establish that every gate is green *before* anything moves, so a later failure is
attributable to this feature rather than inherited. This is the discipline 010 used and the
reason its central claim was measured rather than argued.

- [ ] T001 Record a clean baseline at the branch point: run `gofmt -l .`, `go vet ./...`, `go vet -tags e2e ./...`, and `go test . -run 'TestFacadeNameabilitySweep|TestPublicSurfaceGolden'`, capturing output in the task notes
- [ ] T002 [P] Confirm `examples/kafkaecho` builds untouched at the branch point (`cd examples/kafkaecho && go build ./...`) — the SC-008 baseline

**Checkpoint**: every gate green and recorded; any later red is this feature's.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The seam and its gate. Plan Phase 2 step **5a**.

**⚠️ CRITICAL**: No user story work begins until this phase is complete. It lands before any
handler exists so the two-line golden diff is attributable to nothing else.

- [ ] T003 Add the `ExpectationParser` interface to `internal/core/core.go`, beside `Comparator` and `Expectation`, with a doc comment stating it is OPTIONAL, discovered by type assertion, and that Mentat never inspects the text
- [ ] T004 Add `type ExpectationParser = core.ExpectationParser` to `mentat.go`, alongside the existing seam aliases
- [ ] T005 Run `go test . -run TestPublicSurfaceGolden` and observe it FAIL on surface drift, then regenerate with `MENTAT_UPDATE_GOLDEN=1` and verify the diff to `specs/007-public-extension-api/contracts/public-surface.golden` is EXACTLY two lines: the `method (ExpectationParser) ParseExpectation(text string) (Expectation, error)` line and the `type ExpectationParser = core.ExpectationParser` line
- [ ] T006 Perform the mutation rehearsal per [contracts/expectation-parser-seam.md](./contracts/expectation-parser-seam.md#falsification): temporarily add `type XProbeSpec struct{ Note string }` and an `XProbe() XProbeSpec` method to `internal/core/core.go`, run `go test . -run TestFacadeNameabilitySweep`, and confirm it fails with `internal/core.XProbeSpec — reached by method (ExpectationParser) XProbe`, then revert both edits
- [ ] T007 Record the T006 transcript as a comment in `surface_test.go` alongside the existing rehearsals (`:66`, `:78`, `:96`, `:116`, `:826`, `:1141`), including the note that deleting the facade alias does NOT fail the sweep because it seeds only from alias targets (`surface_test.go:1191-1198`)
- [ ] T008 Verify `go test . -run 'TestFacadeNameabilitySweep|TestPublicSurfaceGolden'` is green after the T006 revert

**Checkpoint**: the seam is published, nameable, frozen in the golden, and the gate is proven
to catch a regression in it.

---

## Phase 3: User Story 1 — Invoke a registered custom comparator (Priority: P1) 🎯 MVP

**Goal**: A comparator registered with `WithComparator` and implementing `ExpectationParser`
can be named from a `.feature` file, receives its own typed expectation, and its verdict lands
like any built-in's.

**Independent Test**: register a comparator in a test binary, run an inline feature naming it,
assert the suite passes and the verdict is recorded. Hermetic — gomock store, no network.

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

- [ ] T009 [US1] Write `TestCustomComparatorFromGherkin` in `internal/steps/steps_test.go` following the shape of `TestFeatureExercisesGrammarAgainstFakeEngine` (`:58`): build the engine with `engine.Build(cfg, st, cor, engine.WithExtraComparator("revenue-shape", …))`, drive an inline feature using the new phrase with a docstring, and assert `suite.Run() == 0`; observe it FAIL with an undefined-step error

### Implementation for User Story 1

- [ ] T010 [US1] Add the `comparatorSatisfiedByDoc(name string, doc *godog.DocString) error` handler to `internal/steps/steps.go`, following the captures-first/docstring-last convention of `resultMeansDoc` (`:510`); resolve via `w.eng.Comparator(name)`, type-assert `core.ExpectationParser`, call `ParseExpectation(doc.Content)` unmodified, and route the result through `w.checkExp(name, exp, true)` (`:254`)
- [ ] T011 [US1] Add exactly ONE row to `stepDefs` in `internal/steps/metadata.go` in a new sixth group `Extend`, with pattern `^the "([^"]+)" comparator is satisfied by:$`, a non-blank summary and a valid example, bound to the T010 handler (depends on T010)
- [ ] T012 [US1] Verify T009 passes, and assert within it that the comparator received the expectation its own `ParseExpectation` produced, and that qualifiers and judge usage were recorded on the world exactly as for a built-in step

**Checkpoint**: US1 is fully functional. This is the MVP — the capability exists end to end.

---

## Phase 4: User Story 2 — Every failure names what went wrong (Priority: P2)

**Goal**: The three D5 failure modes each produce a loud, specific error. No nil expectation,
no zero-value success, no silently skipped assertion.

**Independent Test**: table-driven test over the three modes, asserting on error text.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [ ] T013 [US2] Write `TestCustomComparatorErrors` in `internal/steps/steps_test.go` as a table with one row per D5 mode — unregistered name, registered-without-`ExpectationParser`, and parser-returns-error — asserting the error contains the offending comparator name, and for the unknown-name row at least one registered name; observe it FAIL
- [ ] T014 [P] [US2] Write `TestEngineComparatorsListsRegisteredNames` in `internal/engine/engine_test.go` asserting `Engine.Comparators()` returns the sorted registered names including one added via `WithExtraComparator`; observe it FAIL (method does not exist)

### Implementation for User Story 2

- [ ] T015 [US2] Add `func (e *Engine) Comparators() []string { return e.reg.Comparators() }` to `internal/engine/engine.go`, mirroring `Reporters()` (`:218`) over the existing `Registry.Comparators()` (`registry.go:125`)
- [ ] T016 [US2] Implement the three error branches in the `comparatorSatisfiedByDoc` handler in `internal/steps/steps.go`: unknown name → error naming the captured name verbatim plus `w.eng.Comparators()`; not an `ExpectationParser` → error naming the comparator and stating it cannot be driven from Gherkin; parse error → `%w`-wrapped and named by comparator
- [ ] T017 [US2] Add a table row to T013 covering a comparator name containing an embedded quote, asserting the error echoes the truncated capture verbatim so the truncation is visible to the author (spec Edge Cases)
- [ ] T018 [US2] Verify T013 and T014 pass, and that no branch returns a nil or zero-value expectation

**Checkpoint**: US1 and US2 both work independently; the seam is usable rather than a guessing
game.

---

## Phase 5: User Story 3 — A first-class documented step (Priority: P3)

**Goal**: The new phrase appears in `mentat steps` and `docs/steps.md` with the built-ins,
and the `stepDefs` drift invariant is preserved rather than relaxed.

**Independent Test**: run the five drift tests and `mentat steps`; confirm the committed
`docs/steps.md` matches.

- [ ] T019 [US3] Regenerate `docs/steps.md` from `stepDefs` so the committed reference carries the new row
- [ ] T020 [US3] Run `go test ./internal/steps/ -run 'TestStepDocs|TestStepMetadata|TestNoDirectStepRegistration' -v` and confirm all five drift tests pass with their assertions **UNMODIFIED** — needing to edit one means the design drifted into Option B and the plan is wrong, not the test (FR-012, SC-003)
- [ ] T021 [P] [US3] Run `go run ./cmd/mentat steps` and confirm the `Extend` group lists the new phrase with a non-blank summary and a valid example
- [ ] T022 [US3] Document the seam in `docs/extending/comparator.md`: the `ExpectationParser` interface, a complete worked comparator implementing it, and the feature-file snippet that drives it (FR-014)

**Checkpoint**: all three stories functional; the step is discoverable and documented.

---

## Phase 6: User Story 4 — Mentat proves it goes red (Priority: P4)

**Goal**: The framework is shown to FAIL correctly on a bad custom comparator. Mandatory by
Constitution V.

**Independent Test**: in-process godog suites asserting non-zero status and the right reason.

**Placement note**: `internal/steps`, NOT `e2e/` — per [research R6](./research.md), the e2e
suite drives a prebuilt `cmd/mentat` binary that structurally cannot contain a Go-registered
comparator, and sits behind `//go:build e2e` which `make ci` never compiles.

- [ ] T023 [US4] Write `TestCustomComparatorGoesRed` in `internal/steps/steps_test.go` following `TestFeatureGoesRedOnBadScenario` (`:103`): register a comparator that returns a failing verdict, run an inline feature naming it, assert `suite.Run() != 0` AND that the output contains the comparator's own reasons — status alone is not sufficient
- [ ] T024 [P] [US4] Write `TestCustomComparatorParseError` in `internal/steps/steps_test.go`: register a comparator whose `ParseExpectation` returns an error, assert the suite fails loudly rather than skipping the assertion, and that the output names the comparator and carries the wrapped cause
- [ ] T025 [US4] Falsify T023 and T024: temporarily make the handler swallow the comparator error (return nil), confirm BOTH tests go red, then revert — a red-proof that does not itself fail when the guard is removed proves nothing
- [ ] T026 [US4] Record the T025 falsification as a comment in `internal/steps/steps_test.go`

**Checkpoint**: all four stories complete and independently verified.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T027 [P] Add `CHANGELOG.md` entries under Added for the `ExpectationParser` seam and the new step, with NO breaking-change entry — nothing existing changes shape (FR-016)
- [ ] T028 [P] Verify the coverage floor with `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`, checking `internal/steps` and `internal/engine` specifically, and confirming the four error paths from Phase 4 are covered rather than riding the package total (SC-009)
- [ ] T029 Run `make ci` and confirm green
- [ ] T030 Run `go vet -tags e2e ./...` and confirm the e2e package still compiles (SC-010) — `make ci` has no e2e target, and this is the hole that left e2e unbuildable for six commits during 010
- [ ] T031 [P] Confirm `examples/kafkaecho` still builds untouched, comparing against the T002 baseline (SC-008)
- [ ] T032 Verify the two plan invariants held: the public-surface golden changed exactly ONCE (in T005) by exactly TWO lines, and no drift-test assertion was edited anywhere in the branch — check with `git diff main -- specs/007-public-extension-api/contracts/public-surface.golden internal/steps/metadata_test.go internal/steps/docs_test.go`
- [ ] T033 Walk [quickstart.md](./quickstart.md) end to end and confirm every "Done when" box

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies — start immediately
- **Foundational (Phase 2)**: depends on Phase 1 — **BLOCKS all user stories**
- **US1 (Phase 3)**: depends on Phase 2
- **US2 (Phase 4)**: depends on US1 — extends the same handler
- **US3 (Phase 5)**: depends on US1 (the row must exist before docs can render it)
- **US4 (Phase 6)**: depends on US1 and US2 — needs both the happy path and the error paths
- **Polish (Phase 7)**: depends on all stories

### Why the stories are not fully independent here

Stated plainly rather than pretending otherwise. US2, US3 and US4 all act on the single
artifact US1 creates — one handler and one `stepDefs` row. US1 is the only story that can
stand alone, and it is the MVP. US2–US4 are hardening, documentation and proof layered on it,
sequential by nature. Splitting them further would create false parallelism.

### Within each story

- Tests written and observed FAILING before implementation (Constitution V)
- Handler before the metadata row (the row references the handler)
- Error paths after the happy path
- Story complete before the next priority

### Parallel Opportunities

Genuinely limited — most tasks touch `internal/steps/steps.go` or `steps_test.go`.

- T002 runs alongside T001
- T014 (`internal/engine/engine_test.go`) runs alongside T013 (`internal/steps/steps_test.go`)
- T021 (`mentat steps` check) runs alongside T019/T020
- T024 runs alongside T023 — different test functions, no shared state
- T027, T028, T031 are independent of each other in Phase 7

---

## Parallel Example: Phase 4

```bash
# Different packages, no shared state:
Task: "T013 TestCustomComparatorErrors in internal/steps/steps_test.go"
Task: "T014 TestEngineComparatorsListsRegisteredNames in internal/engine/engine_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 — baseline recorded
2. Phase 2 — seam published, golden frozen, gate rehearsed (**blocks everything**)
3. Phase 3 — US1
4. **STOP and VALIDATE**: a custom comparator runs from a feature file

At that point the capability exists. Everything after is hardening it.

### Incremental Delivery

1. Setup + Foundational → the seam is public and gated
2. US1 → the capability works (**MVP**)
3. US2 → it fails usefully
4. US3 → it is discoverable and documented
5. US4 → it is proven to go red
6. Polish → gates, coverage, changelog

### Two invariants to watch throughout

1. **The golden changes exactly once, in T005, by exactly two lines.** If it moves again,
   something reached the public surface nobody decided to put there — stop and find out what.
2. **The drift tests are never edited.** Needing to touch `metadata_test.go` or `docs_test.go`
   means the design drifted toward Option B; the plan is wrong rather than the tests. This is
   the operational test of D1's central claim.

---

## Notes

- `[P]` = different files, no dependency on an incomplete task
- Verify tests fail before implementing — a test that never failed proves nothing
- Commit after each task or logical group; stage files individually (`git add .` is forbidden)
- Conventional Commits; no AI attribution
- Routing: **go-test-writer** owns T009–T026 (behaviour change, TDD); **go-coder** owns T003–T005 and T019 (mechanical); **go-reviewer** `gate` before commit
