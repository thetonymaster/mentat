# Tasks: Verdict Integrity — Eliminate Silent False Verdicts

**Input**: Design documents from `/specs/002-verdict-integrity/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/evidence-vocabulary.md

**Tests**: MANDATORY (constitution Principle V). Every behaviour task below is a
red→green pair: the test task MUST be written and observed to FAIL before its
implementation task. Routing: red→green pairs → **go-test-writer**; fixture
migration, mock regen, doc sync → **go-coder**.

**Organization**: by user story (US1–US4 from spec.md); each story is
independently testable and shippable.

## Phase 1: Setup

- [X] T001 Record the pre-feature baseline: confirm the hermetic suite (`go test ./...`) + `go build` green — the full `make ci` gate runs at the phase checkpoints and Phase 7 (T032), not at baseline — and note e2e wall time in specs/002-verdict-integrity/baseline-note.md (go-coder)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: canonical status/kind vocabulary — every story's stores and comparators consume it.

- [X] T002 Failing table test for canonical status/kind constants and `NormalizeStatus`/`NormalizeKind` (wire spellings, canonical spellings, omitted→Unset/unspecified, unknown→descriptive error) in internal/trace/trace_test.go (go-test-writer, red)
- [X] T003 Implement constants + normalizers in internal/trace/trace.go; add `Span.Kind` field (go-test-writer, green)
- [X] T004 Regenerate gomock mocks after `core.Evidence` gains `FailureMsg` (see T016) — placeholder ordering note: run `go generate ./...` whenever core.go changes in this feature (go-coder)

**Checkpoint**: `go test ./internal/trace/` green; vocabulary available.

---

## Phase 3: User Story 1 — Error and kind assertions reflect the real trace (P1) 🎯 MVP

**Goal**: A1 + A5 dead: error/kind assertions faithful on live traces and fixtures.

**Independent Test**: live SUT emits an errored span → `no span has status "ERROR"` fails naming the span (quickstart.md e2e section).

- [X] T005 [P] [US1] Failing tests: Tempo decode maps `STATUS_CODE_ERROR/OK/UNSET`+omitted to canonical, unknown spelling → error naming span+value; `kind` decoded from JSON in internal/store/tempo_test.go (go-test-writer, red)
- [X] T006 [US1] Implement Tempo status/kind normalization: add `kind` to `otlpSpan`, decode via trace normalizers in internal/store/tempo.go (go-test-writer, green)
- [X] T007 [P] [US1] Failing tests: fixture loader accepts canonical+OTLP spellings, rejects unknown status/kind naming span+value, loads optional `kind` in internal/store/filestore_test.go (go-test-writer, red)
- [X] T008 [US1] Implement fixture status/kind normalization in internal/store/filestore.go (go-test-writer, green)
- [X] T009 [US1] Failing test: `errorCount` counts `trace.StatusError` (a canonical-status trace with 2 errored spans → 2; Unset/Ok → 0) in internal/comparator/budgets_test.go (go-test-writer, red)
- [X] T010 [US1] Point `errorCount` (and any `"Error"` literal in comparators/CEL) at the canonical constant in internal/comparator/budgets.go, internal/comparator/cel.go (go-test-writer, green)
- [X] T011 [US1] Failing test: `span.status=`/`span.kind=` selector values validated against canonical sets at parse time (unknown value → authoring error) in internal/comparator/shape_selector_test.go (go-test-writer, red)
- [X] T012 [US1] Implement selector value validation in internal/comparator/shape_selector.go (go-test-writer, green)
- [X] T013 [P] [US1] Migrate all repo fixtures to canonical spellings (testdata/traces/**, features/ expectations if any) — suite stays green (go-coder)
- [X] T014 [US1] Failing e2e (F5): orderflow error-mode meta feature `features/meta/error_status.feature` + e2e/error_status_meta_test.go asserting `mentat run` goes RED with reason naming the errored span (prebuilt mentatBin, t.Parallel) (go-test-writer, red — requires harness)
- [X] T015 [US1] Implement orderflow error mode (scenario header triggers one error-status span) in tracelab/orderflow/system.go + scenario wiring (go-test-writer, green)

**Checkpoint**: US1 independently shippable — error assertions trustworthy.

---

## Phase 4: User Story 2 — A failed harness run can never pass (P1)

**Goal**: A2 + A6 dead: drive/resolve failures loud; no fabricated aggregate inputs.

**Independent Test**: target with nonexistent command + assertion-free scenario → scenario fails with driver error.

- [X] T016 [US2] Failing tests: `driveOnce` resolve-failure Evidence retains real `Output` and sets new `FailureMsg`; driver-failure Evidence sets `FailureMsg` in internal/engine/engine_test.go (go-test-writer, red)
- [X] T017 [US2] Add `FailureMsg` to `core.Evidence` (internal/core/core.go), retain `res.Output` on resolve failure in internal/engine/engine.go; `go generate ./...` for mocks (go-test-writer, green)
- [X] T018 [US2] Failing tests: single-run drive failure fails the scenario with `FailureMsg` in the error; assertion-free scenario (drive step only) goes red on drive failure in internal/steps/steps_test.go (go-test-writer, red)
- [X] T019 [US2] Implement single-run failure surfacing in `world.drive` (n==1 && evs[0].Failed → error) in internal/steps/steps.go (go-test-writer, green)
- [X] T020 [US2] Failing tests: aggregate boundary fields bound from real Output for resolve-failed samples; expression referencing boundary field with a driver-failed sample present → hard error advising `r.failed` guard in internal/comparator/aggregate_cel_test.go (go-test-writer, red)
- [X] T021 [US2] Implement guarded boundary binding in `record()` in internal/comparator/aggregate_cel.go (go-test-writer, green)

**Checkpoint**: US1+US2 = both P1 false-verdict classes dead.

---

## Phase 5: User Story 3 — Incomplete trace evidence is never silent success (P2)

**Goal**: A3 + A4 dead; F3 pinned.

**Independent Test**: poll timeout shorter than ingestion lag → hard instability error, never a partial-evidence verdict.

- [X] T022 [US3] Failing test: deadline with unstable/nonzero spans → error naming run id, span count, stability progress, timeout (replaces best-effort return) in internal/correlate/correlate_test.go (go-test-writer, red)
- [X] T023 [US3] Implement deadline hard error in internal/correlate/correlate.go (go-test-writer, green)
- [X] T024 [P] [US3] Regression pin (F3): `DoAndReturn` captures `core.TraceQuery`, asserts `Tag=="test.run.id" && Value==runID` in internal/correlate/correlate_test.go (go-test-writer — passes immediately, guards invariant §5)
- [X] T025 [US3] Failing tests: `Query` sends explicit `limit`; response length == limit → truncation error naming `poll.searchLimit`; N<limit unaffected in internal/store/tempo_test.go (go-test-writer, red)
- [X] T026 [US3] Implement search limit + truncation guard in internal/store/tempo.go; add `poll.searchLimit` (default 100) in internal/config/config.go (go-test-writer, green)

**Checkpoint**: evidence is stable-and-complete or loud.

---

## Phase 6: User Story 4 — Inputs and reporting cannot corrupt a verdict (P3)

**Goal**: A7 + A8 dead.

**Independent Test**: out-of-range parentIndex fixture fails loading; passing scenario without service.name stays passed with a report note.

- [X] T027 [US4] Failing table test: parentIndex validation (-1 root; valid parent; out-of-range, self-parent, omitted-defaulting-to-self → errors naming span+index) in internal/store/filestore_test.go (go-test-writer, red)
- [X] T028 [US4] Implement parentIndex validation in internal/store/filestore.go (go-test-writer, green)
- [X] T029 [US4] Failing tests: derivation failure yields `DerivationNote` on the report entry, scenario verdict unchanged; note rendered in JSON+HTML in internal/report/derive_test.go, internal/report/report_test.go (go-test-writer, red)
- [X] T030 [US4] Implement non-fatal derivation note (report.Derive + collector entry + renderers) in internal/report/derive.go, internal/report/collector.go, internal/report/html.go; steps After-hook records note instead of erroring in internal/steps/steps.go (go-test-writer, green)

**Checkpoint**: all four stories independently green.

---

## Phase 7: Polish & Cross-Cutting

- [X] T031 [P] Coverage gate: `/coverage` — every touched package ≥80% (go-coder)
- [X] T032 [P] Full regression per quickstart.md: `make ci` + e2e meta suite unchanged verdicts (SC-004) (go-coder)
- [X] T033 Sync contracts/evidence-vocabulary.md pinned error substrings with the implemented messages; changelog note for the intentional fixture-strictness break (go-coder)

---

## Dependencies & Execution Order

- Phase 2 (T002–T003) blocks US1 (stores need the vocabulary); T004 rides T017.
- US1 (P3): T005/T007 [P] after T003; T013 after T008; T014–T015 need the harness and T006/T010.
- US2 (P4): independent of US1 — can start after Phase 2 in parallel with US1.
- US3 (P5): independent; T025–T026 touch tempo.go — serialize with T006 (same file).
- US4 (P6): T027–T028 touch filestore.go — serialize with T008 (same file).
- MVP = Phase 1–3 (US1). Incremental delivery per story; each checkpoint shippable.

## Parallel Example (after Phase 2)

```text
go-test-writer A: T005→T006 (tempo)        |  go-test-writer B: T016→T021 (US2 chain)
go-test-writer A: T009→T012 (comparators)  |  go-coder:        T013 (fixtures)
```

## Implementation Strategy

MVP = US1 (the empirically-proven live false-green). Then US2 (equal severity,
different trigger), US3, US4. Stop and validate at every checkpoint; verdict
parity (SC-004) is checked at each story boundary, not only at the end.
