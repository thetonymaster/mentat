# Contract: the intersection decider

**Feature**: 013 | **Status**: proposed | **Scope**: `internal/` only — publishes nothing on the facade

## Surface

```go
// Intersects decides whether any string matches both patterns.
func Intersects(a, b string) (Verdict, error)
```

Internal to `internal/steps` (R5). Not aliased on the facade, not a comparator seam, not a
`core` interface. If the public-surface golden changes when this lands, something is wrong —
stop rather than update the golden (010 D5: only terminal types may be facade-declared).

## What it guarantees

1. **It decides.** For a pattern pair it accepts, the answer is over the whole language of each
   pattern, not over any corpus of example sentences. No filler set, no sampling, no sentence
   generation.
2. **A positive verdict proves itself.** `Intersects == true` carries a witness string, verified by
   `MatchString` against both compiled patterns before it is reported (FR-011).
3. **It refuses what it cannot model**, with an error naming the pattern and the construct — never
   a "disjoint" verdict (FR-012). The refusal set is the four empty-width assertions in
   `data-model.md` §3, **measured** (R4), plus any `EmptyOp` bit it does not recognise.
4. **It terminates.** Reachable product states are finite and deduplicated. Measured over the
   built-in set: max 74 states per pair.
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
| `mentat.Validate` (`run.go`, after `EngineStepChecks`) | only pairs touching ≥1 **contributed** pattern (R6) | emit one `pattern-overlap` finding per pair; **composition still succeeds** (FR-014, D5) |

**Not a caller: the scenario-init fail-fast path** (FR-017). This is structural, not disciplinary —
the pattern half of the check set has no scenario-init counterpart, because godog owns runtime
matching and `registerSteps` hands it patterns directly. See R6.

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
