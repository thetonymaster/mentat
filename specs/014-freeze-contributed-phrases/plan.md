# Implementation Plan: Freeze Contributed Phrases During Engine Composition

**Branch**: `014-freeze-contributed-phrases` | **Date**: 2026-09-11 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/014-freeze-contributed-phrases/spec.md`

**Baseline**: `f6bb402` — 012 merged to `main`.

## Summary

`Engine.ContributedPhrases` (`internal/engine/engine.go:241`) re-invokes every
contributing comparator's seam on **each call**. Three surfaces resolve phrases
independently — `Run`, `Validate`, `StepReference` — so a comparator built from mutable
state is validated on one vocabulary, documented on a second, and executed on a third.

This contradicts three artifacts that already promise the opposite (`mentat.go:66`,
`internal/core/core.go:168`, 012's `contracts/phrase-seam.md:24`). **It is a defect fix,
not a contract change.**

**Approach**: capture one ordered snapshot of phrase bindings inside `Build`, after
`reg.Seal()` and before the `*Engine` escapes; copy it at capture and again on every
return; drop the now-impossible error from the accessor and relocate its defensive check
into `Build`.

Research turned up one fact that shrinks the work considerably: the defect has three
*symptoms* but **one site**. All three surfaces reach the engine through a single
production caller, `resolvePhrases` (`internal/steps/phrase.go:291`). Fixing the engine
method fixes all three at once, and nothing in `internal/steps` needs restructuring.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: `godog v0.15.1` (pinned — five measured behaviours depend on
the pin), `go.uber.org/mock` (gomock), OpenTelemetry. **No new dependency.**

**Storage**: N/A — this feature touches engine composition, not trace resolution.

**Testing**: `go test`, table-driven, gomock for core interfaces, `godog` for the BDD
layer. `make ci` is the source of truth. `-race` is load-bearing here (SC-004).
The **unit lane is hermetic** — no Tempo, no network — and it already contains a
byte-identity oracle: `TestGoldenHermeticStdout` (`mentat_golden_test.go:70`) is in the
**root package with no build tag**, so `make ci` runs it (`make test` is
`go test ./... -race`). It byte-compares `mentat.Run`'s normalized stdout against
`testdata/golden-hermetic.txt`.

**T035 additionally runs the `//go:build e2e` golden lane** (needs `make harness-up`).
That is not redundant: `e2e/golden_test.go` pins the *same* transform against the live
harness, and `make ci` genuinely never compiles that lane — the recorded gap 012 was
bitten by. Two oracles, one hermetic and cheap, one live and gated.

**Correction**: an earlier draft of this plan called the e2e lane "the only byte-identity
oracle for SC-005". That was false and is the kind of claim that justifies a step with a
wrong statement about the gate — the hermetic golden was there all along and unclaimed.

**Target Platform**: Go library plus `cmd/mentat` CLI; darwin/linux.

**Project Type**: Single Go module — a trace-based behaviour test framework.

**Performance Goals**: No target to hit. The change strictly *reduces* work: comparator
seams go from O(surfaces × resolutions) invocations to exactly one per engine. The added
cost is one slice copy per call over a set of tens of entries.

**Constraints**:
- No package-level mutable state (FR-011) — this is the isolation property, not a style rule.
- No public API change. `engine.Engine` is `internal/`, so the signature change cannot
  move the public-surface golden.
- Per-engine isolation must survive in **both** construction orders, under `-race`.
- V1–V5 phrase validation stays in `internal/steps` — `internal/engine` cannot import it.

**Scale/Scope**: 40 built-in step patterns plus N contributed phrases per engine
(realistically single digits). One unexported field, one signature change, one production
call site.

## Constitution Check

*GATE: must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Verdict | Basis |
| --- | --- | --- |
| **I. Evidence-Only Comparators** | ✅ Pass | No comparator, `Evidence`, driver or store is touched. The seam signature is unchanged; comparators need no edit. |
| **II. Trace Is a Forest** | ✅ Pass | Not implicated — no correlation, resolution or trace code in scope. |
| **III. Seams Are Interfaces, Wired Once** | ✅ **Strengthened** | The snapshot is taken *at the single composition root*, `engine.Build`, which is precisely where wiring belongs. The current code does composition work lazily at three call sites; this moves it to the root. No framework DI introduced. |
| **IV. No Silent Fallbacks** | ✅ Pass, with one judged call | The feature *removes* a silent failure (drift nothing reports). The dropped error is relocated to `Build`, not swallowed (research R3). The one accepted risk — a hand-constructed `Engine` reading nil as "no phrases" — is documented at the field, with the reversal recorded (research R4). |
| **V. Test-First & Hermetic (NON-NEGOTIABLE)** | ⚠️ **Pass with two recorded deviations** | TDD red→green→refactor; routed to **go-test-writer**. All tests hermetic. 80% floor per package. No new L3 scenario is warranted (research R9) and the existing L3 suite must stay green. **Two deviations are documented in Complexity Tracking**: US2's tests cannot be red-first (one shared resolver), and one existing test must be inverted (spec D1, research R10). Neither was discovered at the keyboard — both are decided here. |

**Post-Phase-1 re-check**: unchanged. The design added one unexported field and removed
one error return; it introduced no new abstraction, no second registry, and no
package-level state. Nothing in Phase 1 moved any verdict above.

**Complexity Tracking carries two entries** — see below. Both are deliberate deviations
that the constitution's Governance clause requires be justified in the plan and mirrored
in the PR description, so the `go-reviewer` `gate` audit sees them rather than discovering
them.

## Project Structure

### Documentation (this feature)

```text
specs/014-freeze-contributed-phrases/
├── spec.md                       # Feature specification
├── plan.md                       # This file
├── research.md                   # Phase 0 — R1–R10, all decisions measured
├── data-model.md                 # Phase 1 — entities, lifecycle, invariants I1–I8
├── quickstart.md                 # Phase 1 — validation guide + mutation rehearsal
├── contracts/
│   └── phrase-snapshot.md        # Phase 1 — the engine-scoped snapshot contract
├── checklists/
│   └── requirements.md           # Spec quality checklist (all 16 pass)
└── tasks.md                      # Phase 2 — NOT created by /speckit-plan
```

### Source Code (repository root)

Files this feature touches, and why:

```text
internal/engine/
├── engine.go          # ContributedPhrases → reads the snapshot; drops the error return.
│                      #   Engine gains one unexported []PhraseBinding field.
├── build.go           # CAPTURE SITE. After reg.Seal() (:149), before the return (:150).
│                      #   Inherits the relocated registry-inconsistency error.
└── engine_test.go     # Ordering test updated for the new signature; new freeze tests.

internal/steps/
├── phrase.go          # :291 — the ONLY production caller. Drops one error check.
└── phrase_test.go     # Freeze behaviour through resolvePhrases.

# Root package (facade-level, black-box)
├── custom_phrase_isolation_test.go   # SC-004 oracle — must pass UNCHANGED.
└── custom_phrase_facade_test.go      # SC-001/SC-002 across all three surfaces.
```

`internal/core` takes **doc-comment edits only** (T031, T032) — no signature, field or
behaviour change; the seam itself is untouched and no comparator needs an edit.

Untouched entirely: every comparator, `mentat.go` (no public surface change), and the
V1–V5 validation rules.

**Structure Decision**: single Go module, existing layout, no new package. The change is
confined to `internal/engine` (the mechanism) plus its one caller in `internal/steps`,
with black-box proof at the root package where the existing phrase tests already live.
A new package would be ceremony — there is no second implementation and nothing to
decouple (`/composition`).

## Phase Summary

**Phase 0 — Research** (complete → [research.md](research.md)): ten questions, all
settled by measurement — two of them (R8, R10) added or corrected after cross-artifact
analysis caught errors in the first pass. Load-bearing results:

- **R1**: `Build` registers everything (`:42–51`, `:81`) and **seals** (`:149`) before the
  `Engine` exists (`:150`) — so a construction-time snapshot can never go stale.
- **R2**: one production caller, not three. The work is smaller than the spec implies.
- **R3**: drop the error — reading a precomputed snapshot cannot fail, and retaining it
  would leave a caller branch no test could execute under an 80% floor.
- **R5**: no mutex — the write precedes the pointer's escape from `Build`.
- **R6**: per-engine field only. A package-level cache is the exact bug 012 deleted.
- **R8 (corrected)**: **one** explicit copy, on return. The first pass claimed two were
  needed; measured false — the capture loop builds `[]PhraseBinding` element-wise from a
  ranged value, so the transform *is* the capture copy. A separate capture copy would be a
  no-op, and the comparator-mutation test it was paired with is red only *before* the
  freeze, so that test moved to Phase 3 (T008a).
- **R10** (added after cross-artifact analysis): one committed test,
  `TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine`
  (`internal/steps/phrase_test.go:820`), **asserts** the per-call behaviour and goes red
  under the freeze. Two more fail to compile on the signature change. R2 counted callers
  correctly but never asked which tests assert the behaviour — a green suite proves no
  test disagrees with the *current code*, not that none disagrees with the *plan*.

**Phase 1 — Design & Contracts** (complete): [data-model.md](data-model.md) (lifecycle +
invariants I1–I8), [contracts/phrase-snapshot.md](contracts/phrase-snapshot.md) (the
guarantee, stated as enforcement of 012's existing promise rather than new surface),
[quickstart.md](quickstart.md) (seven validation scenarios, each tied to an SC, plus the
mandatory mutation rehearsal).

**Phase 2 — Tasks**: not created by this command. Run `/speckit-tasks`.

## Complexity Tracking

Two deliberate deviations. Neither is a shortcut; both are recorded so review sees them
stated rather than inferred.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| **US2's tests cannot be observed RED before their implementation** (Principle V, Test-First) | All three surfaces share one resolver (`phrase.go:291`), so US1's mechanism makes `StepReference` consistent as a side effect. There is no ordering in which US2's test fails first. | Splitting the resolver so US2 could fail independently would add a seam with no second implementation, purely to satisfy test choreography — over-abstraction to manufacture a red. Instead the red is established by **mutation rehearsal (T029)**, which asserts the source edit applied before trusting the result. |
| **One existing test is edited to invert an assertion** (standing rule: never edit a test to make an implementation pass) | `TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine` (`internal/steps/phrase_test.go:820`) asserts `after == 2` — the exact behaviour FR-003 removes. It cannot both stay and let the feature land. | Leaving it red is not an option, and silently relaxing it is the failure mode the rule exists to prevent. Recorded instead as spec **D1** with the evidence that the test contradicts its own name and doc comment, and confined to **T015a** — the only test edit this feature authorises. |

## Risks

| Risk | Severity | Mitigation |
| --- | --- | --- |
| Implementing the snapshot as a package-level cache | **High** — silently re-breaks engine isolation, the exact bug 012 deleted | Per-engine field (FR-011). `TestContributedPhrasesAreScopedToTheirEngine` must pass **unchanged**, both orders, under `-race`. Do not edit that test to accommodate the implementation. |
| Adding a redundant capture-time copy | **Medium** — a no-op task paired with an unachievable RED, which is how a fabricated green gets recorded as a guard | The capture transform already copies (research R8, corrected). T026 says explicitly not to add one; the comparator-side property is pinned by T008a in Phase 3, where it genuinely goes red. |
| A future reference-typed field on `ContributedPhrase`/`PhraseBinding` | **Medium** — voids the shallow-copy premise silently, with no test failing | Flagged in research R7, data-model, and the contract as a standing condition. `/speckit-tasks` should decide whether it earns an explicit guard. |
| e2e goldens drift unnoticed | **Medium** — `make ci` does not compile the `//go:build e2e` lane (012 was bitten by exactly this) | T035 runs `go test -tags e2e ./e2e/...` **unconditionally**. A conditional run would rest on a judgement no gate can make. |
| Mutation rehearsal that never fired | **Medium** — a guard believed real but untested | Assert the source edit applied before trusting RED. 012 recorded one that failed this way. |
