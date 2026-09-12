# Data Model: Freeze Contributed Phrases During Engine Composition

**Feature**: `specs/014-freeze-contributed-phrases`
**Date**: 2026-09-11

This feature introduces **no new published type**. It adds one unexported field and
changes one method signature. That is deliberate: the behaviour it delivers is already
documented (`mentat.go:66`, `internal/core/core.go:168`,
`specs/012-comparator-gherkin-phrases/contracts/phrase-seam.md:24`), so the data model
exists to make the code match the contract, not to extend it.

---

## Existing entities (unchanged)

### `core.ContributedPhrase`

`internal/core/core.go:145–158`. One Gherkin sentence a comparator offers.

| Field | Type | Rule |
| --- | --- | --- |
| `Pattern` | `string` | Anchored regexp: begins `^`, ends with an unescaped `$` (V2) |
| `Group` | `string` | Non-empty. Reference section heading (V5) |
| `Summary` | `string` | Non-empty. One-line description (V5) |
| `Example` | `string` | Non-empty. One valid Gherkin usage (V5) |

**All four fields are `string`.** This is load-bearing for FR-002/FR-004 — it is what
makes a shallow slice copy a complete copy (research R7). Adding a slice, map, pointer
or interface field here silently re-opens this feature's defect.

### `engine.PhraseBinding`

`internal/engine/engine.go:219–222`. One phrase paired with its contributor.

| Field | Type | Rule |
| --- | --- | --- |
| `Comparator` | `string` | The registry name of the comparator that declared the phrase |
| `Phrase` | `core.ContributedPhrase` | Embedded **by value**, not by pointer |

Also all-value, for the same reason.

### `core.PhraseContributor`

`internal/core/core.go:172–174`. The optional seam, discovered by type assertion:

```go
ContributedPhrases() []ContributedPhrase
```

**Unchanged.** Comparators implementing it need no edit. What changes is how many times
Mentat calls it — from "once per surface, per resolution" to "exactly once per engine".

---

## New state: the phrase snapshot

One unexported field on `engine.Engine`.

| Property | Value |
| --- | --- |
| Name | *(to be fixed in implementation; e.g. `phrases`)* |
| Type | `[]PhraseBinding` |
| Scope | **Per engine.** Never package-level (research R6 — this is the whole isolation property) |
| Written | Exactly once, inside `Build`, after `reg.Seal()` (`build.go:149`) and before the `*Engine` is returned (`:150`) |
| Read | Many times, concurrently, for the life of the engine |
| Synchronisation | **None required** — the write happens before the pointer escapes `Build`, establishing happens-before for every reader (research R5) |
| Ordering | Sorted by comparator name; each comparator's own declaration order preserved within its block (FR-006) |

### Lifecycle

```
Build starts
  │
  ├─ register built-in comparators            (build.go:42–51)
  ├─ applyExtras → register custom comparators (build.go:81 → :210)
  ├─ reg.Seal()  → registry now immutable      (build.go:149)
  │
  ├─ CAPTURE ───────────────────────────────────────────────┐
  │    for each comparator name, sorted:                    │
  │      resolve it; type-assert PhraseContributor;         │  exactly one call
  │      call ContributedPhrases(); COPY the result         │  per contributor,
  │    → snapshot                                           │  for the engine's
  │                                                          │  whole lifetime
  └─ &Engine{… phrases: snapshot} returned  (build.go:150) ──┘
       │
       ▼
  ContributedPhrases() → returns a COPY of the snapshot, every time, forever
```

### One explicit copy, not two (research R8, corrected)

Two mutation directions must be defended, but only one needs code:

| Direction | Prevented by | Spec |
| --- | --- | --- |
| the comparator mutating the slice it handed back | **the capture transform itself** — no separate step | FR-002, US3 §2 |
| a caller mutating the slice it was handed | **an explicit copy on return** (`slices.Clone`) | FR-004, US3 §1 |

The first needs no code because the engine never stores the comparator's slice. It builds
a **different type**, element-wise: `range` copies each `ContributedPhrase` into a loop
variable, and `PhraseBinding{Phrase: p}` copies it again into a fresh backing array. With
every field a `string`, that transform is a complete copy. Adding a second capture-time
copy would be a no-op.

The second does need code: `ContributedPhrases` hands out the snapshot slice itself, so
without `slices.Clone` the caller shares the engine's backing array.

**What keeps the first true over time is the field-kind guard**, not a copy. A
reference-typed field on either struct would make the transform shallow — silently. That
is why the R7 guard is an enforced assertion rather than a doc note.

### The nil-snapshot case

A directly-constructed `Engine` (bypassing `Build`) has a nil snapshot, which reads as
"no contributed phrases". Today that is correct and unobservable: the single direct
construction in the tree (`internal/engine/engine_test.go:185`) passes an **empty**
registry, which genuinely contributes nothing, so both answers agree.

Accepted with a doc comment at the field rather than a sentinel — see research R4 for
why, and for the reversal if planning disagrees.

---

## Signature change

| Before | After |
| --- | --- |
| `func (e *Engine) ContributedPhrases() ([]PhraseBinding, error)` | `func (e *Engine) ContributedPhrases() []PhraseBinding` |

`engine.Engine` lives in `internal/`, so this is **not** a public API change and cannot
appear in the public-surface golden. One production call site is affected
(`internal/steps/phrase.go:291`).

The dropped error covered one case — a comparator listed by the registry but not
resolvable from it — which the code itself calls "unreachable today". It does not
disappear: it **moves into `Build`**, which already returns an error, and keeps its
message. Rationale for removing rather than retaining it (a permanently-dead caller
branch under an 80% coverage floor) is research R3.

**What does not change**: `resolvePhrases` keeps its `error` return. Every V1–V5 phrase
validity failure — uncompilable, unanchored, duplicate, built-in collision, missing
documentation field — flows from there exactly as today, at composition, before any SUT
is driven (FR-008).

---

## Invariants to hold

| # | Invariant | Enforced by |
| --- | --- | --- |
| I1 | A contributor's seam is invoked exactly once per engine | FR-001 / SC-002 (counting contributor) |
| I2 | All surfaces observe one identical set per engine | FR-005 / SC-001 |
| I3 | No caller can mutate another caller's view | FR-004 / SC-003 |
| I4 | No comparator can mutate the engine's view post-capture | FR-002 / SC-003 |
| I5 | Two engines never share phrase data, in either order, under `-race` | FR-007 / SC-004 |
| I6 | Order is deterministic and preserved | FR-006 |
| I7 | No package-level mutable phrase state | FR-011 / research R6 |
| I8 | Malformed phrases still rejected at composition, same messages | FR-008 / SC-006 |
