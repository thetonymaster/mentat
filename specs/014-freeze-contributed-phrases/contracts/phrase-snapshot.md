# Contract: the engine-scoped phrase snapshot

**Fulfils**: FR-001 – FR-011; SC-001 – SC-008. Research: [R1](../research.md),
[R3](../research.md), [R5](../research.md), [R6](../research.md), [R8](../research.md).

**Placement**: `internal/engine`. An unexported field on `Engine`, written in `Build`.
Explicitly **not** package-level — that placement is the defect this contract forbids
(see *Isolation*, below).

**Relationship to 012**: this contract does not extend
[`phrase-seam.md`](../../012-comparator-gherkin-phrases/contracts/phrase-seam.md) — it
**enforces** it. 012 §"Contributed-phrase seam" already states the seam is "Called once
per engine build, never per scenario" and that a comparator mutating its returned slice
"has no effect on the built engine". Both sentences were true of the design and false of
the code. Nothing here is new promise surface.

---

## The guarantee

> For any engine `E`, there exists exactly one ordered set of phrase bindings `S(E)`,
> determined at `E`'s construction. Every observation of `E`'s contributed phrases —
> by any surface, at any time, any number of times — yields `S(E)`. No observation can
> alter `S(E)`, and no party outside `Build` can alter it either.

### 1. Consulted exactly once

Each comparator implementing the seam has `ContributedPhrases()` invoked **once per
engine**, during `Build`. Not once per surface, not once per resolution, not once per
scenario.

Measurable: a comparator that counts its own invocations reports exactly `1` after any
sequence of `Run`, `Validate` and `StepReference` on that engine (SC-002).

### 2. Captured at composition

Capture happens inside `Build`, after `reg.Seal()` (`build.go:149`) and before the
`*Engine` is returned (`:150`).

The seal is what makes this sound rather than merely early: post-seal registration
panics, so the comparator set cannot change after capture. Capturing *before* the seal
would be a snapshot of a set still able to grow.

### 3. Answered by copy

Two mutation directions are forbidden; **one explicit copy delivers both**:

| Direction | Forbids | Delivered by | Spec |
| --- | --- | --- | --- |
| comparator → engine | the **comparator** mutating the slice it returned | the capture transform (`[]ContributedPhrase` → `[]PhraseBinding`, element-wise) | FR-002 |
| engine → caller | the **caller** mutating the slice it received | an explicit `slices.Clone` on return | FR-004 |

The first requires no separate copying step: the engine never retains the comparator's
slice header, it builds a different type element by element. A second capture-time copy
would be a no-op (research R8).

A shallow copy is complete: every field of `PhraseBinding` and `ContributedPhrase` is a
`string` (see [data-model.md](../data-model.md)). **This is a standing condition, not a
one-time check** — introducing a reference-typed field to either struct voids **both**
rows of the table above silently, which is why it is enforced by an assertion rather than
a comment.

### 4. One set across all surfaces

`Run` (registration), `Validate` (findings) and `StepReference` (documentation) observe
the same `S(E)`. Structurally guaranteed: all three reach it through `resolvePhrases`
(`internal/steps/phrase.go:291`), which is now the sole reader of an already-frozen set.

### 5. Ordering is preserved and load-bearing

`S(E)` is ordered by comparator name, preserving each comparator's own declaration order
within its block. This is a **correctness** requirement, not tidiness: the runner returns
the first matching step definition, so this order decides which pattern wins a collision.
Map iteration order would make an unchanged suite resolve differently between runs.

### 6. Isolation

`S(E)` belongs to `E` alone. Two engines in one process observe only their own phrases,
in **either** construction order, including concurrently under `-race`.

This clause has teeth because it has already been violated: 012 deleted a package-level
first-writer-wins `sync.Once` phrase cache for breaking exactly this. Its regression
oracle, `TestContributedPhrasesAreScopedToTheirEngine`
(`custom_phrase_isolation_test.go:121`), runs both orders **because a first-writer-wins
cache passes one and fails the other** — a single-order test would call the bug fixed
about half the time.

No synchronisation is needed to satisfy this: the snapshot is written before the `*Engine`
pointer escapes `Build`, so every reader has a happens-before edge (research R5).

---

## Interface

| Before | After |
| --- | --- |
| `ContributedPhrases() ([]PhraseBinding, error)` | `ContributedPhrases() []PhraseBinding` |

`engine.Engine` is `internal/`, so this is not public API and cannot move the
public-surface golden. One production caller is affected (`phrase.go:291`).

**The error is relocated, not discarded.** It covered one case — a comparator the
registry lists but cannot resolve — which the code itself documents as "unreachable
today". Reading a precomputed snapshot cannot fail, so the check moves into `Build`
(already error-returning) with its message intact. Retaining a permanently-nil error
would leave a caller branch no test could ever execute, under an 80% floor (research R3).

---

## What this contract supersedes

One committed assertion states the opposite of clause 1 and is inverted by this contract:
`TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine`
(`internal/steps/phrase_test.go:820`) requires `after == 2` after a comparator appends a
phrase post-build — "resolution must reflect the comparators as they are when called".

Under this contract the answer is `1`. The inversion is recorded as spec **D1** and
implemented by task **T015a**, and it is the only assertion this feature changes.

Worth noting *why* it is safe: that test's **name** ("…Do Not Affect The Built Engine")
and its **doc comment** ("the resolved set is a snapshot"; post-build mutation "has NO
effect, and that is the correct outcome") already describe the frozen behaviour. Only its
closing assertion describes the per-call behaviour. This contract resolves a
contradiction the test carried within itself.

## What this contract does NOT change

- **The seam itself.** `PhraseContributor` and `CaptureParser` are untouched. No
  comparator needs an edit.
- **Phrase validity.** V1–V5 (compilable, anchored, no duplicate, no built-in collision,
  documentation fields present) stay in `internal/steps`, run at composition, and fail
  with unchanged messages before any SUT is driven (FR-008). `internal/engine` cannot
  import `internal/steps`, and V4 is a statement about `stepDefs` — so the rules stay
  where 012 put them. Freezing changes the *input* they read, never the rules.
- **Intra-run consistency.** `InitializerWithBudget` already resolves once and threads
  the result to both argument checks and registration (`steps.go:101`, `:107`). Already
  correct; now a regression surface.
- **Engines that contribute nothing.** The common path allocates nothing, registers
  nothing, and renders a step reference deep-equal to the built-in-only one; run output
  stays byte-identical under `TestGoldenHermeticStdout` (FR-009, SC-005).
- **Any published type or signature.** This is a defect fix. A comparator that already
  declares phrases from immutable state sees no behavioural difference whatsoever
  (FR-010) — which is the point: the contract was always this.
