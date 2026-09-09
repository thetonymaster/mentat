---
description: "Task list for 010-seam-type-nameability"
---

# Tasks: Seam-Type Nameability

**Input**: Design documents from `/specs/010-seam-type-nameability/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: This project's constitution mandates Test-First / TDD as NON-NEGOTIABLE
(Principle V). Test tasks are REQUIRED and each MUST be written to FAIL before its
implementation. They are not optional.

**Revised for D5.** Phase 5 was rewritten after adversarial review found the original design
to be an import cycle. See [spec D5](./spec.md) and [research R8](./research.md).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US4, mapping to the spec's user stories
- Exact file paths are given in every task

## Path conventions

Single Go module. The facade package at the repository root (`mentat.go`, `run.go`) **aliases**;
`internal/` holds every declaration; `cmd/mentat` is consumer zero; `examples/kafkaecho` is a
separate module (via `replace`) serving as the external-module witness.

---

## ⚠️ Read before starting

**Phase order is execution order, not story-priority order.** The spec's P1–P4 rank importance:

| Phase | Story | Priority | Why here |
|---|---|---|---|
| 2 | — | — | The format golden must be green **before** anything moves (FR-013) |
| 3 | US2 | P2 | No open decisions; independently shippable |
| 4 | US3 | P3 | No open decisions; independently shippable |
| 5 | US1 | **P1** | Largest slice; needs Phase 2 in place first |
| 6 | US4 | P4 | Root-cause fix, but **cannot go green until US1 retires the `RunReport` name** |

**Two types cannot be facade-declared.** Root imports `internal/report`, `internal/engine` and
`internal/registry`, so anything those packages consume lives in an internal package and is
aliased upward. Only *terminal* types may be declared at the facade. Any task that puts a seam
or its parameter type in package `mentat` is wrong.

---

## Phase 1: Setup

- [X] T001 Run `make ci` and record it green as the pre-change baseline; note it in the PR description
- [X] T002 [P] Re-verify by **symbol** (not line number) that the call sites in [plan.md](./plan.md) Risks still hold at HEAD: `registry.RegisterReporter` callers, `registry.Reporter` callers, the `//go:generate` directive in `internal/core/core.go`, and the declaration sites of `RunReport`/`ScenarioResult`/`RunRecord`/`Reporter`. Line numbers throughout this file are pinned at `f2afdda` and drift as tasks land — treat them as hints, symbols as truth

**Checkpoint**: Baseline green, call sites confirmed by symbol.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Protect the emitted report format **before** anything moves.

**⚠️ CRITICAL**: Nothing in Phase 5 may start until this is green. A golden minted after the
move records the new bytes and proves nothing — FR-013's ordering clause.

**Contract**: [report-format-golden.md](./contracts/report-format-golden.md)

> D5 reduced what this guards: the bytes are now unchanged by construction, since the
> marshalled struct is the same struct relocated. It stays because a tag dropped or a field
> reordered *during transcription* is an ordinary mistake that every other check in this repo
> is blind to.

- [X] T003 [P] Add a deterministic report fixture builder in `internal/report/fixture_test.go` — a fixed report value exercising every tagged field both set and unset: a scenario with qualifiers, one with an aggregate detail, one with a derivation note, one with judge usage, one multi-run, and one plain
- [X] T004 [P] ~~Add a normalization helper replacing `StartedAt`/`Duration` with fixed placeholders~~ — **not needed, and deliberately not added.** The task's own preference ("fix the fixture timestamp outright and normalize only what cannot be fixed") turned out to cover everything: `StartedAt` and `Duration` are literals in the fixture, and none of the report types contains a map, so there is no iteration-order nondeterminism either. A normalizer would have been dead code. Replaced by `TestReportFormatGoldenIsDeterministic` in `internal/report/format_golden_test.go`, which renders each fixture twice and compares — making determinism a checked fact rather than an assumption
- [X] T005 Add `TestReportFormatGolden` in `internal/report/format_golden_test.go` covering json, html and junit against `internal/report/testdata/report-{json,html,junit}.golden`; run with no golden files present and **confirm RED** (depends on T003, T004)
- [X] T006 Mint the three goldens with `MENTAT_UPDATE_GOLDEN=1 go test ./internal/report/ -run TestReportFormatGolden`, inspect each by eye against today's output, commit them; confirm GREEN
- [X] T007 Falsification rehearsal recorded in `internal/report/format_golden_test.go`: (a) rename one json tag on the scenario type → confirm RED naming the format; (b) swap two field positions → confirm RED on key order; revert both, confirm GREEN
- [X] T008 Verify `internal/report` holds ≥80% coverage via the `/coverage` skill

**Checkpoint**: The format is guarded and the guard is proven to fail.

---

## Phase 3: User Story 2 — Build a complete RunSpec from a custom Driver (Priority: P2)

**Goal**: A driver author can construct a `RunSpec` with `Extract` and `HTTP` populated,
importing only the facade.

**Independent Test**: An external-module compile test builds a `RunSpec` with both fields set
to non-zero values using facade names only. Compiling is the proof.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [X] T009 [P] [US2] In `mentat_external_test.go`, add a composite literal constructing `mentat.RunSpec` with `Extract` set (mode, marker and pattern all populated) and `HTTP` set (URL, method, headers) — **confirm the package fails to compile** (the RED for a nameability defect)
- [X] T010 [P] [US2] ~~Add an `ExtractPolicy` error-path test in `mentat_external_test.go`~~ — **premise was false; no test added.** Two facts found while attempting it: (a) the capture-group error path is **already covered**, at `internal/core/core_test.go:146` ("pattern with zero capture groups is a hard error even on a match", asserting substring `no capture group`) and `internal/config/config_test.go:290`; (b) `core.ExtractAnswer` is **not on the facade**, so an external-module test cannot invoke it — the path is only reachable through a driver. Duplicating the assertion at the facade would test nothing new and could not be written where the task said. **Real gap surfaced instead**: the mode constants `ExtractWhole`/`ExtractMarker`/`ExtractPattern` are also absent from the facade, so an external driver author must write `Mode: "pattern"` as a magic string, making a typo a runtime error rather than a compile error. Documented on the `ExtractPolicy` alias; exporting the constants is a further surface widening and therefore a scope decision, not a task — **raised for decision, see T057**

### Implementation for User Story 2

- [X] T011 [US2] Add `type ExtractPolicy = core.ExtractPolicy` and `type HTTPSpec = core.HTTPSpec` to `mentat.go`, each with a doc comment justifying its place on the surface (SC-007); confirm T009 and T010 GREEN
- [X] T012 [US2] Regenerate the surface golden with `MENTAT_UPDATE_GOLDEN=1 go test -run TestPublicSurfaceGolden` and hand-review `specs/007-public-extension-api/contracts/public-surface.golden` — expect 2 alias lines plus 6 expanded `field (X)[nn]` lines, nothing else
- [X] T013 [US2] Confirm `examples/kafkaecho` still compiles **untouched** via `make example` (SC-006)
- [X] T014 [US2] Verify the root package holds ≥80% coverage

**Checkpoint**: A driver author can populate `RunSpec` fully. Two of four gaps closed.

---

## Phase 4: User Story 3 — Return a structured Detail from a custom Comparator (Priority: P3)

**Goal**: A comparator author can attach structured detail to a verdict, importing only the
facade.

**Independent Test**: An external-module compile test returns a `Verdict` with `Detail`
populated using facade names only.

### Tests for User Story 3 (REQUIRED — Test-First) ⚠️

- [X] T015 [P] [US3] In `mentat_external_test.go`, add a comparator returning a `mentat.Verdict` with `Detail` set — every exported member populated — and **confirm it fails to compile**
- [X] T016 [P] [US3] ~~Add a test asserting a verdict carrying `Detail` has it rendered rather than dropped~~ — **already covered in composition; no duplicate added.** US3 acceptance 2 is the chain `Verdict.Detail` → `ScenarioResult.Aggregate` → rendered output, and each link is pinned: `internal/report/derive_test.go:65,289-293` asserts the mapping (`derive.go:36`, `Aggregate: v.Detail`) including `Computed`; `internal/report/html_test.go:53,63` asserts the rendered-vs-nil branch; and T006's new `report-full.html.golden:37` now pins the rendered bytes exactly (`<p>rate = 0.50, want &gt;= 0.80</p>`). A third test asserting the same chain would be duplication, not coverage

### Implementation for User Story 3

- [X] T017 [US3] Add `type AggregateDetail = core.AggregateDetail` to `mentat.go` with its surface justification; confirm T015 and T016 GREEN
- [X] T018 [US3] Regenerate and hand-review the surface golden — expect 1 alias line plus 6 expanded field lines
- [X] T019 [US3] Confirm `make example` green and coverage floors hold

**Checkpoint**: Three of four gaps closed. Everything so far is additive and independently shippable — a valid stopping point, and the recommended MVP.

---

## Phase 5: User Story 1 — Implement and register a custom Reporter (Priority: P1)

**Goal**: An external module can declare a `Reporter`, register it, and have it invoked with
the run's results — with access to everything the built-in reporters see.

**Independent Test**: A facade-only module declares a type satisfying `Reporter`, registers it
with `WithReporter`, selects it through `WithReports`, runs a suite, and receives the run's
outcome with full fidelity.

**Contract**: [reporter-seam.md](./contracts/reporter-seam.md) ·
**Data model**: [data-model.md §2–§6](./data-model.md) · **Decision**: [spec D5](./spec.md)

> **Phase 2 must be green before starting.**

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

- [X] T020 [P] [US1] In `mentat_external_test.go`, declare a type whose method set matches `Reporter` using facade names only — **confirm it fails to compile** (acceptance 1)
- [ ] T021 [P] [US1] Add `TestCustomReporter` in `mentat_run_test.go`: register a facade-only reporter via `WithReporter`, select it via `WithReports`, run a suite, assert it received the run's outcome and produced output (acceptance 2)
- [ ] T022 [P] [US1] Add a collision test: registering a reporter under a name already taken fails loudly naming the conflict, never last-wins (acceptance 3)
- [ ] T023 [P] [US1] Add an error-path test: a registered reporter returning an error surfaces the failure **with the reporter named** while `Results` are still returned, and the `errors.Join` composition at `run.go:471-476` still surfaces a simultaneous budget trip (acceptance 4)
- [ ] T024 [P] [US1] Add a **characterization** test that `WithReports` naming an unregistered reporter is a loud error naming the unknown name. Note: `EmitReports` already errors on this (`internal/report/emit.go:38`) — this pins existing behaviour through the refactor, it is not a missing error path
- [ ] T025 [P] [US1] Assert the three built-in reporters compile against the **published** seam with their field reads unmodified — under D5 this is how SC-008 is verified, not by counting fields (acceptance 5)
- [ ] T026 [US1] Add `TestConcurrentReporterRegistration` in `mentat_run_test.go`: two genuinely concurrent `Run` calls register **different** reporters under the **same** name; each uses its own (acceptance 7, SC-010). Must run under `-race`

### Implementation — 5a: the move (PURE — no shape change)

> Nothing in 5a adds, removes, reorders or re-tags a field. `TestReportFormatGolden` must stay
> green throughout. A red golden here means the transcription is wrong — **fix the code, never
> the golden.**

- [X] T027 [US1] Create `internal/result/result.go` and move `RunReport` (renamed `Results`), `ScenarioResult`, `RunRecord` and `Reporter` out of `internal/core/core.go:334-399` **verbatim** — field sets, declaration order and struct tags byte-identical. The package imports `internal/core` for `AggregateDetail` and `JudgeUsage`; `core` must import nothing back
- [X] T028 [US1] Move `Results.ExitCode()` from `run.go:231` to `internal/result/result.go` with its doc comment and the 130/1/0 contract intact
- [X] T029 [US1] Re-shape the seam in `internal/result/result.go` to `Report(res Results, w io.Writer) error` (FR-012 — exactly one `Reporter` interface exists after this)
- [X] T030 [US1] Update `internal/report/{collector,derive,ledger,json,html,junit,emit}.go` and `internal/registry/registry.go` to the moved types — **parameter and field types renamed only**; no field read changes
- [X] T031 [US1] Run `go test ./internal/report/ -run TestReportFormatGolden` — **MUST be GREEN**. This is the task Phase 2 exists for
- [X] T032 [US1] Run `make ci` — the whole module builds and tests green after a pure move, before any widening

### Implementation — 5b: the facade

- [X] T033 [US1] In `mentat.go`, alias `Results`, `ScenarioResult`, `RunRecord` and `Reporter` to their `internal/result` declarations; delete the facade's own `Results`/`ScenarioResult` declarations and `toResults` (`run.go:483`, called at `:476` and `:478`) — the conversion no longer exists
- [X] T034 [US1] Add `RunIDs []string` with tag `json:"-"` to `result.ScenarioResult`, derived from `Runs` in order; add a test pinning the correspondence so the two can never disagree. This is the **only** field this feature adds to the moved types
- [X] T035 [US1] Rewrite the rationale comment at `mentat.go:59-62` so only the `Correlator` half of the "no concrete external demand" note survives (FR-015)
- [X] T036 [US1] Regenerate and hand-review the surface golden. Expect these types to change from inline declarations (`public-surface.golden:169`, `:173`) to **aliases expanded per field** — roughly twenty added lines, not two rewritten ones
- [X] T037 [US1] Run `make example` — SC-006 checked **here**, at the phase that reorders fields and breaks a signature, not two phases later

### Implementation — 5c: the mock

- [X] T038 [US1] Resolve `MockReporter`'s home explicitly. The only `//go:generate` is `internal/core/core.go:3` (`-source=core.go`); T027 removes `Reporter` from that file, so `go generate ./...` would **delete** the mock while `internal/registry/registry_test.go:265` still uses it. Either add a `//go:generate mockgen` directive for `internal/result`, or replace the mock with a value stub — then regenerate, commit, and update `registry_test.go:265` and `internal/report/lifecycle_test.go:181`

### Implementation — 5d: registration

- [ ] T039 [US1] Add `RegisterReporter`/`Reporter` methods to `*Registry` in `internal/registry/registry.go` beside the other seams, sealed by `Build`; **delete** the package-global block at `registry.go:184-193` **together with its rationale comment** — the comment justifies the global by a call path that has not existed since the 007 recompose
- [ ] T040 [US1] Change `EmitReports`/`emitAtomic` in `internal/report/emit.go` to take the resolved reporter set (or the `*Registry`) alongside `Results`; the caller at `run.go:457` holds both. State the signature in the code, do not leave it implicit
- [ ] T041 [US1] Add `ReporterFactory` and `WithReporter(name string, f ReporterFactory) Option` to `run.go`, mirroring `WithComparator` (`run.go:201`); wire reporter registration into `engine.Build` (`internal/engine/build.go:56`) at the single composition root
- [ ] T042 [US1] Confirm T020–T026 all GREEN, with T026 under `go test . -race`
- [ ] T043 [US1] Verify the root package, `internal/result`, `internal/registry`, `internal/report` and `internal/engine` each hold ≥80% coverage

**Checkpoint**: Six of six seams implementable; the `RunReport` name is retired.

---

## Phase 6: User Story 4 — The gate catches the next unnameable seam type (Priority: P4)

**Goal**: A maintainer who adds an unnameable seam parameter type learns it from their own PR.

**Independent Test**: Introduce a deliberately unnameable seam parameter on a scratch branch;
confirm the check fails and names it; remove it and confirm green.

**Contract**: [facade-nameability-v2.md](./contracts/facade-nameability-v2.md)

> **Cannot go green before T029.** While the seam names a type with no facade name, "zero
> unnameable types" is unreachable. Last by necessity, not by importance.

### Tests for User Story 4 (REQUIRED — Test-First) ⚠️

- [ ] T044 [US4] Extend the nameability sweep in `surface_test.go` to walk **seam method parameter and result types** in addition to data reachability from `Config`/`Results`, per [nameability-v2](./contracts/facade-nameability-v2.md); confirm zero unnameable types (acceptance 1)
- [ ] T045 [US4] Assert the failure message names **both** the offending type and the reaching position — `method (Reporter) Report(rep RunReport, …)`, `field (Verdict) Detail *AggregateDetail`. Naming only the type does not satisfy the contract (acceptance 2, SC-004)
- [ ] T046 [US4] Falsification rehearsal recorded in `surface_test.go`: add a seam method with an unnameable parameter → confirm RED naming type and position; name it on the facade → confirm GREEN; revert (acceptance 3)

### Implementation for User Story 4

- [ ] T047 [US4] Extend the compile-level witness in `mentat_external_test.go` to declare a type satisfying **each of the six** seams, so the sixth is proven by compilation (FR-008). The example module is **not** extended — it carries a compile-only obligation (SC-006)
- [ ] T048 [US4] Update `specs/009-extension-surface-integrity/contracts/facade-nameability.md` to point at the v2 contract as its successor, replacing the "deferred to spec 010" section with the resolution

**Checkpoint**: The defect class is closed, not just its four instances.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T049 **Delete** stability boundary 4 from `docs/extending/stability.md:122-137` — the gap no longer exists, so it is removed rather than reworded (FR-010, SC-005)
- [ ] T050 [P] Update `docs/extending/new-seam.md` for the reporter registration model — per-engine and sealed like the other five — and add the rule D5 discovered: a seam's parameter types cannot be declared at the facade, because root imports the packages that consume them
- [ ] T051 [P] Correct the stale "the CLI emits reports after `Run` returns" claim at `internal/engine/build.go:53-55` (FR-015). The identical claim in `registry.go` is deleted wholesale by T039 — do not edit it separately
- [ ] T052 Add the `CHANGELOG` entry: **breaking** — `Reporter.Report` now takes `Results`; `RunReport` is retired. **Additive** — three new aliases, `RunRecord`, `WithReporter`, and `Results`/`ScenarioResult` gain fields (FR-009)
- [ ] T053 Write the migration note: implementers of `Reporter` take `Results`; `RunReport` no longer exists; **unkeyed composite literals** of `Results`/`ScenarioResult` will fail to compile, and field order changed, so an unkeyed literal that still compiles would mean something different. Keyed literals are unaffected
- [ ] T054 Record the per-symbol justification for every newly public symbol per `specs/007-public-extension-api/contracts/public-surface.md` (SC-007). Under D5 these types are exposed *directly* rather than mirrored, so each needs its own justification written, not inherited
- [ ] T055 Run every check in [quickstart.md](./quickstart.md) — all six, including `go test . -race` for SC-010 and the three documentation `grep`s
- [ ] T056 Run `make ci` green, then `go-reviewer` in `gate` mode over the staged diff, with explicit attention to the hand-reviewed `public-surface.golden` diff

### Unplanned work done during implementation

- [X] T058 **Surface gate: render methods on aliased structs.** Found at T036. Aliasing `Results` moved its methods into `internal/result`, where the renderer's `FuncDecl` branch never looks — so `func (r Results) ExitCode() int`, the documented 130/1/0 exit-status contract, **silently left the golden with the gate still green**. The renderer expanded aliased *interfaces* to their method sets and aliased *structs* to their field sets only; the struct method-set case was missing, and D5 walked straight into it. Fixed by `renderStructMethods` in `surface_test.go` (the method-side complement of 009's `renderStructFields`), matching value and pointer receivers alike since both are reachable through an alias. Mutation rehearsal recorded in the function's doc comment: appending `func (r Results) XProbe() bool` now fails the gate naming `method (Results) XProbe() bool`, where before the same experiment produced no diff at all. This was a regression **introduced by this feature**, not a pre-existing accepted boundary, so it is fixed rather than documented

### Open decision raised during implementation

- [ ] T057 **DECISION NEEDED — export the extraction-mode constants?** Found at T010. `mentat.ExtractPolicy` is now nameable, but `ExtractWhole`/`ExtractMarker`/`ExtractPattern` (`internal/core/core.go`) are not, so every external driver author writes `Mode: "pattern"` as a magic string and a typo fails at run time instead of compile time. The same argument applies to `FailureKind*`, which **is** already exported — so the precedent favours exporting. Against: it is a further public-surface widening, a one-way door, and outside this feature's stated scope (the spec's FR-001/FR-002 are about *types* and are already satisfied — `Mode` is a settable `string`). Options: (a) export the three constants under 010 with SC-007 justifications, (b) defer to a later spec, (c) decide the string is fine and record that in `stability.md` so it is not rediscovered. **Not to be actioned without an explicit decision**

---

## Dependencies & Execution Order

### Phase dependencies

- **Phase 1**: no dependencies
- **Phase 2**: depends on Phase 1 — **BLOCKS Phase 5 absolutely**
- **Phases 3–4**: depend only on Phase 1; sequenced after Phase 2 so the guard is in place first
- **Phase 5**: **requires Phase 2 green**. Independent of Phases 3–4 except for the shared golden
- **Phase 6**: requires **T029**. Cannot be green before it
- **Phase 7**: requires all stories complete

### Story dependencies

- **US2 (P2)** and **US3 (P3)**: fully independent of each other and of US1
- **US1 (P1)**: independent of US2/US3 in logic; shares `public-surface.golden` with them
- **US4 (P4)**: **hard dependency on US1** — the only true cross-story dependency

### Within Phase 5

The sub-blocks are strictly ordered and must not be parallelized:

```
5a (pure move)  →  5b (facade + RunIDs)  →  5c (mock)  →  5d (registration)
     ↑
  golden green across this boundary is the whole point
```

Separating 5a from 5b is deliberate: it makes the move provably inert before any widening, so
a red golden has exactly one possible cause.

### Parallel opportunities

- T003 ∥ T004; T009 ∥ T010; T015 ∥ T016; T020–T025; T050 ∥ T051
- **Phases 3 and 4 could run in parallel** but both regenerate `public-surface.golden`.
  Serialize them — a conflict in a hand-reviewed artifact costs more than the overlap saves

---

## Parallel example: User Story 1 tests

```bash
# Distinct concerns, all must go RED first:
Task: "T020 external-module Reporter declaration in mentat_external_test.go"
Task: "T021 TestCustomReporter end-to-end in mentat_run_test.go"
Task: "T022 reporter name collision fails loudly"
Task: "T023 reporter render error surfaces named, Results still returned"
Task: "T024 characterize WithReports unknown-name error"
Task: "T025 built-ins compile against the published seam"
```

---

## Implementation Strategy

### MVP: Phases 1–4 — **not** User Story 1

The template's default is "US1 alone is the MVP". That is wrong here:

- **US2 + US3** close three of four gaps, are pure additions, need no decisions, and ship
  independently. With the Phase 2 guard they are a coherent release.
- **US1** is a breaking seam change, a package split, a registration-model move and a mock
  decision — the largest and riskiest slice, and the one D5 had to rescue.

Ship Phases 1–4, stop, validate.

### Incremental delivery

1. Phases 1–2 → the format is guarded and the guard is proven to fail
2. Phase 3 → drivers can build a complete `RunSpec` → shippable
3. Phase 4 → comparators can attach structured detail → shippable
4. Phase 5 → the sixth seam becomes implementable → shippable (breaking; three acts)
5. Phase 6 → the defect class closes → shippable
6. Phase 7 → docs and changelog tell the truth

### Routing (constitution: Development Workflow)

- **go-test-writer**: Phases 2–6 — all behaviour changes, TDD loop
- **go-coder**: T038 (mock), T049–T054 (docs, changelog)
- **go-reviewer** (`gate`): T056, plus a `pair` scan at each checkpoint

---

## Notes

- The RED for a nameability task is a **compile failure**, not a failing assertion. Do not
  rewrite such a test so it compiles in order to "fail properly"
- `TestReportFormatGolden` is green at T006 and must be green at T031. **Fix the code, never
  regenerate the golden** — regeneration at that point silently accepts a transcription error
- 5a is a pure move. If it changes any field's name, order or tag, it is wrong
- Line numbers here are pinned at `f2afdda` and drift as tasks land (T002). Symbols are truth
- Regenerate the surface golden with `MENTAT_UPDATE_GOLDEN=1 go test -run TestPublicSurfaceGolden`; there is no `make` target
- `examples/kafkaecho` must compile **untouched** throughout (SC-006), checked at T013, T019,
  T037 and T055
- Commit per task or logical group; `git add .` is forbidden — stage files individually
