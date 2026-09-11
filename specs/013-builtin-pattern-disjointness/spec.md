# Feature Specification: Built-in Step-Pattern Disjointness

**Feature Branch**: `013-builtin-pattern-disjointness`

**Created**: 2026-09-11

**Status**: Draft

**Input**: Raised by 012's convergence. The `ambiguous-step` class 012 added is unreachable from
the `mentat validate` binary, and three records now say so as **measured, not structural**. The
measurement substitutes nine fixed fillers into capture groups; two built-ins colliding only on a
string no filler produces would pass it. Close the gap, or stop depending on it.

---

## Context

`TestBuiltinStepPatternsArePairwiseDisjoint` (`internal/steps/metadata_test.go:286`) parses each
built-in pattern's syntax tree, expands **every** alternation branch, and substitutes a filler for
anything variable. Measured on 2026-09-11:

| | |
|---|---|
| built-in patterns | 40 |
| fillers | 9 — `"x"`, `""`, `"a b"`, `"1"`, `"2nd"`, `"true"`, `"0.5"`, `"tool-name"`, `"a/b.c"` |
| unique sentences generated | 1530 |
| max expansions per pattern×filler | 54 |
| expansions hitting the 4000 truncation | 0 |

The last two rows matter, and they narrow the problem rather than widen it. `expandPattern` has
three ways to miss a sentence — the filler set, a depth cap at 12, and a 4000-combination
truncation in `OpConcat` — but only the **first** is live today. Nothing is deep enough to hit the
depth cap and nothing expands widely enough to be truncated. Saying "the generator has three blind
spots" would overstate the gap in the opposite direction from the overclaim this feature exists to
correct.

So the live limit is precise: **every variable construct contributes exactly one string per run,
drawn from nine.** `(\d+)`, `([^"]*)` and `.` each become the filler and nothing else. Two patterns
that overlap only on a string outside that set pass the test.

### What rests on the property

Before this feature, "the 40 built-in patterns are pairwise disjoint" is **evidence, not proof**,
and four claims lean on it:

1. **V4's rationale** — rejecting a contributed pattern identical to a built-in's is only
   meaningful if the built-in set is itself unambiguous.
2. **R10's "latent, not live" finding** — that godog's ambiguity branch is unreachable through the
   public surface at `0f9dcea`.
3. **`ambiguous-step` is unreachable from the `mentat validate` binary** — stated at
   `specs/012-.../contracts/validate-surface.md:150` and at `CLAUDE.md:322` and `CLAUDE.md:333`.
   (An earlier draft of this list named `CHANGELOG.md` and the `StepBindingFindings` doc comment.
   **Both were wrong** — grep finds the hedge in neither; `precheck.go:110`'s only "unreachable" is
   about `MustCompile` invariants. Corrected 2026-09-11 by measurement, which is the third time a
   provenance claim in this spec has had to be checked rather than trusted.)
4. **`StepArguments.matchBuiltin`** (`internal/steps/stepargs.go:310`) returns the **first**
   matching built-in as though it were the only one.

The first three are *statements*. The fourth is different in kind: it **encodes** the assumption in
a running code path. If two built-ins ever match one sentence, `matchBuiltin` silently picks one
and the argument check diagnoses against it — a silent fallback, which Constitution IV forbids.
That asymmetry drives this feature's priority order.

**And the same shape sits one function below it.** `matchPhrase` (`stepargs.go:319`) returns the
first of several matching *contributed* phrases the same way, and V3/V4 (`phrase.go:336-343`) reject
only **identical** pattern strings — so two distinct-but-overlapping contributed phrases coexist
legally and the argument check diagnoses against whichever registered first. Nothing ever claimed
contributed phrases were pairwise disjoint, which makes that half worse than claim 4 rather than
better: it is positional resolution resting on no measurement at all. It is not a fifth thing
resting on the built-in property; it is the same defect, and US1 covers both (see Clarifications).

**How 013 addresses both halves.** US1 removes claim 4 — no code path resolves by position, so
nothing *executable* depends on the property. US2 then decides the property outright, which turns
claims 1–3 from hedged statements into decided ones. The two halves are independent: US1 would
still be required if the set were proved disjoint forever (Constitution IV is about the code path,
not the property), and US2 would still be worth having if no code relied on it (an overlapping
built-in pair is a grammar design bug worth a red build). An earlier draft of D1 treated them as
alternatives and made US2 optional; see Decisions for why that argument fails.

### How the overclaim was caught

During 012's Phase 10 review, a draft of `validate-surface.md` called the gap "structurally
unreachable" in a sentence whose own next clause conceded the test "proves only over generated
sentences". It was corrected to "unreachable **as measured**". The wording is now honest; the
dependency is still there. This feature closes the dependency rather than re-wording it again.

---

## Clarifications

### Session 2026-09-11

- Q: FR-001 forbids resolving among multiple matching **built-in** patterns by position, but
  `matchPhrase` does exactly that among multiple matching **contributed** phrases, one function
  away. What is the requirement's boundary? → A: Source-agnostic — no path resolves among multiple
  matching patterns by position, whatever their source.

  The rationale Out of Scope used to exclude contributed-phrase overlap ("already reported per
  sentence as `ambiguous-step`") is the *same* rationale that would dissolve US1, because once US1
  lands a built-in collision is reported by that same `StepBindingFindings` call. A reason cannot
  be sufficient for one half and irrelevant to the other. Scoping the fix to the half where the
  defect was noticed is also, precisely, the mistake 012 recorded making twice: each fix was
  aimed at the instance rather than the mechanism, and review found the same hole one step over
  both times.

- Q: Does 013 adopt the exact intersection decision route (previously Out of Scope), and what does
  it cover? → A: Yes — asymmetric. A hard gate over the built-in set; contributed patterns decided
  eagerly at `engine.Build` and surfaced as a finding, not a build failure. (The *seam* named in
  the answer was corrected twice — first because `internal/engine` cannot host a findings-producing
  check without an import cycle, then again because "at composition" is ambiguous between three
  functions here and the first correction picked one that sits on the run path. The seam is
  `EngineStepChecks`. The intent — eager, not per-sentence; reported, not fatal — is unchanged
  throughout. See FR-014.)

  Asymmetric **on purpose**: `stepDefs` is ours, so overlap there is a design bug and must redden
  the build. Consumers' comparators are not ours, and overlap between two independently-authored
  phrases is only a *potential* failure — it becomes real when someone writes a sentence inside the
  overlap. Failing `engine.Build` would forbid the potential, making two third-party comparators
  mutually unusable for a consumer whose feature files never enter the overlap.

  Adopting the route also **supersedes** the original US3 rather than deferring it: a decision
  procedure strictly dominates a sampler, so the fuzz machinery is repointed at the *decider* (is
  it correct?) instead of at the claim (is it still holding?). Nothing is postponed by this answer;
  one story is replaced by a stronger one and the sampler becomes that story's verification.

- Q: `internal/steps` prechecks feed both `mentat validate` and the fail-fast scenario-init Before
  hook, which turns the first finding into a scenario-init error. Does the new pattern-level
  overlap finding join that shared pool? → A: No — Validate-only; the run path is untouched.

  Otherwise two overlapping contributed phrases would abort **every scenario in the suite**, making
  overlap fatal to runs — the outcome D5 declined one layer up. D5's reason (overlap is a
  *potential* failure; it becomes real for a sentence inside the overlap) is indifferent to whether
  the gate is the build or the run, so honouring it at one and not the other would be arbitrary.
  The genuinely-failing case is already covered without any new mechanism: a sentence inside the
  overlap fails at run time through godog's `Strict` plus 012's per-sentence `ambiguous-step`.

  This is the **third** time in this spec that one answer's reasoning had to be carried to a place
  the question did not mention — phrases after built-ins, composition after the build, and now the
  run path after composition. Worth recording as a pattern rather than three incidents: a decision
  about *how strict to be* propagates to every gate, and naming only the gate in front of you is
  how the asymmetry gets shipped by accident.

---

## Decisions

- **D1 — Remove the reliance *and* decide the property. Both, not either.**

  An earlier draft of D1 made proving the property optional, on the argument that *"a proof is a
  snapshot of 40 patterns; the 41st reopens it."* **That argument is wrong, and it was the sole
  reason the exact-decision route sat in Out of Scope.** It is true of a hand proof and false of a
  **decision procedure wired as a gate**: a gate decides whatever pattern set exists, on every
  build, so the 41st pattern is decided the day it is added. D1 conflated an artifact with a
  procedure.

  Removing the reliance (US1) is still mandatory and still independently justified — Constitution
  IV forbids positional resolution whether or not the sets happen to be disjoint, and US1 is what
  makes a *deliberately* overlapping future built-in survivable. It is no longer the only
  mandatory half.

- **D2 — Decide intersection with the standard library. Still no third-party dependency.**

  An earlier draft of D2 dismissed this as needing "a DFA library or hand-rolled automata. Neither
  is justified." The cost estimate was wrong, and it was measured wrong rather than argued wrong.
  A lazy product automaton over `regexp/syntax` — the pattern pair's NFAs, stepped over rune
  equivalence classes drawn from both programs — decides emptiness-of-intersection for this
  pattern class. Measured on 2026-09-11 with a ~250-line stdlib-only prototype:

  | | |
  |---|---|
  | pairs decided | **780** (all 40 built-ins, pairwise) |
  | intersecting pairs | **0** |
  | max product states for any pair | 74 |
  | total product states | 2589 |
  | wall clock, including compilation | **0.36s** |
  | third-party dependencies | **0** |

  It is tractable because RE2 has no backreferences or lookaround, so the language class is
  regular, and because the built-in patterns are anchored literals with character classes. The Op
  census is in D2a below, and the one construct that looks handled but is not is R3 in
  `research.md` — found by breaking the prototype, not by reading it.

- **D2a — The construct census, because a decider must refuse what it cannot model.** Measured
  across all 40 built-in patterns after `Simplify()`: `Literal` (155), `Capture` (52), `Concat`
  (52), `CharClass` (48), `BeginText` (40), `EndText` (40), `Plus` (26), `Star` (22), `Alternate`
  (17), `Quest` (10). **Zero** `WordBoundary`, `NoWordBoundary`, `BeginLine`, `EndLine`, `Repeat`,
  `AnyChar`, or case-folding, and **40/40** anchored at both ends. A construct outside the modelled
  set MUST make the decider return a descriptive error, never a quiet "disjoint" — that would be a
  silent fallback inside the very gate built to remove one (Constitution IV). Contributed patterns
  are author input, so this is reachable there even though the built-ins do not reach it.

  **The refusal set is exactly four empty-width assertions** — `\b`, `\B`, and the multi-line `^`
  and `$` — measured, not guessed. **Case folding is not among them: it must be *handled*.** The
  prototype that got all 780 pairs right got this one wrong, reporting `^(?i)abc$` and `^abc$` as
  disjoint when they share `"abc"`. See R3 in `research.md`; it is the reason US3 is not optional.

- **D3 — The generated-sentence test stays, now as an independent cross-check.** Never delete it.
  Once the decider lands it is no longer the load-bearing evidence, which *raises* its value rather
  than lowering it: two mechanisms of different kinds checking one property means a disagreement
  between them proves one is broken. Its `generated < 500` sanity floor stays too — a generator
  that silently produced nothing would report success forever.

- **D4 — Sampling is repointed at the decider, never at the claim.** The original D4 said fuzzing
  is "additive evidence, never proof", and that remains true of any sampler aimed at *disjointness*
  — which is exactly why the claim is no longer sampled. A decider's **positive** verdicts prove
  themselves (it emits a witness string, re-verified against both patterns), but its **negative**
  verdicts rest on the decider being correct — a buggy decider reports "decided disjoint" and is
  wrong, which is this feature's own failure mode one level up. So the sampler's job becomes
  falsifying the decider: sample strings against pairs it called disjoint, and any string matching
  both means the decider is wrong. That is a verification deliverable, not a relabelled claim.

- **D5 — Enforcement is asymmetric by ownership.** Built-in overlap fails the build; contributed
  overlap is reported. We own `stepDefs` and can simply not ship an overlapping pair, so overlap
  there is a design bug. We do not own consumers' comparators, and overlap between two of them is a
  *potential* failure that becomes real only for a sentence inside the overlap — so it is surfaced
  where the author can act on it, without forbidding a working setup. See Clarifications.

  **The load-bearing half of that argument is already pinned, so cite it rather than assert it:**
  `TestGenuinelyOverlappingPhrasesFailLoudly` (`custom_phrase_isolation_test.go:302`, 012's
  T038/SC-011) is the existing proof that a sentence inside the overlap fails loudly at run time.
  Its `overlapComparator` fixture (`:270`) is also exactly what this feature's contributed-overlap
  tests need. D5 asserted this in four places and cited it in none — which, in a feature about
  claims resting on unexamined evidence, was worth fixing.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - No code path silently assumes a step matches one definition (Priority: P1)

A maintainer adds a 41st built-in step whose pattern happens to overlap an existing one — or a
consumer contributes a second phrase that overlaps one their own comparator already contributes.
Today the argument check would quietly diagnose the author's step against whichever pattern
registered first, producing a message about the wrong step definition — or no message where one was
due. After this story, that situation is reported, not guessed at, and it does not matter which
source the colliding patterns came from.

**Why this priority**: This is the only place the assumption is *executable* rather than written
down, and a wrong first-match produces a misleading error aimed at the wrong step — the failure
mode Constitution IV exists to prevent. It is also the smallest change of the four, and it is
**independent** of the others rather than a substitute for them: it would still be required if the
pattern set were decided disjoint forever, because Constitution IV constrains the code path and not
the property.

The story covers **multiplicity within either source**, not just within the built-in set, because
the defect is one shape written twice: `matchBuiltin` and `matchPhrase` each return their first
match as though it were their only one. Fixing the built-in half alone would leave the contributed
half one function away — the scoping mistake 012 made twice (see Clarifications).

**Independent Test**: Register two deliberately colliding rows in a test fixture — once as two
built-ins, once as two contributed phrases — drive a sentence both match, and assert the argument
check defers instead of diagnosing against the first. Delivers the Constitution IV guarantee on its
own, with no other story implemented.

**Acceptance Scenarios**:

1. **Given** two built-in patterns that both match one sentence, **When** the step-argument check
   runs, **Then** it defers that step rather than diagnosing it against the first match — the same
   deferral it already performs when a built-in and a contributed phrase both match, and for the
   same reason: under `Strict` neither definition binds, so any message naming one of them is
   false as well as misdirecting.
2. **Given** two contributed phrases that both match one sentence and no built-in matches it,
   **When** the step-argument check runs, **Then** it defers that step for the identical reason —
   the deferral is a property of how many definitions match, never of which source they came from.
3. **Given** either of those suites, **When** it is validated, **Then** exactly one
   `ambiguous-step` finding is reported, naming every matching pattern.
4. **Given** exactly one pattern matches, from either source, **When** the check runs, **Then** its
   behaviour is byte-identical to today's — this story must not change the single-match path.

---

### User Story 2 - Disjointness is decided, not sampled (Priority: P1)

A maintainer adds the 41st built-in step. If its pattern can match any string an existing built-in
also matches, the build goes red and names a witness string that both match — whether or not any
feature file in the world contains that sentence. A consumer whose two comparators contribute
overlapping phrases is told so when the engine is built, naming the same kind of witness, and gets
a working engine anyway.

**Why this priority**: This is what closes the gap the feature was raised for, and after D1's
correction it is no longer the optional half. Nine fixed fillers is a sample; a decision procedure
over the pattern pair is an answer, and it costs 0.36s (D2). Without it, every record still has to
hedge and the 41st pattern is still checked by substitution.

**Independent Test**: Run the gate over the current built-in set and confirm it decides all 780
pairs disjoint; add a deliberately overlapping built-in row and confirm the gate reddens with a
witness. Needs no other story.

**Acceptance Scenarios**:

1. **Given** the built-in pattern set, **When** the gate runs, **Then** it decides every pair and
   passes only if no pair's intersection is non-empty — no sentence corpus is consulted.
2. **Given** two built-in patterns whose languages intersect, **When** the gate runs, **Then** it
   fails, names both patterns, and emits a **witness string** that both match.
3. **Given** a witness is emitted, **When** it is reported, **Then** it has been re-verified
   against both patterns, so a positive verdict proves itself rather than asserting itself.
4. **Given** two contributed phrases whose languages intersect, **When** the suite is **validated**
   (`mentat.Validate` → `EngineStepChecks`), **Then** that call **succeeds** and a finding reports
   the overlap with its witness (D5) — the asymmetry is deliberate and ownership-based, not an
   oversight.
5. **Given** a pattern using a construct the decider does not model — a word boundary, a
   multi-line anchor — **When** it is decided, **Then** the decider returns a descriptive error
   naming the pattern and the construct, and **never** reports "disjoint" (D2a).
6. **Given** two overlapping contributed phrases, **When** the suite is **run** rather than
   validated, **Then** every scenario whose steps fall outside the overlap passes exactly as
   before — the pattern-level finding never aborts scenario init (FR-017).

---

### User Story 3 - The decider is falsifiable (Priority: P2)

A contributor can break the decider and see a test go red, rather than trusting a gate whose green
means "my code found nothing".

**Why this priority**: A decider's negative verdicts are exactly as trustworthy as the decider, and
a gate that wrongly reports "decided disjoint" reproduces this feature's own defect one level up —
a confident claim resting on an unexamined mechanism. Shipping US2 without US3 would be the same
mistake in a new place, which is the one outcome this feature cannot afford.

**Independent Test**: Mutate the decider (drop a rune class from the alphabet partition; treat an
unmodelled construct as disjoint) and confirm at least one test reddens for each mutation, with
each mutation confirmed to have landed first.

**Acceptance Scenarios**:

1. **Given** a corpus of pattern pairs with known verdicts — intersecting and disjoint, including
   the exact-vs-general case from Edge Cases — **When** the decider runs, **Then** every verdict
   matches and every positive verdict's witness is verified against both patterns.
2. **Given** a pair the decider called **disjoint**, **When** strings are sampled against it,
   **Then** no sampled string matches both — and any that does is reported as a decider defect,
   not as a disjointness defect (D4).
3. **Given** the existing generated-sentence corpus, **When** any sentence in it matches two
   patterns, **Then** the decider reports that pair as intersecting — the two mechanisms must
   agree, and a disagreement means one is broken (D3).
4. **Given** a mutation is introduced into the decider, **When** the suite runs, **Then** it
   reddens, and the rehearsal record states that the mutation was confirmed to have landed — "the
   mutation didn't fire" and "the guard is real" are indistinguishable from test output alone.

---

### User Story 4 - Every record states what is decided and what is not (Priority: P3)

A contributor reads any place the disjointness property is asserted and comes away knowing whether
it was **decided** or **measured**, and for the measured parts, measured how.

**Why this priority**: Lowest not because it is optional but because it describes the outcome of
US1 and US2, so it cannot be written accurately before they land. Doing it first would produce a
third round of wording-corrections to a claim whose status was still changing.

**Independent Test**: Read each site and confirm it names its own basis. Verifiable without running
anything.

**Acceptance Scenarios**:

1. **Given** any document or comment asserting built-in disjointness, **When** a reader reaches the
   claim, **Then** it says the property is **decided** by the gate, not that it was sampled — the
   nine-filler hedge is removed because it is no longer what the claim rests on.
2. **Given** a record of the generated-sentence test, **When** a reader reaches it, **Then** it is
   described as an independent cross-check of the decider (D3), not as the evidence for
   disjointness.
3. **Given** US1 has landed, **When** a reader reaches claim 4 from Context, **Then** it no longer
   exists as a dependency, because no code path relies on the property.
4. **Given** the `ambiguous-step` reachability claim from Context, **When** a reader reaches it,
   **Then** it states the post-013 position: reachable through contributed phrases, and for
   built-ins gated at build time rather than asserted unreachable.

---

### Edge Cases

- **A collision reachable only via a character the fillers omit** — a quote, a newline, a leading
  or trailing space, a Unicode letter. This *was* the live gap, and **US2 closes it outright**: a
  decider does not sample characters, so a collision reachable only through an exotic string is
  decided like any other. It is also why the original US3 — widen the filler set — was superseded
  rather than delivered: more fillers would have narrowed this gap without ever closing it.
- **A collision that is exact-vs-general rather than partial** — e.g. a future
  `^the result contains "revenue"$` against the existing `^the result contains "([^"]*)"$`. Both
  anchored, both well-formed, and the generated sentence for the general one would only collide if
  a filler happened to be `revenue`.
- **The latent generator caps becoming live.** The depth cap (12) and the `OpConcat` truncation
  (4000) are unreached today — measured: max 54 expansions, 0 truncations. A future built-in with
  deep nesting or many alternations could reach them, and the test would then quietly cover less
  while still passing. Any widening of the pattern set should re-measure rather than assume.
- **Two built-ins that are genuinely, intentionally overlapping.** Nothing in this feature assumes
  disjointness is *desirable* — only that reliance on it must be explicit. If a future design wants
  overlapping built-ins, US1 is what makes that survivable.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: No production code path may resolve among multiple matching step patterns by
  **position** — whether those patterns are two built-ins, two contributed phrases, or one of each.
  Each such path MUST either handle every match or defer to the ambiguity report.
- **FR-002**: The step-argument check MUST defer any step matched by more than one pattern, for
  every combination of sources, exactly as it already defers a step matched by both a built-in and
  a contributed phrase. It MUST reach that decision from the **match count**, not from a
  per-source case, so that a third pattern source added later cannot reintroduce the positional
  path by omission. The comment explaining the deferral MUST give the one reason that covers all
  of them (under `Strict` neither definition binds).
- **FR-003**: The single-match path of the step-argument check MUST be unchanged for either
  source — this feature adds a branch, it does not alter existing diagnosis.
- **FR-004**: Every recorded assertion of built-in disjointness MUST state its **basis** — decided
  by the intersection gate (FR-010, FR-013). Any claim that remains sampled rather than decided
  MUST name its method and its limit rather than asserting the property bare.
- **FR-005**: `TestBuiltinStepPatternsArePairwiseDisjoint` MUST remain, including its
  `generated < 500` sanity floor, and MUST keep failing loudly on any sentence matching two
  built-ins. Its recorded role becomes an independent cross-check of the decider (D3), not the
  evidence for disjointness.
- **FR-006**: *Folded into FR-013 — it stated the same obligation.* Its surviving clause is kept
  there as rationale: neither documentation nor a sentence-corpus check alone satisfies the gate
  requirement, because a corpus can only miss what it does not contain. Retained as a numbered
  stub so FR numbering stays stable across the spec's own cross-references.
- **FR-007**: Any new guard added by this feature MUST be rehearsed against a deliberately
  introduced collision, and the rehearsal — including confirmation that the mutation actually
  landed — MUST be recorded beside the guard.
- **FR-008**: This feature MUST NOT add a third-party dependency (D2).
- **FR-009**: Every touched package MUST stay at or above the 80% coverage floor.
- **FR-010**: The project MUST provide a procedure that decides whether two step patterns'
  languages intersect, implemented with the standard library only.
- **FR-011**: A positive verdict MUST carry a **witness string** that both patterns match, and the
  witness MUST be re-verified against both before it is reported — a positive verdict proves
  itself.
- **FR-012**: A pattern using a construct the decider does not model MUST produce a descriptive
  error naming the pattern and the construct. Reporting "disjoint" for such a pattern is
  PROHIBITED: it is the silent fallback the gate exists to remove (D2a, Constitution IV).
- **FR-013**: The built-in pattern set MUST be decided pairwise by an automated gate, which fails
  on any non-empty intersection and names both patterns and the witness. A breach introduced by a
  future built-in MUST redden this gate. **Neither documentation nor a sentence-corpus check alone
  satisfies it** — a corpus can only miss what it does not contain (absorbed from FR-006).
- **FR-014**: Contributed patterns MUST be decided in **`EngineStepChecks`**
  (`internal/steps/phrase.go`), beside `stepPatternsFor`, and an overlap — with a built-in or with
  another contributed phrase — MUST be surfaced as a finding carrying its witness. That path MUST
  still succeed (D5); rejecting contributed overlap outright is PROHIBITED.

  **Named as a function, not as "at composition", because "composition" denotes three different
  points in this repo and two of them are wrong.** An earlier draft of this FR said
  `resolvePhrases`, which has **three** callers — `steps.go:101` (**scenario init, the run path**),
  `phrase.go:518` (`EngineStepDocs`) and `phrase.go:611` (`EngineStepChecks`). Implementing it
  there would put a findings path on the run path, which FR-017 prohibits. `stepPatternsFor`
  (`phrase.go:565`) has exactly **one** caller, inside `EngineStepChecks` — which is why FR-017 is
  satisfied by location rather than by discipline (research.md R6).

  **And the helper MUST be unexported, with the findings returned from `EngineStepChecks`.** An
  exported `PatternOverlapFindings` in `internal/steps` would be callable from `steps.go:101` —
  so FR-017 would rest on "`run.go` happens to be its only caller today", which is discipline
  wearing the word structural. Unexported, invoked from inside the one function scenario init does
  not call, and surfaced to `mentat.Validate` as a return value: then the run path cannot reach
  these findings even by a future mistake, which is the whole claim R6 makes.

  Not at `engine.Build` either: `internal/steps` imports `internal/engine`, so the reverse is a
  cycle and a findings-producing check cannot live in the engine. Same constraint 012 recorded when
  it placed phrase validation in `internal/steps` rather than at the composition root.
- **FR-015**: The decider MUST itself be verified by (a) a corpus of pattern pairs with known
  verdicts, (b) differential sampling against pairs it called disjoint, and (c) agreement with the
  generated-sentence corpus wherever that corpus contains a two-pattern sentence. A disagreement
  MUST be reported as a decider defect (D4).
- **FR-016**: The gate's cost over the built-in set MUST be measured and recorded, not assumed.
- **FR-017**: The pattern-level overlap finding MUST NOT join the scenario-init fail-fast pool. It
  is reported by `mentat.Validate` only; the run path's behaviour on overlap is unchanged, staying
  godog's `Strict` ambiguity plus 012's per-sentence `ambiguous-step`. A scenario-init abort for
  pattern-level overlap is PROHIBITED — it would make overlap fatal to runs, which is what D5
  declined for builds and for the same reason.

### Key Entities

- **Built-in pattern set**: the 40 `stepDefs` patterns, compiled in table order. Registration order
  decides which definition godog binds when two match, which is why position-based resolution is
  the defect and not merely a style issue.
- **Contributed phrase set**: whatever phrases the resolved comparators contribute, compiled after
  the built-ins. Pattern validation rejects only *identical* strings, so this set carries no
  disjointness claim whatsoever — which is why FR-001 is stated over match count rather than over
  either set.
- **Generated sentence corpus**: the sentences `expandPattern` derives from the pattern set for a
  given filler. Once US2 lands it stops being the evidence for disjointness and becomes an
  independent cross-check of the decider (D3) — which is a promotion, not a demotion: two
  mechanisms of different kinds mean a disagreement proves one is broken. Its size and limits stay
  properties to measure, not to infer from the test's name.
- **Intersection decider**: the procedure that answers "can any string match both of these
  patterns?" over a pattern pair, by lazy product automaton on the two compiled NFAs. Decides, so
  it needs no corpus; refuses constructs it cannot model, so it never guesses; emits a witness on a
  positive verdict, so a "yes" proves itself while a "no" is only as good as the decider — which is
  what US3 exists to keep honest.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Given one sentence matched by two patterns — measured for **both** combinations, two
  built-ins and two contributed phrases — the step-argument check produces **zero** argument
  diagnoses for that step and the suite reports exactly **one** `ambiguous-step` finding naming
  every matching pattern.
- **SC-002**: Removing the deferral added by US1 reddens at least one test, measured separately for
  the built-in and the contributed combination — verified by mutation, with each mutation confirmed
  to have landed before its red is trusted. A count-based deferral (FR-002) is one edit to remove,
  so the two reds may come from the same mutation only if that mutation is shown to reach both.
- **SC-003**: Introducing a deliberately overlapping built-in row reddens the decider gate, with a
  witness string verified against both patterns — and does so whether or not any feature file
  contains a sentence in the overlap (FR-006, FR-013).
- **SC-004**: Every site asserting disjointness states that it is **decided**; no site still cites
  the nine fillers as the basis for the claim. A reader can state the basis without opening a test.
- **SC-005**: Every existing suite produces byte-identical findings and verdicts before and after
  this feature — the single-match path is untouched (FR-003).
- **SC-006**: No new module dependency appears in `go.mod`.
- **SC-007**: The gate decides **every pair** over the built-in set — 780 pairs at 40 patterns —
  reports zero intersecting, and records the pair count and wall clock as measured (FR-016).
  Prototype baseline, 2026-09-11: 780 pairs, 0 intersecting, max 74 product states, 0.36s.
- **SC-008**: Each mutation of the decider named in US3's Independent Test reddens at least one
  test, with each mutation confirmed to have landed before its red is trusted.
- **SC-009**: Given two overlapping contributed phrases, **`mentat.Validate` returns a nil error**
  and exactly one `pattern-overlap` finding with a verified witness, positioned inside the sorted
  finding list (FR-014).

  Not "composition succeeds": FR-014 declared that word ambiguous between three functions, and
  worse, the check runs at Validate time — so `engine.Build` never sees the overlap and
  "composition succeeds" would be **vacuously true**, measuring nothing. A success criterion that
  cannot fail is the defect this feature exists to remove.
- **SC-010**: No pattern containing an unmodelled construct is ever reported disjoint; each yields
  an error naming the pattern and the construct (FR-012).
- **SC-011**: With two overlapping contributed phrases registered, a suite whose steps fall outside
  the overlap produces byte-identical run output to the same suite without them — the pattern-level
  finding is Validate-only (FR-017).

---

## Out of Scope

- **~~Exact decision of regex intersection.~~** **No longer out of scope — it is US2.** The
  original entry declined it because Go's stdlib "exposes nothing for it" and because D1 made the
  property non-load-bearing. Both premises failed: `regexp/syntax` exposes the compiled NFA a
  product automaton needs, and D1 conflated a proof with a gate. The entry is struck rather than
  deleted, because a reader who finds the old roadmap line deserves to see that it was reversed by
  measurement rather than quietly dropped. Its one surviving clause still holds: this is a **gate**
  over a pattern set, never a runtime path on a per-sentence basis.
- **A general-purpose regex-intersection API.** The decider is an internal gate over step patterns.
  It is not published on the facade, does not become a comparator seam, and need not handle
  constructs the step grammar cannot contain — it refuses them instead (FR-012).
- **Rejecting contributed overlap at composition.** Declined per D5: overlap between two
  independently-authored comparators is a potential failure, and forbidding the potential would
  make two otherwise-usable comparators mutually exclusive. Reported, not fatal (FR-014).
- **Changing how overlap is *reported*.** `ambiguous-step` already names every matching pattern per
  sentence, for any mix of sources, and 012 shipped it. This feature changes only which code paths
  *rely* on there being exactly one match; it adds no finding class and alters no message. (This
  entry replaces an earlier one declaring contributed-phrase overlap out of scope altogether — see
  Clarifications for why that scoping did not hold.)
- **Changing which definition wins a collision.** Registration order and godog's `Strict` behaviour
  are unchanged. This feature makes multiplicity *visible*; it does not re-rank it.
- **CLI / `mentatctl` UX — 014.**

---

## Assumptions

- **This feature depends on 012, which has landed.** PR #41 merged to `main` at
  2026-09-11T22:19:08Z as `f6bb402`, so the `ambiguous-step` path US1 defers to is on `main`. Two
  earlier drafts of this assumption were wrong in opposite directions: the first described the
  merge as pending, and the second said this branch carries 012's unsquashed history and needs a
  rebase. **Neither holds.** The branch is cut directly from `f6bb402`, so its history is clean and
  no rebase is required. (The original branch of that name did carry the unsquashed history; its
  tree was byte-identical to `main` and every one of its commits also lives on
  `origin/012-comparator-gherkin-phrases`, so it was deleted rather than rebased.)
- **The module is untagged.** Zero tags exist, so no version contract has been published. This is
  why FR-014's contract choice is a pre-release decision rather than a break of a shipped API — a
  window that closes at the first tag, and one that D5 deliberately does not spend on forbidding
  contributed overlap outright.
- **`godog v0.15.1` remains pinned.** R1 and R10–R15 are properties of that version; a bump
  re-opens them and this feature's premise with them.
- **Disjointness is true, and after US2 it is *decided*.** No collision exists today: the stdlib
  prototype decided all 780 pairs disjoint on 2026-09-11, independently of the 1530 generated
  sentences that also found none. The feature addresses the *basis* of that claim and the code that
  depended on it, not a known defect.
- **US1 alone is no longer the intended outcome.** An earlier draft said it was, with US2 and US3
  deferrable. That framing is withdrawn: deferring the half that actually closes the gap is the
  pattern that produced 013 out of 012, and D1's argument for the deferral does not survive
  contact with a gate. US1, US2 and US3 all ship; only US4 (records) is genuinely tail-end, and
  only because it describes the others' outcome.
- **Filler-set expansion is not a substitute for either US1 or US2.** Adding fillers produces more
  samples and no decision, so it can discharge neither FR-001 nor FR-006 (D4).
