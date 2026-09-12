---

description: "Task list for 014-freeze-contributed-phrases"
---

# Tasks: Freeze Contributed Phrases During Engine Composition

**Input**: Design documents from `/specs/014-freeze-contributed-phrases/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/phrase-snapshot.md](contracts/phrase-snapshot.md), [quickstart.md](quickstart.md)

**Tests**: This project's constitution mandates Test-First / TDD as NON-NEGOTIABLE (Principle V). Test tasks are REQUIRED and each test MUST be written to FAIL before its implementation. Do not treat the test tasks below as optional.

**Organization**: Tasks are grouped by user story. **Read the note below before assuming the stories are independent.**

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

Single Go module at repository root (`github.com/thetonymaster/mentat`). Go tests live
beside their source (`internal/engine/engine_test.go`), and facade-level black-box tests
live in the **root package** (`custom_phrase_facade_test.go`). There is no `tests/`
directory — do not create one.

---

## ⚠️ Story independence: an honest caveat

The template assumes each story is a separately deliverable slice. **That is only
partly true here**, and pretending otherwise would produce fake TDD.

This feature has **three symptoms but one mechanism** (research R2). All three surfaces
reach phrases through a single caller, `resolvePhrases`
(`internal/steps/phrase.go:291`). Consequences for sequencing:

| Story | Genuinely red before its implementation? | Why |
| --- | --- | --- |
| **US1** | ✅ Yes | The freeze does not exist yet. Its tests fail against today's code. |
| **US2** | ❌ **No** | US1's mechanism makes `StepReference` consistent as a side effect. Its red must come from a **mutation rehearsal**, not from ordering. |
| **US3** | ✅ Yes | US1 lands the freeze but still hands out the snapshot by reference, so T021 fails until Phase 5 adds the return-side copy. |

**One test moved out of US3 into US1** (research R8, corrected). The "a comparator cannot
mutate the engine's answer" test (US3 §2 / FR-002) is red *before* the freeze and green
after, because today's engine re-invokes the comparator and observes the mutation. Its RED
belongs to Phase 3, so it is now **T008a**. Left in Phase 5 it would have been
unachievable there and paired with a no-op implementation task — the exact failure mode
this repo has recorded twice (011's un-achievable T009 red, 012's mutation that never
fired).

US2 is flagged rather than disguised. 012 recorded a rehearsal that silently failed to
apply, and noted that "the mutation didn't fire" and "the guard is real" are
indistinguishable from test output alone — so T029 asserts the mutation applied before
trusting its RED.

**MVP = Phase 1 + 2 + 3 (US1).** That alone closes the defect's primary harm.

---

## Phase 1: Setup

- [X] T001 Record the pre-change baseline: run `make ci`, then `go test -race ./internal/engine/... ./internal/steps/... .`, and note the result in the PR description so a later red is attributable rather than ambiguous
- [X] T002 Confirm the e2e lane compiles before any change with `go test -tags e2e -run XXX ./e2e/...` (compile-only; `make ci` does NOT cover this lane — the trap 012 hit)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: shared test doubles every story needs. **No behaviour changes in this phase.**

- [X] T003 [P] Add a counting phrase contributor and a state-changing phrase contributor (returns pattern A on first call, pattern B thereafter) as test doubles in `internal/engine/engine_test.go`, alongside the existing `phraseStubComparator`
- [X] T004 [P] Add the equivalent counting and state-changing contributors as test doubles in `custom_phrase_facade_test.go`, following the shape of the existing `phraseRevenue` double
- [X] T005 Confirm the full isolation oracle passes **before** any change, with `go test -race -run 'TestContributedPhrasesAreScoped|TestAForeignPhraseIsUnbound' .` — all three tests in `custom_phrase_isolation_test.go`. The regex MUST include `TestAForeignPhraseIsUnboundUnderAnotherEngine` (`:159`), whose own comment says it "is the other half, and **the one that actually detects a leak**": the two `…AreScoped…` tests would still pass if every engine saw every phrase, because each engine's own sentence binds either way

**Checkpoint**: doubles compile, existing suite still green, oracle confirmed green.

---

## Phase 3: User Story 1 - Validation describes the run that will actually happen (Priority: P1) 🎯 MVP

**Goal**: one phrase set per engine, captured at composition, observed identically by `Validate` and `Run`.

**Independent Test**: build one engine whose comparator returns a different pattern on every call; validate then run a feature file written against the validated sentence; the step binds and executes instead of coming back unbound.

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

> Write these FIRST and observe them FAIL against unfrozen code.

- [X] T006 [P] [US1] Failing test in `internal/engine/engine_test.go`: a counting contributor is invoked exactly once per engine no matter how many times `ContributedPhrases` is called (SC-002, FR-001)
- [X] T007 [US1] Failing test in `internal/engine/engine_test.go`: a state-changing contributor yields the same bindings on every call to `ContributedPhrases` (FR-003). Same file as T006, so not marked [P]
- [X] T008 [P] [US1] **RELOCATED to `internal/steps/phrase_test.go`** (spec D2 / research R11 — measured during implementation). Failing test: **one** `*engine.Engine` drives all three surfaces (`EngineStepDocs` → `EngineStepChecks` → `resolvePhrases`/`InitializerWithBudget`) with a state-changing contributor, and all three must observe one vocabulary (SC-001, FR-005). **Subsumes T018.** The originally-planned facade version is impossible: each facade entry point builds its own engine (`run.go:344`, and `:682` via `buildEngineForInspection`) and consults each contributor exactly once — before *and* after the freeze — so a facade test is permanently red with a shared contributor instance or permanently green with a fresh one, and proves nothing either way. A labelled characterization test stays at the facade pinning what it *can* prove: no entry point resolves phrases twice per engine
- [X] T008a [US1] Failing test in `internal/engine/engine_test.go`: a contributor that retains and mutates the slice it returned during `Build` cannot change the engine's answer (FR-002, US3 §2). **This is red today** because the engine re-invokes the comparator on every call, so the mutation is observed — its RED comes from the missing freeze, not from a missing copy (research R8). Same file as T006/T007, so not marked [P]
- [X] T009 [US1] Confirm T006–T008a are RED and that each failure message names the drift, not an incidental error

### Implementation for User Story 1

- [X] T010 [US1] Add the unexported snapshot field `phrases []PhraseBinding` to `Engine` in `internal/engine/engine.go`, with a doc comment stating it is per-engine, written only by `Build`, and that a hand-constructed `Engine` reads nil as "no phrases" (research R4)
- [X] T011 [US1] Capture the snapshot in `internal/engine/build.go` after `reg.Seal()` (`:149`) and before the `&Engine{…}` return (`:150`): walk `reg.Comparators()` in sorted order, resolve each, type-assert `core.PhraseContributor`, and collect its phrases as `PhraseBinding` values preserving declaration order within each comparator (FR-006). This element-wise transform **is** the capture-time copy — `range` copies each element and `PhraseBinding{Phrase: p}` copies it again into a fresh backing array, so FR-002 is satisfied here and needs no separate copying step (research R8)
- [X] T012 [US1] Relocate the defensive "listed by the registry but cannot be resolved" error from `internal/engine/engine.go:251` into the `Build` capture loop in `internal/engine/build.go`, keeping its message text intact (research R3)
- [X] T013 [US1] Rewrite `Engine.ContributedPhrases` in `internal/engine/engine.go` to return the snapshot, changing the signature from `([]PhraseBinding, error)` to `[]PhraseBinding`; update the doc comment so it describes capture-at-build rather than per-call resolution
- [X] T014 [US1] Update the sole production caller in `internal/steps/phrase.go:291` (`resolvePhrases`) to drop the now-removed error check; `resolvePhrases` keeps its own `error` return for V1–V5
- [X] T015 [US1] Update **both** existing call sites of the two-value form in `internal/engine/engine_test.go` for the new signature — `TestEngineContributedPhrasesResolvesInSortedComparatorOrder` (`:1877`) and `TestEngineContributedPhrasesIsEmptyWithoutContributors` (`:1928`) — preserving every existing assertion unchanged; both fail to compile otherwise
- [X] T015a [US1] **Invert the one test that asserts the removed behaviour**, per spec **D1**: in `TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine` (`internal/steps/phrase_test.go:820`) change the final assertion from `after == 2` to `after == 1` and rewrite the comment above it to state that re-resolution returns the snapshot captured at build. **Leave the test's name and its first assertion (`before` stays 1) unchanged** — both already describe the frozen behaviour. Record in the test comment that this inversion is D1, so a future reader sees a deliberate contract change rather than a relaxed assertion
- [X] T016 [US1] Confirm T006–T008a are GREEN (all four, including the FR-002 guard) and the full suite passes with `go test -race ./internal/engine/... ./internal/steps/... .` — this is achievable only once T015 and T015a have landed

**Checkpoint**: the phrase set is frozen at composition and consistent across `Validate` and `Run`. Copies are NOT yet in place — that is Phase 5, deliberately.

---

## Phase 4: User Story 2 - The step reference documents the phrases that bind (Priority: P2)

**Goal**: `StepReference` documents exactly the phrases the runner binds.

**Independent Test**: with a state-changing contributor, render the step reference and register the suite; documented and bound patterns are the same set in the same order.

**⚠️ TDD note**: T017 will pass as soon as Phase 3 lands, because all three surfaces share
one caller. Its red is established by the mutation rehearsal in T029, not by ordering.
This is stated rather than worked around.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [X] T017 [P] [US2] Test in `custom_phrase_facade_test.go`: `mentat.StepReference` and `mentat.Run` observe the same phrase set from a state-changing contributor — every documented contributed phrase binds, and every bound contributed phrase is documented (FR-005)
- [X] T018 [P] [US2] Test in `internal/steps/phrase_test.go`: `EngineStepDocs` and `EngineStepChecks` resolve identical phrase sets for one engine across repeated calls in mixed order

### Implementation for User Story 2

- [X] T019 [US2] No production change is expected — verify T017–T018 pass on the Phase 3 mechanism alone. **If either fails, stop**: an unshared resolution path exists that research R2 did not find, and the plan's "one site" premise is wrong
- [X] T020 [US2] Verify the no-contributor path is untouched (SC-005, FR-009) with a real equality oracle, not just a green suite: assert in `internal/steps/phrase_test.go` that `EngineStepDocs` on an engine with no contributing comparators returns a value **deep-equal to `StepDocs()`** — the built-in-only reference — and run `go test ./internal/steps/... .`. This is the *whole* oracle for step-reference identity: no lane renders an engine step reference to stdout, because the only stdout renderer (`cmd/mentat/steps_cmd.go:66`) reads built-in `steps.StepDocs()` and never an engine
- [X] T020a [US2] Test in `internal/steps/phrase_test.go`: a comparator implementing the seam but returning `nil` or an empty slice is observably identical to one that does not implement it at all — nothing registered, nothing documented (spec Edge Cases). **Assert the empty path returns nil** (`ContributedPhrases() == nil` on a contributor-free engine), which is what actually detects the `make`+`copy` regression; the observable-identity assertion alone cannot see an allocation, and claiming otherwise would be a guard believed real but untested — the failure mode this repo has recorded twice
- [X] T020b [US2] Cover SC-005's third clause, which no other task reaches: assert in `internal/steps/phrase_test.go` that `EngineStepChecks` (and so `mentat.Validate`) produces findings **deep-equal** to the built-in-only result for an engine with no contributing comparators. T020 covers step-reference identity and `TestGoldenHermeticStdout` covers run-result identity; validation findings were uncovered

**Checkpoint**: all three surfaces provably share one set.

---

## Phase 5: User Story 3 - A returned phrase set cannot reach back into the engine (Priority: P3)

**Goal**: a caller cannot mutate the engine's view.

**Scope correction (research R8)**: this phase originally carried *two* copies. Only one
is real. The comparator-mutation half (US3 §2 / FR-002) is satisfied by T011's
element-wise transform and its test is red only *before* the freeze — so it moved to
Phase 3 as **T008a**. What remains here is the caller-mutation half, which is genuinely
red after Phase 3 because the snapshot itself is handed out by reference.

**T022 and T024 are intentionally absent.** T022 (comparator-mutation test) became T008a
in Phase 3; T024 (capture-time copy) was deleted as a no-op. The IDs are not reused, so
the gap records a decision rather than hiding one — and existing cross-references stay
valid.

**Independent Test**: resolve twice, mutate the first result, assert the second is unchanged.

### Tests for User Story 3 (REQUIRED — Test-First) ⚠️

> This genuinely fails after Phase 3: Phase 3 returns the snapshot without copying it.

- [X] T021 [US3] Failing test in `internal/engine/engine_test.go`: mutating the slice returned by `ContributedPhrases` leaves the next call's result unchanged (FR-004, US3 §1)
- [X] T023 [US3] Confirm T021 is RED, and confirm it fails because the caller shares the snapshot's backing array rather than for an unrelated reason

### Implementation for User Story 3

- [X] T025 [US3] Copy the snapshot on **every return** in `Engine.ContributedPhrases` in `internal/engine/engine.go` using `slices.Clone` (FR-004) — not `make`+`copy`, which would allocate on the empty path and break the edge case T020a pins
- [X] T026 [US3] Confirm T021 is GREEN, and confirm the copy is load-bearing by removing it and observing T021 go RED. **Do not** add a second copy at capture time — T008a already covers the comparator side and a capture copy would be a no-op (research R8)

**Checkpoint**: the snapshot is frozen and immutable from both directions. Feature complete.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T027 Re-run the full SC-004 oracle **unedited**: `go test -race -run 'TestContributedPhrasesAreScoped|TestAForeignPhraseIsUnbound' .` — all three isolation tests, including the foreign-phrase one that actually detects a leak (see T005). If any needed an edit to pass, the implementation broke engine isolation and the change is wrong (research R6)
- [X] T028 Verify no package-level mutable phrase state was introduced (014 FR-011), and **update the package-state audit note in `internal/steps/phrase.go:25-44` — the edit is certain, not conditional**. Its closing sentence ("Anything that varies by engine — the contributed phrase set, the compiled step-pattern set — is a value threaded through as a parameter") becomes false: the phrase set is now a field on `Engine`. Rewrite it to say the phrase set is captured per-engine at composition, which carries the isolation property the same way `resolveOnce` does. While editing, **qualify the FR number** the note cites — its "FR-010" is *012's* FR-010; 014's FR-010 is the no-behaviour-change rule and 014's FR-011 is the package-state rule
- [X] T029 Mutation rehearsal for US1 and US2: revert the freeze (restore per-call invocation), **assert the source edit actually applied**, confirm T006–T008a and T017–T018 go RED, restore, re-confirm green under `-race`; record the rehearsal in the test files as 011 and 012 did. T008a belongs in this set — reverting the freeze is exactly what makes it red
- [X] T030 Verify every existing malformed-phrase rejection (uncompilable, unanchored, duplicate, built-in collision, missing documentation field) still fires at composition with unchanged message text (SC-006, FR-008) via `go test ./internal/steps/... .` — the trailing `.` is required: `TestMalformedPhraseFailsBeforeAnyScenarioRuns` (`custom_phrase_isolation_test.go:250`) and `TestGenuinelyOverlappingPhrasesFailLoudly` (`:302`) live in the **root** package and are missed by `./internal/steps/...` alone
- [X] T031 [P] Update the `ContributedPhrases` and `PhraseContributor` doc comments in `internal/engine/engine.go` and `internal/core/core.go` so they describe the enforced behaviour; the three once-per-build claims (`mentat.go:66`, `internal/core/core.go:168`, `specs/012-comparator-gherkin-phrases/contracts/phrase-seam.md:24`) should need **no** correction (SC-008)
- [X] T032 Make the shallow-copy premise **enforced, not just documented** (research R7, which calls this the highest-value cheap guard in the feature): add a test **in `internal/engine/engine_test.go`** — that package already imports `core`, so one test can reach both types — that reflects over every field of `ContributedPhrase` (`internal/core/core.go:145`) and `PhraseBinding` (`internal/engine/engine.go:219`) and fails if any field is not a value type, so adding a slice/map/pointer field fails the suite rather than silently voiding FR-002/FR-004. Add the explanatory note at both definitions too. Shares files with T031, so not marked [P]
- [X] T033 [P] Record the feature in `CHANGELOG.md` as a defect fix, noting that no public API changed
- [X] T034 Run `make cover` (or `/coverage`) and confirm **no package** drops below 80% (SC-007) — `make cover` reports `./...`, and the root package is touched too (T004, T008, T017 add to `custom_phrase_facade_test.go`); `internal/engine` and `internal/steps` are the ones to watch
- [X] T035 Run `go test -tags e2e ./e2e/...` (needs `make harness-up`) **unconditionally** — `make ci` does not compile this lane, and "did step-registration output change?" is a judgement no gate can make for you. Note this is the *second* stdout oracle, not the only one: `TestGoldenHermeticStdout` (`mentat_golden_test.go:70`) is untagged and already runs under `make ci`; `e2e/golden_test.go` pins the same transform against the live harness
- [ ] T035a Mirror both Complexity Tracking rows from `plan.md` into the PR description — the US2 red-ordering deviation and the T015a test inversion — before requesting the `go-reviewer` `gate` audit. The constitution's Governance clause requires every deliberate deviation be justified in the PR description; the plan states this is done but no task did it
- [X] T036 Run `make ci` and confirm the full gate is green before requesting review. This is also the explicit check for **FR-010** (no behaviour change for comparators that already declare phrases from immutable state): every existing comparator and its tests are exactly that case, so a green suite — with the single D1 inversion accounted for in T015a — is the evidence FR-010 holds

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)** → no dependencies
- **Phase 2 (Foundational)** → after Phase 1. **Blocks all stories** (doubles are shared)
- **Phase 3 (US1)** → after Phase 2. Delivers the mechanism
- **Phase 4 (US2)** → after Phase 3. Verification only; no production change expected
- **Phase 5 (US3)** → after Phase 3. Adds the return-side copy
- **Phase 6 (Polish)** → after Phases 3–5

### User Story Dependencies

Unlike a typical feature, **US2 and US3 both depend on US1's mechanism** and cannot be
delivered before it. US1 is genuinely standalone; US2 and US3 are increments on it.
US4/US5 do not exist. This is recorded honestly rather than forced into the template's
independence model.

**US2 and US3 are independent of each other** and can proceed in parallel once US1 lands,
with **one cross-phase constraint**: T020a (Phase 4) pins the no-allocation property on
the empty path, which only T025 (Phase 5) can break. T025 must use `slices.Clone`, not
`make`+`copy` — `slices.Clone(nil)` returns nil while `make` allocates on every call. If
the phases are split across people, T020a's constraint travels with T025.

### Within Each Story

Tests → confirm RED → implementation → confirm GREEN. No exceptions (Principle V).

### Parallel Opportunities

- T003 and T004 (different files)
- T008 runs parallel to T006/T007/T008a — but those three all live in `internal/engine/engine_test.go` and must be written as one edit, so only T008 carries [P]
- T017 and T018 (different files)
- T033 runs parallel to T031/T032 — but T031 and T032 both touch `internal/core/core.go` and `internal/engine/engine.go`, so only T033 carries [P]
- Phases 4 and 5 after Phase 3, subject to the T020a → T025 constraint below
- Phases 4 and 5 in parallel after Phase 3

---

## Parallel Example: User Story 1

```text
# One edit to the engine test file (three assertions, written together):
T006  internal/engine/engine_test.go      — invoked exactly once
T007  internal/engine/engine_test.go      — stable across calls
T008a internal/engine/engine_test.go      — comparator cannot mutate the answer

# Genuinely parallel — a different file:
T008  custom_phrase_facade_test.go        — Validate/Run agree

# Then serialize the implementation — T010→T011→T012→T013→T014→T015→T015a.
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

Phases 1 → 2 → 3. That closes the defect's primary harm: `Validate` stops describing a
run that will not happen. Shippable on its own, with the copy hardening still outstanding.

### Incremental Delivery

1. **Phase 3** — the freeze. Cross-surface drift ends.
2. **Phase 5** — the copies. Mutation-proof in both directions.
3. **Phase 4** — verification that the shared-path premise held.
4. **Phase 6** — rehearsals, docs, gates.

Phase 4 is cheap and can ride alongside Phase 5.

### Routing (per `CLAUDE.md`)

| Tasks | Agent |
| --- | --- |
| T003–T004 (test doubles), T031–T033 (docs/CHANGELOG) | **go-coder** |
| T006–T030 (all behaviour change, TDD loop) | **go-test-writer** |
| Pre-commit audit before the PR | **go-reviewer** (`gate`) |

---

## Notes

- [P] tasks = different files, no dependencies
- Verify tests fail before implementing — and verify the *reason* they fail
- Commit after each task or logical group; stage files individually (`git add .` is forbidden)
- Conventional Commits; no AI attribution in commits or PRs
- **Exactly one test may be edited to change an assertion: T015a**, and only because spec **D1** records that it encodes a contract this feature is chartered to change. Everything else follows the standing rule — a red test means the implementation is wrong, not the test
- **Do not edit `custom_phrase_isolation_test.go` to make the implementation pass.** It is the regression oracle for the one way this feature can go badly wrong. T015a's licence does not extend to it, or to anything else
- If T019 fails, stop and revisit research R2 rather than patching around it

---

## Phase 7: Convergence

Appended by `/speckit-converge` on 2026-09-11. The specified scope is implemented and the
gate is green; these are documentation obligations the artifacts imply but no task covered.
No code-level gap, contradiction, or unrequested addition was found.

- [X] T037 Add a 014 feature retrospective to `CLAUDE.md` above the `<!-- SPECKIT -->` markers, matching the shape 011 and 012 use (what landed, and the corrections this feature made to its own artifacts — D1's test inversion, D2/R11's latent-not-live measurement, and R8's two-copies error) per repo convention (`CLAUDE.md:118-124`) (was: missing — closed by this feature)
- [X] T038 Document in `docs/extending/phrases.md` that `ContributedPhrases()` is called exactly once per engine build and that the returned slice is treated as immutable — mutating it afterwards has no effect on the built engine — so authors know to declare phrases from immutable state; the guide currently states neither, and `internal/core/core.go` is not author-facing per spec Edge Cases and FR-002 (was: partial — closed by this feature)
