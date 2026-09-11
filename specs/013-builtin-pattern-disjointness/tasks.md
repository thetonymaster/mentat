---

description: "Task list for 013-builtin-pattern-disjointness"
---

# Tasks: Built-in Step-Pattern Disjointness

**Input**: Design documents from `/specs/013-builtin-pattern-disjointness/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: This project's constitution mandates Test-First / TDD as NON-NEGOTIABLE (Principle V).
Every test task below MUST be written and **observed failing** before its implementation. A test
that was never seen red is not evidence — 011's `tasks.md` shipped two such tasks and both had to
be reordered.

**Organization**: Tasks are grouped by user story. US1 and US2 are both P1 and mutually
independent; US3 verifies US2; US4 documents the outcome of US1+US2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1 / US2 / US3 / US4
- Exact file paths are in every task

## Path Conventions

Single Go module at repository root. Feature code lives in `internal/steps/`; one call site in
`run.go`. No new package (research.md R5), no new module dependency (FR-008).

---

## Phase 1: Setup

**Purpose**: Capture the "before" state that two success criteria compare against.

- [ ] T001 Record the pre-change baseline into `specs/013-builtin-pattern-disjointness/baseline.txt`: full `go test ./... 2>&1` output, `go tool cover -func=cover.out` per-package figures, and `go.mod`'s require block — SC-005 and SC-006 are diffs against this, and it cannot be reconstructed after the first edit
- [ ] T002 [P] Confirm the starting point is clean in the worktree: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run ./...` clean, `make ci` green — record the result in `baseline.txt`
- [ ] T003 [P] Confirm the e2e lane is green BEFORE any change: `make harness-up && go test -tags e2e ./... && make harness-down`, appending to `baseline.txt` — research.md R8: `make ci` never compiles this lane, so a post-change failure is otherwise unattributable

**Checkpoint**: The "before" side of SC-005, SC-006 and SC-011 exists on disk.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Make the package compile while both P1 stories are red, and give them a shared
fixture. US1 and US2 share no production code — but they share a **Go package**, and that is what
makes this phase blocking rather than thin.

- [ ] T004 In `internal/steps/disjoint.go`, land a COMPILING stub: the `Verdict` type and `func Intersects(a, b string) (Verdict, error)` returning a `fmt.Errorf("intersection decider: not implemented")`. Then add the shared fixture — a deliberately overlapping pattern pair (`^the result contains "([^"]*)"$` vs `^the result contains "revenue"$`, the exact-vs-general case from spec.md Edge Cases) plus a known-disjoint pair — in a NEUTRAL file, `internal/steps/patternfixtures_test.go`, not in either story's test file

> **Why the stub is mandatory and not a shortcut.** `internal/steps` is ONE Go package. A test
> referencing an undeclared `Intersects` is a **package-wide compile error**, so
> `go test ./internal/steps/` would not run at all while US2's tests are written and unimplemented
> — which means US1's own red/green could not be observed either. The stub converts every US2 red
> from "the package does not build" into a genuine assertion failure, which is the only kind of red
> that is evidence. This is the recorded lesson *parallelize by package, not story; TDD RED poisons
> sibling signal*, and the first draft of this task list violated it.

**Checkpoint**: `go test ./internal/steps/` compiles and runs. US1 and US2 can now proceed in
parallel without either blinding the other.

---

## Phase 3: User Story 1 - No code path silently assumes a step matches one definition (Priority: P1) 🎯 MVP

**Goal**: `stepProblem` decides from the total match count across both pattern sources, so no path
resolves among multiple matching patterns by position (FR-001, FR-002).

**Independent Test**: Register two colliding rows — once as two built-ins, once as two contributed
phrases — drive a sentence both match, and assert the argument check defers instead of diagnosing
against the first. Delivers the Constitution IV guarantee with no other story implemented.

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

> Write these FIRST and observe each one FAIL. T007 is the exception and is labelled as such.

- [ ] T005 [US1] Test in `internal/steps/stepargs_test.go`: a sentence matched by TWO built-in patterns makes the step-argument check defer (zero argument diagnoses) rather than diagnose against the first — MUST FAIL against current `matchBuiltin`
- [ ] T006 [US1] Test in `internal/steps/stepargs_test.go`: a sentence matched by TWO contributed phrases, with no built-in matching, defers for the identical reason — MUST FAIL against current `matchPhrase`
- [ ] T007 [US1] Characterization test in `internal/steps/stepargs_test.go`: a sentence matched by one built-in AND one contributed phrase still defers — this one PASSES before the change and must keep passing; it pins the behaviour the new count branch generalises, so label it a regression guard, not a red test
- [ ] T008 [US1] Test in `internal/steps/stepargs_test.go`: exactly one matching pattern (each source, separately) produces a diagnosis byte-identical to the pre-change message — MUST PASS before and after (FR-003, SC-005)
- [ ] T009 [P] [US1] Test in `internal/steps/precheck_test.go`: each of the two colliding suites from T005/T006 yields exactly ONE `ambiguous-step` finding naming every matching pattern (SC-001)

### Implementation for User Story 1

- [ ] T010 [US1] Change `matchBuiltin` in `internal/steps/stepargs.go` to report every matching built-in (count plus one representative), not the first
- [ ] T011 [US1] Change `matchPhrase` in `internal/steps/stepargs.go` to report every matching contributed phrase, not the first
- [ ] T012 [US1] Rewrite `stepProblem` in `internal/steps/stepargs.go` to branch on the TOTAL match count across both sources — 0 → no finding, 1 → diagnose, >1 → defer — with no per-source case analysis (FR-002; a per-source switch is how a third source added later reintroduces the positional path by omission)
- [ ] T013 [US1] Replace the deferral comment in `internal/steps/stepargs.go` with the single count-based reason (under `Strict` neither definition binds), deleting the two-source framing that no longer describes the code (FR-002)

### Verification for User Story 1

- [ ] T014 [US1] Mutation rehearsal for SC-002: remove the `>1` deferral, CONFIRM THE EDIT IS PRESENT in `internal/steps/stepargs.go`, then run `go test ./internal/steps/` and confirm RED for BOTH the built-in and the contributed combination; record the rehearsal and the mutation-landed confirmation in `stepargs_test.go` — "the mutation didn't fire" and "the guard is real" are indistinguishable from test output alone
- [ ] T015 [US1] Confirm `internal/steps` is at or above the 80% coverage floor (FR-009) via `go test ./internal/steps/ -coverprofile=cover.out && go tool cover -func=cover.out`

**Checkpoint**: US1 is complete and shippable alone. The Constitution IV exposure is closed.

---

## Phase 4: User Story 2 - Disjointness is decided, not sampled (Priority: P1)

**Goal**: A stdlib product automaton decides emptiness-of-intersection; the built-in set is gated
hard and contributed overlap is reported (FR-010 – FR-014, FR-016, FR-017).

**Independent Test**: Run the gate over the current built-in set and confirm it decides all 780
pairs disjoint; add a deliberately overlapping built-in row and confirm the gate reddens with a
verified witness — with no feature file present.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [ ] T016 [US2] Test in `internal/steps/disjoint_test.go`: table-driven known-verdict corpus for `Intersects` using T004's fixtures plus the digits/digits-and-dots and optional-plural cases from research.md R1 — MUST FAIL (no implementation yet)
- [ ] T017 [US2] Test in `internal/steps/disjoint_test.go`: every positive verdict carries a witness, and the witness is re-verified with `regexp.MatchString` against BOTH patterns before the test trusts it (FR-011) — MUST FAIL
- [ ] T018 [US2] Test in `internal/steps/disjoint_test.go`: `\b`, `\B`, `(?m)^…`, `(?m)…$`, and an unrecognised `EmptyOp` each return a descriptive error naming the pattern and the construct, and NONE returns a "disjoint" verdict (FR-012, SC-010) — MUST FAIL
- [ ] T019 [US2] Test in `internal/steps/disjoint_test.go`: `^(?i)abc$` vs `^abc$` are reported INTERSECTING with witness `"abc"`, and `^(?i)[a-c]$` vs `^a$` likewise — the regression guard for research.md R3, the defect the prototype shipped with because `syntax` leaves `FoldCase` in `Inst.Arg` for single-rune instructions instead of materializing it — MUST FAIL
- [ ] T020 [US2] Test in `internal/steps/metadata_test.go`: `TestBuiltinStepPatternsAreDecidedDisjoint` decides every pair over `StepDocs()` (780 at 40 patterns), asserts zero intersecting, and REPORTS the pair count and wall clock rather than asserting a ceiling (FR-013, FR-016, SC-007; research.md recommends reporting so a slow runner cannot false-RED) — MUST FAIL
- [ ] T021 [US2] Test in `internal/steps/metadata_test.go`: injecting a deliberately overlapping row into the decided set reddens the gate, naming BOTH patterns and a verified witness, **with no feature file loaded** (SC-003) — MUST FAIL
- [ ] T022 [P] [US2] Test in `internal/steps/phrase_test.go`: two overlapping contributed phrases leave composition SUCCEEDING and produce exactly one `pattern-overlap` finding carrying a verified witness and naming the contributing comparator (FR-014, SC-009) — MUST FAIL
- [ ] T023 [P] [US2] Test in `internal/steps/phrase_test.go`: with those same two overlapping phrases registered, a suite whose steps fall OUTSIDE the overlap produces run output byte-identical to the same suite without them — the pattern-level finding never reaches scenario init (FR-017, SC-011) — MUST FAIL

### Implementation for User Story 2

- [ ] T024 [US2] Flesh out `internal/steps/disjoint.go` from T004's stub: `Verdict` gains `Intersects bool` / `Witness string` per contracts/decider.md, and `Intersects` stops returning the not-implemented error
- [ ] T025 [US2] Implement pattern compilation in `internal/steps/disjoint.go`: `syntax.Parse` → `Simplify` → `Compile`, wrapping each failure with `%w` and naming the offending pattern
- [ ] T026 [US2] Implement the epsilon closure in `internal/steps/disjoint.go` over `InstAlt`/`InstAltMatch`/`InstCapture`/`InstNop`, gating `InstEmptyWidth` on `atStart`/`atEnd`, and REFUSING the four unmodelled assertions plus any unrecognised `EmptyOp` by default (data-model.md §3, §4)
- [ ] T027 [US2] Implement `foldRanges` in `internal/steps/disjoint.go` honouring `FoldCase` via `unicode.SimpleFold` for single-rune instructions, mirroring `Inst.MatchRunePos` — cite R3 in the doc comment so the next reader knows why `Inst.Rune` alone is not the alphabet
- [ ] T028 [US2] Implement the rune-class alphabet partition in `internal/steps/disjoint.go` from the union of BOTH programs' rune-range boundaries
- [ ] T029 [US2] Implement the product BFS in `internal/steps/disjoint.go`: dedup on `(coreA, coreB, atStart)`, closure computed twice per state (`atEnd=false` to step, `atEnd=true` to accept), accumulating the witness string
- [ ] T030 [US2] Implement witness re-verification in `internal/steps/disjoint.go` — compile both patterns and `MatchString` the witness before returning a positive verdict (FR-011)
- [ ] T031 [US2] Add `LabelledPattern` and its `PatternSource` to `internal/steps/phrase.go`, derive the labelled set alongside `stepPatternsFor` (`phrase.go:565`) and return it through `EngineStepChecks` (`phrase.go:612`), so provenance and the comparator name survive to the finding (data-model.md §1, FR-014). `stepPatternsFor` has exactly ONE caller — that is what keeps this off the run path, so do not hang the derivation off `resolvePhrases`, which scenario init also calls (`steps.go:101`)
- [ ] T032 [US2] Add the `pattern-overlap` finding class and `PatternOverlapFindings` in `internal/steps/disjoint.go`, deciding only pairs touching at least one CONTRIBUTED pattern, emitting `File: ""`/`Line: 0`, one finding per pair, in deterministic registration order (R6, R8, data-model.md §5)
- [ ] T033 [US2] Wire `PatternOverlapFindings` into `mentat.Validate` in `run.go` by WRAPPING the return — `steps.DedupeSortFindings(append(overlap, check.Paths(ro.featurePaths)...))` — because `Validate` currently returns `SuiteCheck{…}.Paths(…)` directly with no pre-walk list and no `DedupeSortFindings` call of its own (that idiom is `cmd/mentat/validate.go:161`, the binary, which never emits this class). Appending without re-sorting would leave the `File:""`/`Line:0` findings outside the ordered list. NOT inside `SuiteCheck.Paths` (runs per feature file) and NOT in scenario init (R6, FR-017)
- [ ] T034 [US2] Update `StepBindingFindings`' doc comment in `internal/steps/precheck.go` to add `pattern-overlap` to the class list and state how it differs from `ambiguous-step` (per pattern pair vs per sentence)
- [ ] T035 [US2] Reframe `TestBuiltinStepPatternsArePairwiseDisjoint` in `internal/steps/metadata_test.go` as an INDEPENDENT CROSS-CHECK of the decider, keeping the test, its nine fillers and its `generated < 500` sanity floor intact (FR-005, D3) — this task changes comments and the test's stated purpose, never an assertion. **Rename it for its role** (e.g. `TestBuiltinPatternsCrossCheckedBySentenceCorpus`) so it does not sit one word away from T020's `TestBuiltinStepPatternsAreDecidedDisjoint` in the same file, where a reader grepping for the gate would land on the cross-check

### Verification for User Story 2

- [ ] T036 [US2] Confirm `internal/steps` and the root package stay at or above the 80% coverage floor (FR-009)
- [ ] T037 [US2] Confirm `go.mod`'s require block is byte-identical to T001's baseline (FR-008, SC-006)

**Checkpoint**: Built-in disjointness is decided. Contributed overlap is reported without breaking
composition or the run path.

---

## Phase 5: User Story 3 - The decider is falsifiable (Priority: P2)

**Goal**: The decider's negative verdicts are backed by differential evidence and mutation
rehearsals, not by trust (FR-007, FR-015).

**Independent Test**: Mutate the decider four ways and confirm at least one test reddens for each,
with every mutation confirmed to have landed first.

**Why this cannot be deferred past US2**: research.md R3 is a real defect that US3's method found
in US2's prototype. Shipping US2 unverified would reproduce this feature's own failure mode — a
confident claim resting on an unexamined mechanism — inside the mechanism built to remove it.

### Tests for User Story 3 (REQUIRED — Test-First) ⚠️

- [ ] T038 [US3] Differential test in `internal/steps/disjoint_test.go`: for every pair the decider called DISJOINT, sample strings against both patterns and fail if any string matches both — the failure message MUST name this a decider defect, never a disjointness defect (FR-015b, D4). **Include a known-overlapping pair as a positive control**, asserted to be *caught* by the sampler: over the real built-in set every pair is disjoint, so without a control this test's sampler is never once proven able to fire. Name the sampler's input set and its size explicitly — it must be deterministic and must not depend on fuzzing having run (research.md R7)
- [ ] T039 [US3] Agreement test in `internal/steps/disjoint_test.go`: every sentence in the existing generated corpus that matches two patterns MUST be reported intersecting by the decider; a disagreement fails and names both mechanisms (FR-015c, D3). **Seed a known two-pattern sentence as a positive control.** Verified 2026-09-11: `TestBuiltinStepPatternsArePairwiseDisjoint` PASSES, which means no generated sentence matches two patterns — so the natural input set is **EMPTY** and this test would be green while asserting nothing. That is 012's R11 lesson (*a gate's coverage is a property to measure, not to infer from its name*) applied to this feature's own tests
- [ ] T040 [P] [US3] Add `FuzzDecider` to `internal/steps/disjoint_test.go` with a SEED CORPUS covering the known-verdict pairs, so plain `go test` (and therefore `make ci`) exercises the seeds while `-fuzz` extends it — the differential check must not depend on fuzzing having run (research.md R7)

### Verification for User Story 3 — four mutation rehearsals

> Each: apply the mutation, **confirm the edit is present**, run, record. An unconfirmed mutation
> that produces green is indistinguishable from a guard that works.

- [ ] T041 [US3] Rehearsal 1 in `internal/steps/disjoint_test.go`: drop a rune class from the alphabet partition → expect RED via a missed transition producing a false "disjoint"; record the confirmation
- [ ] T042 [US3] Rehearsal 2 in `internal/steps/disjoint_test.go`: treat an unmodelled `EmptyOp` as satisfiable instead of refusing → expect RED on T018's refusal cases; record the confirmation
- [ ] T043 [US3] Rehearsal 3 in `internal/steps/disjoint_test.go`: ignore `FoldCase` in the single-rune path → expect RED on T019 — this rehearsal reproduces the R3 defect exactly, so it is the one with a known-good expected output to compare against; record the confirmation
- [ ] T044 [US3] Rehearsal 4 in `internal/steps/disjoint_test.go`: collapse the two closures into one with `atEnd` fixed → expect RED because `$`-anchored patterns become unsatisfiable; record the confirmation
- [ ] T045 [US3] Record all four rehearsals AND T038/T039's two positive controls beside the guards they exercise in `internal/steps/disjoint_test.go`, each with its confirmation that the mutation landed (FR-007, SC-008). FR-007 says *every* new guard is rehearsed against a deliberate collision; the four rehearsals target decider internals only, so without T038/T039's controls two new guards would ship never having been shown capable of failing

**Checkpoint**: A green decider gate now means something checkable.

---

## Phase 6: User Story 4 - Every record states what is decided and what is not (Priority: P3)

**Goal**: No reader mistakes a sample for a decision (FR-004, SC-004).

**Independent Test**: Read each site and confirm it names its own basis, without running anything.

**Why last**: it describes US1's and US2's outcome. Written earlier it would need a third round of
wording corrections to a claim whose status was still moving.

- [ ] T046 [P] [US4] Update `CHANGELOG.md`: built-in disjointness is DECIDED by a gate; add the `pattern-overlap` finding class; remove the "as measured" hedge as the basis for the claim
- [ ] T047 [P] [US4] Update the 013 roadmap entry and feature-history section in `CLAUDE.md` (above the `<!-- SPECKIT -->` markers — the hook destroys anything below them), recording what landed, R3 as the finding worth keeping, the corrections this feature made to its own artifacts, and **the gate's shipped pair count and wall clock** — FR-016 says the cost is "measured and **recorded**", and test output is not a record. Also fix the stale `internal/steps/stepargs.go:305` citation in that section; `matchBuiltin` is at `:310`
- [ ] T048 [P] [US4] Add a superseded pointer to `specs/012-comparator-gherkin-phrases/contracts/validate-surface.md` §4 directing readers to 013's contract, rather than silently rewriting a merged feature's contract
- [ ] T049 [P] [US4] Update `docs/extending/phrases.md`: what overlap between contributed phrases now reports, that composition still succeeds, and that a sentence inside the overlap still fails at run time
- [ ] T050 [US4] Sweep for stale claims with `grep -rn "as measured\|nine fillers\|evidence, not proof" CHANGELOG.md CLAUDE.md docs/ internal/steps/ specs/012-*/` — note the path is `specs/012-*/`, NOT `specs/012-*/contracts/`: verified live hits exist at `specs/012-comparator-gherkin-phrases/spec.md:640,647` that the narrower list would have missed. Confirm no remaining hit presents sampling as the BASIS for built-in disjointness (SC-004). A hit inside a **merged feature's own spec** stays as history — but say so deliberately in the sweep's recorded result rather than leaving it excluded by a path that happened not to reach it

**Checkpoint**: All four stories complete.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T051 [P] Run `gofmt -l .` (expect empty), `go vet ./...` and `golangci-lint run ./...`
- [ ] T052 Confirm the 80% coverage floor for every touched package via `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`, comparing per-package figures against the baseline in `specs/013-builtin-pattern-disjointness/baseline.txt` (FR-009)
- [ ] T053 Run `make ci` and confirm green
- [ ] T054 Run the e2e lane — `make harness-up && go test -tags e2e ./... && make harness-down` — and account for any golden churn rather than blanket-refreshing it. **Required, not optional**: `make ci` is `lint test cover example` and never compiles this lane, so a green `make ci` is not evidence the stdout goldens are current (research.md R8; 012 was bitten by exactly this)
- [ ] T055 Confirm `TestFacadeNameabilitySweep` and the public-surface golden are UNCHANGED — the decider is `internal/`-only, so movement here means something leaked onto the facade and is a signal to stop, not a golden to update (plan.md, 010 D5)
- [ ] T056 Confirm SC-005 by diffing the **findings and verdicts** — not raw runner output — against `specs/013-builtin-pattern-disjointness/baseline.txt`: normalize away per-package elapsed times (`\t[0-9.]+s`) and `(cached)` markers before comparing, or compare the `mentat.Validate` finding lists and scenario verdicts directly. A literal diff of `go test ./... 2>&1` ALWAYS differs on timings and cache state, so the task as first written could never have passed (FR-003, SC-005)
- [ ] T057 Walk `specs/013-builtin-pattern-disjointness/quickstart.md` end to end and confirm each documented command behaves as written
- [ ] T058 Request a `go-reviewer` `gate` audit of the staged diff and resolve every finding before commit (constitution: Development Workflow & Quality Gates)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies. T001–T003 must precede any edit — the baseline cannot be reconstructed afterwards.
- **Foundational (Phase 2)**: depends on Setup. Blocks US1 and US2 only for the shared fixture.
- **US1 (Phase 3)** and **US2 (Phase 4)**: both P1, mutually independent, can run in parallel.
- **US3 (Phase 5)**: depends on US2 — it verifies it. MUST land in the same branch as US2.
- **US4 (Phase 6)**: depends on US1 and US2 having landed.
- **Polish (Phase 7)**: depends on all desired stories.

### User Story Dependencies

- **US1 (P1)**: independent. Would still be required if the pattern set were decided disjoint forever — Constitution IV constrains the code path, not the property.
- **US2 (P1)**: independent of US1's production code; shares only T004's fixture.
- **US3 (P2)**: hard dependency on US2. Not separable — see Phase 5's rationale.
- **US4 (P3)**: documents US1+US2.

### Within Each User Story

- Tests written and **observed failing** before implementation, one at a time.
- T007 and T008 are the exceptions: characterization guards that must pass before AND after.
- Mutation rehearsals come last within a story, and each requires confirming the mutation landed.

### Parallel Opportunities

- T002 and T003 (Setup) are independent of each other.
- **US1 and US2 can be developed concurrently** — different files apart from T004's shared fixture.
- T022 and T023 (`phrase_test.go`) are parallel with the `disjoint_test.go` tests.
- T040 (`FuzzDecider`) is parallel with T038/T039 only if written as a separate block in the file.
- T046–T049 (US4 docs) are four different files, fully parallel.
- **Not parallel**: T005–T008 all edit `stepargs_test.go`; T010–T013 all edit `stepargs.go`; T024–T032 all edit `disjoint.go`. Same file, sequential.

---

## Parallel Example: the two P1 stories

```bash
# ONLY valid after T004's compiling stub. Before it, US2's tests are a package-wide
# compile error and Track A cannot observe its own red.
Track A (US1): T005 → T006 → T007 → T008 → T010 → T011 → T012 → T013 → T014
               files: internal/steps/stepargs.go, stepargs_test.go

Track B (US2): T016 → T017 → T018 → T019 → T024 … T030
               files: internal/steps/disjoint.go, disjoint_test.go

# T009 [P] and T022/T023 [P] slot into either track — different files again.
# Both tracks share one package, so a broken build in either blinds the other.
# If running them concurrently, keep the build green at every handoff.
```

```bash
# US4's documentation tasks, four files, all at once:
Task: "Update CHANGELOG.md"
Task: "Update CLAUDE.md feature history and roadmap"
Task: "Add superseded pointer to specs/012-*/contracts/validate-surface.md"
Task: "Update docs/extending/phrases.md"
```

---

## Implementation Strategy

### MVP scope

**US1 alone is a shippable increment** — it closes the Constitution IV exposure and needs nothing
else. But it is **not the intended stop**: spec.md's Assumptions explicitly withdraw the earlier
framing that US1 alone was the goal, because deferring the half that actually closes the gap is
the pattern that produced 013 out of 012.

Treat **US1 + US2 + US3** as the deliverable. US3 is not a nice-to-have attached to US2; it is
what makes US2's green mean anything, and R3 is the worked proof of that.

### Incremental delivery

1. Setup + Foundational → the baseline exists.
2. US1 → validate independently → the positional-resolution defect is closed.
3. US2 → validate independently → disjointness is decided.
4. US3 → the decider is falsifiable. **Do not stop between 3 and 4.**
5. US4 → the records match reality.
6. Polish, including the e2e lane.

### Routing

Per the constitution's routing table: every phase here is a behaviour change, so
**go-test-writer** owns Phases 3–5 (it owns the TDD loop). T035, T046–T049 and the Setup phase are
behaviour-preserving or documentation and suit **go-coder**. T058 is **go-reviewer** in `gate`
mode.

---

## Notes

- `[P]` = different files, no dependency on an incomplete task.
- Commit after each task or logical group; Conventional Commits; stage files individually (`git add .` is forbidden).
- **Every mutation rehearsal must confirm the mutation landed.** 011 recorded one that initially failed to go red because the mutation had not applied, and the two states are indistinguishable from test output.
- The spec, plan and research were corrected four times during specification and planning (D1's premise, D2's cost, FR-014's seam, and FR-012's treatment of case folding). If a task below contradicts the spec, check research.md before assuming the task is wrong — but check, do not assume either way.
