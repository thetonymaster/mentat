# Research: Built-in Step-Pattern Disjointness

**Feature**: 013-builtin-pattern-disjointness | **Date**: 2026-09-11

Everything below was **measured** against Go 1.25's `regexp/syntax` and the current branch. Three
findings correct claims someone had previously asserted without testing — two of them in this
feature's own spec, and one in this feature's own prototype. The prototype one (R3) matters most:
it is the defect class this feature exists to remove, found inside the mechanism built to remove
it.

---

## R1 — The decider is feasible with the standard library. D2's cost estimate was wrong.

**Decision**: Decide emptiness-of-intersection with a lazy product automaton over `regexp/syntax`.

**Rationale**: RE2 has no backreferences or lookaround, so the language class is regular and
intersection-emptiness is decidable. `regexp/syntax` exposes everything needed: `Parse` →
`Simplify` → `Compile` yields a `*syntax.Prog`, an NFA whose instructions carry their own rune
ranges. A search state is a pair of NFA program-counter sets plus whether we are at text start;
transitions are taken over **equivalence classes of runes** derived from the union of both
programs' rune ranges, which makes the alphabet finite and small without enumerating Unicode. BFS
terminates because reachable state pairs are finite.

**Measured** (stdlib-only prototype, ~250 lines, 2026-09-11):

| | |
|---|---|
| pairs decided | **780** (all 40 built-ins, pairwise) |
| intersecting pairs | **0** |
| max product states for any pair | 74 |
| total product states | 2589 |
| wall clock incl. compilation | **0.36s** |
| third-party dependencies | **0** |

**Alternatives considered**: (a) A DFA library — rejected, D2's no-dependency rule stands and
nothing needs it. (b) Widening the generated-sentence filler set — rejected, more samples never
become a decision (D4). (c) Leaving the property sampled and removing only the reliance — this was
the original D1, and it is wrong for the reason recorded next.

**What this corrects**: D2 said the route "would mean a DFA library or hand-rolled automata.
Neither is justified." The cost was overestimated by enough to change the feature's scope.

---

## R2 — D1's argument against proving the property does not survive the word "gate".

**Decision**: Ship both halves — remove the reliance (US1) *and* decide the property (US2).

**Rationale**: D1 argued *"a proof is a snapshot of 40 patterns; the 41st reopens it."* True of a
hand proof; false of a decision procedure wired as a gate, which decides whatever set exists on
every build. The 41st pattern is decided the day it is added. D1 conflated an artifact with a
procedure, and that conflation is the only reason the route sat in *Out of Scope*.

The two halves are independent rather than alternative: US1 would still be required if the set
were disjoint forever (Constitution IV constrains the code path, not the property), and US2 would
still be worth having if no code relied on it (an overlapping built-in pair is a grammar design
bug worth a red build).

**Alternatives considered**: Keeping US2 optional — rejected; deferring the half that closes the
gap is the pattern that produced 013 out of 012.

---

## R3 — `FoldCase` is the one alphabet `syntax` does not materialize, and the prototype got it wrong

**Decision**: The decider MUST honour `FoldCase` by expanding through `unicode.SimpleFold`. Case
folding is **not** a refusal case.

**Measured**:

| pattern | compiled instructions |
|---|---|
| `(?i)abc` | `InstRune Rune=['A'] Arg=1`, `Rune=['B']`, `Rune=['C']` — **single rune + FoldCase in Arg** |
| `(?i)[a-c]` | `InstRune Rune=['A' 'C' 'a' 'c'] Arg=0` — **materialized** |
| `abc` | `InstRune1 Rune=['a'] Arg=0` |

So a character class folds into explicit ranges, but a **literal** does not: it compiles to a
single-rune instruction whose fold flag lives in `Inst.Arg`. A reader of `Inst.Rune` alone sees
only `'A'` and concludes the program cannot match `'a'`. `Inst.MatchRunePos` is the authority —
it consults `Flags(i.Arg)&FoldCase` for exactly the `len(Rune)==1` case.

**The prototype had this defect.** It reported `^(?i)abc$` and `^abc$` as **disjoint** when ground
truth is that both match `"abc"`:

```
WRONG  FOLD single-rune literal   want=true  got=false  states=1  witness=""
```

After mirroring `MatchRunePos` in a 10-line `foldRanges` helper, all 8 self-tests pass and the
780-pair result is unchanged — built-ins use no folding, which is exactly why this would have
shipped unnoticed and fired on the first contributed pattern containing `(?i)`.

**Why this is the load-bearing finding**: the prototype decided 780 pairs correctly and was wrong
about a construct nobody had tested it on. That is *this feature's own failure mode* — a confident
claim resting on an unexamined mechanism — reproduced inside the gate built to remove it, and
caught by the method US3 prescribes (a corpus of pattern pairs with known verdicts) before any of
it shipped. **US3 is not optional, and R3 is the evidence.**

**Alternatives considered**: Refusing folded patterns outright — rejected; correct handling is ten
lines, and refusing a construct we can model would push authors toward working around the gate.

**What this corrects**: the spec's D2a and US2 acceptance scenario 5 both listed case folding as an
unmodelled construct. Both amended 2026-09-11.

---

## R4 — The refusal set is exactly four empty-width assertions

**Decision**: Refuse `EmptyWordBoundary`, `EmptyNoWordBoundary`, `EmptyBeginLine`, `EmptyEndLine`
with a descriptive error naming the pattern and the construct. Model everything else.

**Measured**: `\babc\b` compiles to `InstEmptyWidth` carrying word-boundary flags; `(?m)^a$`
carries `BeginLine`/`EndLine`. Both are position predicates the product automaton does not track,
so a "disjoint" verdict for them would be unfounded. `BeginText`/`EndText` **are** modelled —
satisfiable only at position 0 and at end-of-input respectively, which is why the closure is
computed twice per state (once with `atEnd=false` for stepping, once with `atEnd=true` for
acceptance).

**Construct census across all 40 built-in patterns** (after `Simplify()`): `Literal` 155,
`Capture` 52, `Concat` 52, `CharClass` 48, `BeginText` 40, `EndText` 40, `Plus` 26, `Star` 22,
`Alternate` 17, `Quest` 10. **Zero** word boundaries, multi-line anchors, `Repeat`, `AnyChar`, or
folding; **40/40** anchored at both ends. The built-ins therefore never reach the refusal path —
but contributed patterns are author input, so it is reachable through them.

**Alternatives considered**: Modelling word boundaries by extending the state with "previous rune
was a word character" — deferred, not rejected; it is a real extension but nothing needs it, and
an unused generalisation fails `/composition`'s second-implementation test.

---

## R5 — The decider lives in `internal/steps`, not a new package

**Decision**: New file `internal/steps/disjoint.go`. No new package.

**Rationale**: `internal/steps` is the only consumer — both the CI gate test and the Validate-time
check are there. A leaf package would be more composable in principle, but there is no second
consumer now or in a committed plan, which is the bar `/composition` sets. The decider is pure
(pattern strings in, verdict + witness out) so it is testable without any of `steps`' other
machinery regardless of where it sits.

**Layering fact that constrains this**: `internal/steps` imports `internal/engine` (`phrase.go:12`,
`precheck.go:12`, `steps.go:18`); `internal/engine` imports `internal/steps` nowhere, and cannot
without a cycle. This is why FR-014 places the contributed check at composition inside
`internal/steps` rather than at `engine.Build` — the same constraint 012 recorded.

---

## R6 — FR-017 is satisfied by construction, not by discipline

**Decision**: Produce the pattern-level overlap findings in `mentat.Validate` (`run.go:573`),
immediately after `steps.EngineStepChecks(eng)`, folded in with the existing pre-walk findings.

**Rationale**: This is the strongest finding for FR-017 ("Validate-only; the run path is
untouched"). The pattern half of the check set **already has no scenario-init counterpart**. From
`phrase.go`'s own comment on `EngineStepChecks`:

> The PATTERN half has no scenario-init counterpart to drift from. godog owns matching at runtime:
> `registerSteps` hands it the patterns directly (`steps.go:128`) and no `StepPatterns` set is
> built there at all. So the shared root of the guarantee is `resolvePhrases`, not
> `stepPatternsFor` — whose only caller is this function.

So a check placed beside `stepPatternsFor` cannot reach scenario init even by accident. FR-017 is
a property of the location, not a rule someone has to remember. `cmd/mentat/validate.go` already
establishes the folding-in idiom (`pre` findings gathered before the walk, then
`DedupeSortFindings`).

**Scope decision**: at Validate time, decide only pairs involving **at least one contributed**
pattern. Built-in × built-in is the CI gate's job (FR-013), and reporting a built-in-only overlap
to a consumer would name a defect they cannot fix. For a consumer contributing *k* phrases this is
`k(k-1)/2 + 40k` pairs — 210 for k=5, tens of milliseconds.

**Alternatives considered**: Producing the finding inside `SuiteCheck.Paths` — rejected, that runs
per feature file and would emit one finding per file for a single pattern-pair defect.

---

## R7 — `make ci` runs a fuzz target's seed corpus but does no fuzzing

**Decision**: Two artifacts. A deterministic table-driven test carrying the known-verdict corpus
and the differential cases (runs in `make ci`), plus a `Fuzz…` target for longer exploration on
demand. The fuzz target's **seed corpus** is what `make ci` exercises.

**Measured**: `make ci` is `lint test cover example` (`Makefile:17`). `go test ./...` runs `Fuzz`
functions as ordinary tests over their seed corpus only; fuzzing requires an explicit
`-fuzz=` flag and time bound.

**Consequence for FR-015**: the differential check must not *depend* on fuzzing to have run, or the
gate is green in CI while never having sampled anything. The deterministic table is the gate; the
fuzz target is the extension.

---

## R8 — Finding class key: `pattern-overlap`

**Decision**: New class key `pattern-overlap`, with `File: ""` and `Line: 0`.

**Rationale**: existing keys are `bad-cel`, `unknown-target`, `unbound-step`, `ambiguous-step`,
`step-argument`, `unknown-shape`, `bad-runs-tag`. A new key is right rather than reusing
`ambiguous-step`, because the two answer different questions: `ambiguous-step` is **per sentence**
and locates a feature-file line; `pattern-overlap` is **per pattern pair** and has no feature-file
location at all. Conflating them would make a pattern-level defect look like a line-level one and
would break the contract that an `ambiguous-step` finding points at a step someone wrote.

`Source` already tolerates zero `File`/`Line` — scenario-init leaves it zero by design
(`precheck.go:30-38`).

**Contract impact**: `specs/012-.../contracts/validate-surface.md` enumerates the classes, and the
stdout goldens are `//go:build e2e`, so `make ci` will not notice their churn. **Run
`go test -tags e2e` with the harness up.** This repo has been bitten by exactly that gap before.

---

## Open items deliberately left to implementation

- **Message wording** for `pattern-overlap`. It must name both patterns, the contributing
  comparator where there is one, and the witness string — enough that an author can act without
  opening the spec. Exact text is a TDD decision.
- **Whether the CI gate also asserts the prototype's cost figures.** FR-016 requires the cost
  measured and recorded; whether that becomes an asserted ceiling (brittle on slow CI) or a
  reported number is a plan-time call. Recommend reporting, not asserting.
