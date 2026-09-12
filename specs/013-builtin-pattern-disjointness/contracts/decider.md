# Contract: the intersection decider

**Feature**: 013 | **Status**: proposed | **Scope**: `internal/` only — publishes nothing on the facade

## Surface

```go
// Intersects decides whether any string matches both patterns.
func Intersects(a, b string) (Intersection, error)
```

Internal to `internal/steps` (R5). Not aliased on the facade, not a comparator seam, not a
`core` interface. If the public-surface golden changes when this lands, something is wrong —
stop rather than update the golden (010 D5: only terminal types may be facade-declared).

## What it guarantees

0. **Precondition: both patterns are whole-text anchored**, and it CHECKS this rather than
   assuming it. The decider reasons over the language of the compiled program; `regexp.MatchString`
   asks whether an unanchored pattern matches *anywhere*. Those differ, so an unanchored pattern is
   a construct it does not model and is refused (FR-019). An earlier version of this contract said
   "over the whole language of each pattern" while the code silently answered "disjoint" for
   `"a"` vs `"ba"` — measured by review.

1. **It decides.** For a pattern pair it accepts, the answer is over the whole language of each
   pattern, not over any corpus of example sentences. No filler set, no sampling, no sentence
   generation.
2. **A positive verdict proves itself.** `Intersects == true` carries a witness string, verified by
   `MatchString` against both compiled patterns before it is reported (FR-011).
3. **It refuses what it cannot model**, with an error naming the pattern and the construct — never
   a "disjoint" verdict (FR-012). The refusal set is the four empty-width assertions in
   `data-model.md` §3, **measured** (R4), plus any `EmptyOp` bit it does not recognise.
4. **It terminates.** Reachable product states are finite and deduplicated.

   The figure "max 74 states per pair" is a **measurement over the built-in set, not a bound on
   author input** — and author input is the reachable path, since contributed patterns are decided
   too. The search is worst-case exponential in the two programs' state counts, so it is bounded by
   **`maxProductStates` (100,000 reachable product states)**. Exceeding the bound is a **refusal**
   (`errSearchBudget`), never a verdict: per-pair `pattern-undecidable` for contributed patterns, a
   hard failure for the built-in gate.

   **This paragraph previously declined the bound, and was wrong.** It argued that no blowup could
   be produced — "best adversarial attempt: 532µs, from two independent tries" — and that a
   cancellation seam with no second implementation failed `/composition`. The second half still
   holds and is why this is a constant and a counter rather than a `context.Context` seam. The
   first half was **evidence mistaken for proof**, which is the precise error 013 exists to correct,
   applied here to the decider's cost instead of its verdict. One deliberate construction refuted it:

   | pair | result |
   |---|---|
   | `^[ab]*a[ab]{4}$` vs `^[ab]*b[ab]{4}x$` | decided, 365µs |
   | …`{8}`… | decided, 5.37ms |
   | …`{12}`… | decided, 83.5ms |
   | …`{16}`… | **refused at the bound**, 255ms |
   | …`{20}`… | **refused at the bound**, 258ms |

   ~15× per +4, and both patterns are **anchored and legal**, so a consumer can contribute them and
   reach this through `mentat.Validate`. Unbounded, n=20 runs for tens of seconds and n=24 for
   minutes, with memory tracking it — every queued node retains its witness prefix. Pinned by
   `TestSearchBudgetRefusesRatherThanReportingDisjoint`.

   The bound is ~1350× the largest pair **the built-in gate** needs (74), so it cannot refuse work
   that gate legitimately does. **That guarantee does not extend to contributed patterns**, and
   saying otherwise was an overclaim caught in review — inside the paragraph correcting an
   overclaim. A legal, anchored contributed phrase can exceed it: the n=16 fixture above is exactly
   that. For consumer patterns the bound is a cost ceiling, not a promise of decidability, and
   hitting it yields `pattern-undecidable` rather than a verdict.

   There are **two** bounds, because one was not enough:

   | | |
   |---|---|
   | `maxProductStates` (100,000) | the most any single pair may spend |
   | `maxValidationStates` (1,000,000) | the most ONE overlap analysis may spend across every pair |

   The per-pair bound alone bounds a search and not the work: an analysis decides
   `N(N-1)/2 + 40N` pairs, so ~990 searches at 20 contributed phrases, each entitled to the full
   100k — minutes of CPU inside `mentat.Validate` from a bound that looked like it had fixed the
   problem. Bounding the inner loop and leaving the outer one unbounded is the same defect one
   level out, and it shipped in the first version of this bound. Pairs reached after the allowance
   is spent are reported undecidable **without being searched**, which costs nothing and still
   tells the author the truth. For scale: the built-in gate's 780 pairs cost 2589 states in total,
   0.3% of the validation-wide allowance.

   Both are **cost** bounds and not deadlines: neither observes cancellation, and a single
   exhausted pair costs ~250ms of CPU.
5. **It never panics** on author input. Contributed patterns reach it.

## What it does NOT guarantee — read this before relying on a negative

**`Intersects == false` is not a proof of disjointness. It is this code reporting that it found no
shared string.**

That distinction is the entire subject of this feature, and stating it anywhere else would
reproduce the defect 013 was raised to fix. The negative direction is trusted only to the extent
US3's verification makes it trustworthy:

- a known-verdict corpus of pattern pairs, positive and negative;
- differential sampling against pairs it called disjoint;
- agreement with the generated-sentence corpus wherever that corpus holds a two-pattern sentence;
- mutation rehearsals, each confirmed to have landed before its red is trusted.

**Worked precedent, not a hypothetical**: the prototype behind this contract decided all 780
built-in pairs correctly and reported `^(?i)abc$` and `^abc$` as **disjoint** when both match
`"abc"`. It was found by the corpus method above, not by reading the code. See R3.

## Callers

| Caller | Pairs decided | On overlap |
|---|---|---|
| Built-in gate (test, `make ci`) | all pairs over `stepDefs` — 780 at 40 patterns | **FAIL the build**, naming both patterns and the witness (FR-013) |
| `patternOverlapFindings`, unexported, called inside `EngineStepChecks` (`internal/steps/phrase.go`) and reaching `mentat.Validate` as a return value | only pairs touching ≥1 **contributed** pattern (R6) | emit one `pattern-overlap` finding per intersecting pair, one `pattern-undecidable` per refused pattern, and one per pair refused at the state budget; **`mentat.Validate` still returns a nil error** (FR-014, FR-018, D5) |

**Not a caller: the scenario-init fail-fast path** (FR-017). This is structural, not disciplinary —
the pattern half of the check set has no scenario-init counterpart, because godog owns runtime
matching and `registerSteps` hands it patterns directly. See R6.

**And "structural" has to be earned, not asserted.** An earlier version of this paragraph said an
*exported* `PatternOverlapFindings` would be callable from scenario init (`InitializerWithBudget`,
`internal/steps/steps.go`), so unexporting it was
what made FR-017 structural. **That is wrong**: scenario init is in the same package, so an
unexported helper is equally callable from there. Unexporting only stops other packages reaching
past `EngineStepChecks`.

The guarantee is the call graph: `EngineStepChecks` has one non-test caller (`run.go`), the helper
has one caller (`EngineStepChecks`), and scenario init calls `resolvePhrases` directly. Keep the
helper unexported as defence in depth; do not call that the structural property.

**Not a caller: the `mentat validate` binary.** It builds no engine, so it cannot see contributed
phrases (012's D7), and built-in × built-in is the CI gate's job. Unchanged by this feature.

## Asymmetry, and why it is not an inconsistency

Built-in overlap fails the build; contributed overlap is reported. The rule is **ownership**
(D5): `stepDefs` is ours and an overlapping pair there is a design bug we can simply not ship.
Consumers' comparators are not ours, and overlap between two of them is a *potential* failure —
it becomes real only for a sentence inside the overlap, which still fails loudly at run time via
godog's `Strict` plus 012's per-sentence `ambiguous-step`. Forbidding the potential would make two
independently-authored comparators mutually unusable for a consumer whose feature files never
enter the overlap.

The same reasoning is why the run path is untouched (FR-017): a rule about how strict to be
applies to every gate, and naming only the gate in front of you is how an asymmetry ships by
accident.


## Refusals reaching a consumer

A refusal is an error from `Intersects`, but it must NOT become an error from
`mentat.Validate`. A contributed phrase containing `\b`, `\B` or a `(?m)` anchor is legal — V2
admits it and godog runs it — so `patternOverlapFindings` converts a refusal into a
`pattern-undecidable` finding and carries on with the rest of the set (FR-018).

Propagating it instead made the validator refuse a suite the runner executes: the mirror image of
the drift D7 was created to remove, and a regression against 012 behaviour. Reported once per
pattern, not once per pair.

For the **built-in** set the opposite applies: `decidedCollisions` treats a refusal as `t.Fatalf`,
because an undecidable built-in is a defect in a table we own and ship.
