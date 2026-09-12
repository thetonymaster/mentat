# Phase 0 Research: Freeze Contributed Phrases During Engine Composition

**Feature**: `specs/014-freeze-contributed-phrases`
**Date**: 2026-09-11
**Baseline**: `f6bb402` (012 merged to `main`)

Every finding below was **measured against the working tree**, not inferred. Where a
claim is defensive reasoning rather than an observation, it says so.

---

## R1 — Is a Build-time snapshot even sound? (Can the registry change after Build?)

**Decision**: Yes, sound. Capture the snapshot inside `engine.Build`, after `reg.Seal()`.

**Rationale (measured)**: `Build` (`internal/engine/build.go:30`) has exactly this order:

| Line | What happens |
| --- | --- |
| `:42–51` | Built-in drivers, comparators, aggregate comparators, matchers registered |
| `:81` | `applyExtras(cfg, o, reg)` — **custom** comparators registered (`:210`), the only ones that realistically contribute phrases |
| `:149` | `reg.Seal()` — "wiring complete: post-build registration now fails loudly (FR-009)" |
| `:150` | `&Engine{… reg: reg}` returned |

So by the time the `Engine` value exists, every comparator is registered **and the
registry is sealed**. A snapshot taken at that point cannot go stale, because a stray
`Register*` after the seal panics rather than mutating the set. This is the property
007 established (FR-009) and it is what makes a construction-time snapshot correct
rather than merely convenient.

**Alternatives considered**:

- *Lazy capture on first read (`sync.Once` field)*. Rejected. It satisfies "capture once"
  but not "capture during composition" — the spec's FR-001 — and it makes the first
  caller's identity decide when comparator code runs. It also needs synchronisation that
  the Build-time write does not (see R5).
- *Snapshot incrementally as comparators register*. Rejected. Ordering is sorted by
  comparator name (FR-006), so the set must be walked after registration completes
  anyway; incremental capture would buy nothing and would interleave two concerns at the
  composition root.

---

## R2 — How much churn does this actually cause?

**Decision**: Very little. `Engine.ContributedPhrases` has exactly **one** production
caller.

**Rationale (measured)**: `grep` for `ContributedPhrases()` excluding tests and mocks
yields one production call site outside the engine itself:
`internal/steps/phrase.go:291`, inside `resolvePhrases`.

The three surfaces named in the spec — `Run`, `Validate`, `StepReference` — do **not**
call the engine method directly. They each call `resolvePhrases`, which calls it once:

```
Run          → InitializerWithBudget (steps.go:101)  ┐
Validate     → EngineStepChecks     (phrase.go:612)  ├→ resolvePhrases → eng.ContributedPhrases()
StepReference→ EngineStepDocs       (phrase.go:519)  ┘                      (phrase.go:291)
```

This reframes the work: the defect has three *symptoms* but one *site*. Fixing the
engine method fixes all three surfaces at once, and no call site in `internal/steps`
needs restructuring.

**Correction (added after cross-artifact analysis):** this question asked "how many
places *call* the method" and answered it correctly. It did not ask "how many places
*assert* the current behaviour", and that omission hid a committed test which pins the
per-call contract and will go red under this feature — see **R10**. Counting callers is
not the same as counting contracts. The measurement was right; the question was too
narrow.

---

## R3 — Should `ContributedPhrases` keep returning an `error`?

**Decision**: **Drop the error.** New signature `ContributedPhrases() []PhraseBinding`.

**Rationale**: Today the method returns an error for one case — a comparator listed by
the registry that cannot be resolved from it — which its own comment already calls
"unreachable today: name came from this same sealed registry's own listing"
(`engine.go:250–256`). Once the walk happens at Build, reading the snapshot cannot fail
at all. Keeping `([]PhraseBinding, error)` would mean a signature that promises a failure
its own contract no longer admits — the accessor becomes a field read, and a field read
does not fail.

Constitution IV is about not *hiding* failures. Removing an error that can no longer
happen is not hiding one; keeping it would be the dishonest option.

**One argument deliberately NOT relied on**: an earlier draft also justified this by the
caller's `if err != nil` branch being permanently unexecutable under an 80% floor. That
argument does not survive the relocation — T012 moves the same unreachable check into
`Build`, where it is equally unexecutable. The relocation is still right (Principle IV:
report it, don't swallow it), but it relocates the dead branch rather than removing one,
so the coverage framing was never the real reason. The contract argument above is.

**Cost, measured**: one production call site (`phrase.go:291`) and the engine tests.
`resolvePhrases` keeps its own `error` return — the V1–V5 phrase-validity failures are
unaffected and still flow from there.

**Where the old error goes**: the defensive registry-inconsistency check moves into
`Build`, which already returns `(*Engine, error)`, and keeps its message. It stays
unreachable-but-loud, which is the same posture `isNilSeam` (`build.go:159`) takes for
its own defensive case — the established house pattern.

**Alternative considered**: keep the signature to avoid churn. Rejected on the dead-branch
and honesty grounds above; the churn it avoids is one call site.

---

## R4 — Does a directly-constructed `Engine` become a silent fallback?

**Decision**: Accept nil-snapshot == no phrases, and pin `Build`-as-sole-constructor with
the existing doc contract. Do **not** add a sentinel.

**Rationale (measured)**: `Engine` is documented "Build is the only way to construct it"
(`engine.go:26`). One direct construction exists in the whole tree —
`internal/engine/engine_test.go:185` — and it passes `reg: registry.New()`, an **empty**
registry, deliberately, to reach `driveOnce`'s missing-driver guard. An empty registry
genuinely contributes no phrases, so nil-snapshot and computed-snapshot **agree** there.
There is no case today where the two answers differ.

**The residual risk, stated plainly**: a *future* direct construction with a populated
registry would silently observe no phrases. That is a real landmine, and it is the one
place this design could re-introduce the class of bug it exists to remove.

**Why not a sentinel anyway**: a `snapshotTaken bool` (or pointer-typed field) would make
that case a loud error, but it can only be exercised by a test that itself does the thing
the type contract forbids — so it buys a guard whose only caller is its own test, which
is the shape `/composition` calls an abstraction without a second implementation.

**Mitigation instead**: state it in the field's doc comment at the point of definition, so
the next person constructing an `Engine` by hand reads it there rather than discovering
it. Recorded here so the decision is auditable rather than accidental. **If planning
disagrees, the sentinel is the cheap reversal** — it is additive and breaks nothing.

---

## R5 — Concurrency: does the snapshot need a mutex?

**Decision**: No synchronisation needed.

**Rationale**: the snapshot is written once inside `Build`, **before** the `*Engine`
pointer escapes the function. Every reader therefore observes it through a
happens-before edge established by the pointer's publication. This is the same reason
`cfg`, `reg` and `pricing` need no locking today.

This is a genuine advantage of Build-time capture over the lazy `sync.Once` alternative
(R1): lazy capture publishes the pointer first and fills the field later, so it needs
`sync.Once` to be race-free — which is precisely why the existing `resolveOnce` field
has one. Capturing at Build removes the need rather than managing it.

**Verification**: SC-004 requires the two-engine isolation test to pass under `-race`,
which exercises exactly this.

---

## R6 — Does freezing break the deleted-cache isolation guarantee?

**Decision**: No — provided the snapshot is a **field on `Engine`** and never package-level.

**Rationale (measured)**: 012 deleted a package-level, first-writer-wins `sync.Once`
phrase cache because it answered engine B with engine A's phrases. The guard is
`TestContributedPhrasesAreScopedToTheirEngine`
(`custom_phrase_isolation_test.go:121`), which runs two engines with disjoint vocabularies
in **both** construction orders — deliberately, because a first-writer-wins cache passes
one ordering and fails the other, so a single-order test "would have reported this bug
fixed roughly half the time."

A per-engine field is structurally incapable of that failure: there is no shared cell to
win. The codebase already states the distinction at
`internal/steps/phrase.go:37` — `resolveOnce` "is a FIELD on Engine, so it is per-engine
and carries the isolation property rather than breaking it."

**This test must be re-run unchanged, not adapted.** It is the regression oracle for the
one way this feature could go badly wrong.

---

## R7 — Is a shallow copy sufficient for FR-002 and FR-004?

**Decision**: Yes. `slices.Clone` (or equivalent) on `[]PhraseBinding` is a complete copy.

**Rationale (measured)**: `ContributedPhrase` (`internal/core/core.go:145–158`) has four
fields — `Pattern`, `Group`, `Summary`, `Example` — all `string`. `PhraseBinding`
(`engine.go:219–222`) adds `Comparator string` and embeds the phrase by value. There is
no slice, map, pointer or interface field anywhere in the graph, so copying the outer
slice fully severs aliasing.

**The fragility is real and worth a guard**: this holds only while both structs stay
plain-text. Adding a reference-typed field to either silently re-opens FR-002/FR-004
with no test failing. Planning should decide between a doc note at both struct
definitions and a compile-time or reflective assertion; this research does not settle it,
but flags it as the highest-value cheap guard in the feature (it is also
`requirements.md` Note 3).

---

## R8 — Copy-on-capture as well as copy-on-return?

**Decision (CORRECTED)**: **Only one explicit copy is needed — on return.** Capture-time
copying is already done by the transform and must not be added as a separate step.

**The original answer here was wrong**, and the error is worth keeping visible because it
would have produced a no-op task and an unachievable RED.

**What it claimed**: that omitting a capture-time copy would leave the snapshot "aliasing
memory the comparator still owns". **Measured false.** The engine never stores the
comparator's slice. `engine.go:260-262` builds a *different type*, element-wise:

```go
for _, p := range pc.ContributedPhrases() {
    out = append(out, PhraseBinding{Comparator: name, Phrase: p})
}
```

`range` copies each element into `p`; `PhraseBinding{Phrase: p}` copies it again into
`out`'s own backing array. Every field in the graph is a `string` (R7). So the
`[]ContributedPhrase` → `[]PhraseBinding` transform **is** the copy. There is no alias
left to sever.

| Copy | Status | Spec |
| --- | --- | --- |
| At capture | **Already done by the transform** — no separate step, and adding one is a no-op | FR-002 |
| On return | **Genuinely required** — the snapshot itself is handed out | FR-004 |

**Consequences, which matter more than the correction itself:**

1. **FR-002 is satisfied by the capture loop**, not by a distinct copying task. Nothing
   needs building for it beyond the transform R1 already prescribes.
2. **A test that a comparator cannot mutate the engine's answer is red TODAY and goes
   green with the freeze** — because today the engine *re-invokes* the comparator, so a
   mutated list is observed on the next call. Its red comes from the missing freeze
   (FR-003), not from a missing copy. **It therefore belongs in the US1 phase, not US3.**
3. **What actually keeps FR-002 true over time is R7's field-kind guard**, not a copy.
   Add a reference-typed field to `ContributedPhrase` and the transform stops being a
   deep copy — silently. That is why R7's guard was upgraded from a doc note to an
   enforced assertion.

**Method note**: the original claim reasoned about "a slice the comparator owns" without
checking what the engine actually stores. Go's value semantics make the transform a copy;
the premise assumed a retained header that the code never had.

---

## R9 — Does this need an L3 meta-test?

**Decision**: No new L3 scenario. The equivalence tests are the proof, and the existing
L3 suite must stay green.

**Rationale**: the mandatory L3 test proves Mentat goes RED on bad *SUT* behaviour. This
feature changes no verdict logic and adds no comparator — a drifting phrase set produces
an unbound step or a mis-bound one, not a wrong pass/fail on a SUT. The falsifiable claim
here is "all three surfaces see one set", which US1–US3 test directly.

**What this feature owes instead** is a mutation rehearsal, per the house convention
recorded in 011 and 012: revert the freeze and confirm the new tests actually go RED.
012's own notes warn that "the mutation didn't fire" and "the guard is real" are
indistinguishable from test output alone, so the rehearsal must assert that the source
edit applied.

---

## R10 — Which existing tests assert the behaviour this feature removes?

**Decision**: Exactly one test must be inverted; two more need mechanical updates. All
three are accounted for in `tasks.md`, and the inversion is recorded as spec **D1**.

**Rationale (measured)**: R2 counted *callers* and found one. Counting *assertions* finds
more:

| Test | Location | Impact | Kind |
| --- | --- | --- | --- |
| `TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine` | `internal/steps/phrase_test.go:820` | **Goes RED.** Asserts `after == 2` — "resolution must reflect the comparators as they are when called" | **Contract inversion** (spec D1) |
| `TestEngineContributedPhrasesResolvesInSortedComparatorOrder` | `internal/engine/engine_test.go:1877` | Fails to compile — two-value form | Mechanical |
| `TestEngineContributedPhrasesIsEmptyWithoutContributors` | `internal/engine/engine_test.go:1928` | Fails to compile — two-value form | Mechanical |

The first is the one that matters. It is not a test that merely *exercises* per-call
resolution; it **asserts** it, in prose, as correct. An implementer who reached it with no
task authorising a change would be in exactly the position the repo's standing rule
forbids — editing a test to make an implementation pass.

**What makes the inversion principled rather than convenient**: the test contradicts
itself. Its name ("…Do Not Affect The Built Engine") and its doc comment ("the resolved
set is a snapshot"; post-build mutation "has NO effect, and that is the correct outcome")
both describe the frozen behaviour. Only the closing assertion describes the per-call
behaviour. Four statements of intent exist across the codebase — the test's name, the
test's comment, 012's seam doc, 012's contract — and the assertion disagrees with all
four. See spec **D1** for the full table.

**Method note worth keeping**: the general lesson is cheap to apply and was skipped here.
Before changing a documented behaviour, grep the test tree for assertions *about* that
behaviour, not just call sites of the function that implements it. A green suite is not
evidence that no test disagrees with your plan — it is evidence that no test disagrees
with the *current code*.

---

## R11 — At which layer is the defect actually observable?

**Decision**: `internal/steps`, driving one `*engine.Engine`. **Not** the facade. The
defect is latent through the public API (spec **D2**).

**Rationale (measured during implementation)**: each facade entry point constructs its own
engine and resolves phrases once against it.

| Entry point | `engine.Build` at | Surface |
| --- | --- | --- |
| `mentat.Run` | `run.go:344` | `InitializerWithBudget` (`run.go:381`) |
| `mentat.Validate` | `run.go:682` via `buildEngineForInspection` | `EngineStepChecks` (`run.go:596`) |
| `mentat.StepReference` | `run.go:682` via `buildEngineForInspection` | `EngineStepDocs` (`run.go:544`) |

A shared stateful contributor driven through all three facade entry points was measured
at `calls=1, 2, 3` — one per entry point, because each builds a fresh engine. **That count
is identical after the freeze**, since the call merely moves from the surface into `Build`.
The facade cannot distinguish frozen from unfrozen, in either direction:

- with a **shared** contributor instance, the surfaces disagree before *and* after (a
  permanently red test);
- with a **fresh** instance per build, they agree before *and* after (a permanently green
  one).

Driving all three surfaces from **one** engine in `internal/steps` produces the real
failure today: documented `^the first reading is (\w+)$`, validated and registered
`^the later reading is (\w+)$`. That is SC-001 failing verbatim, and it is red now and
green after the fix.

**Why this matters beyond test placement**: it demotes the defect from live to latent and
corrects this spec's opening framing (see D2). The fix stays worth making — the contract
at `core.go:168` is unenforced, and it becomes live the moment any engine serves a second
surface — but the honest claim is "unenforced contract", not "users are being misled
today".

**Method note**: R2 established that three surfaces call one resolver. It never asked
*how many engines those surfaces run against*. "Three call sites" and "three call sites
that can disagree" are different claims, and only the second is the bug. This is the third
question-too-narrow error in this feature's research (with R2/R10 and R8) — the pattern is
asking about the code's structure rather than about the conditions under which the failure
can actually occur.

---

## Summary of decisions

| # | Decision |
| --- | --- |
| R1 | Capture in `Build`, after `reg.Seal()`, before the `Engine` is returned |
| R2 | One production call site to touch (`phrase.go:291`); the three surfaces fix themselves |
| R3 | Drop the `error` from `ContributedPhrases`; move the defensive check into `Build` |
| R4 | nil-snapshot == no phrases; document at the field; no sentinel (reversible if planning disagrees) |
| R5 | No synchronisation — the write happens before the pointer escapes `Build` |
| R6 | Per-engine field only; re-run the two-order isolation test unchanged as the oracle |
| R7 | Shallow copy is complete; flag the plain-text-only assumption for a guard |
| R8 | **Corrected** — one explicit copy, on return only. The capture transform already copies (element-wise into `[]PhraseBinding`), so FR-002 needs no separate step and its test belongs in US1, not US3 |
| R9 | No new L3 scenario; mutation rehearsal required instead |
| R10 | One test asserts the removed behaviour and is inverted deliberately (spec D1); two more need mechanical signature updates |
| R11 | The defect is observable only with one engine across surfaces — `internal/steps`, not the facade. Latent through the public API (spec D2) |

**No `NEEDS CLARIFICATION` items remain.** The spec carried none, and the one question
with design weight (does snapshotting change when comparator factories run?) was settled
in the spec's Assumptions by measurement: the registry holds already-constructed
comparator values (`internal/registry/registry.go:117-121` — the body is a plain map
read, not a factory call), so it does not.
