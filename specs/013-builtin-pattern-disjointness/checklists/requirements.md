# Specification Quality Checklist: Built-in Step-Pattern Disjointness

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-11
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

All items pass. Re-validated 2026-09-11 after `/speckit-clarify` (3 questions); 16/16 → 16/16, no
item changed state, but the substance behind several changed a great deal — see below.

### The "zero clarification markers" claim did not survive, and that is the useful part

The original note said: *"the one genuinely open question — whether to decide regex intersection
exactly — has a reasonable default (D1: remove the reliance instead)… If planning disagrees with
D1, that entry is where to push back."*

**Planning disagreed, and D1 was wrong.** Not a judgement call that went the other way — a false
premise. D1 argued a proof is "a snapshot of 40 patterns; the 41st reopens it", which is true of a
hand proof and false of a **decision procedure wired as a gate**. D2 argued the route needed "a DFA
library or hand-rolled automata", and a ~250-line stdlib prototype decided all **780 pairs in
0.36s** with zero dependencies. Both decisions have been corrected in place, with their original
wording quoted so the reversal is legible.

So the honest reading of "zero `[NEEDS CLARIFICATION]` markers" is that **a confident default
suppressed the one question that mattered.** A default is not a resolution; it is an answer nobody
checked. This checklist's own note is what preserved the question well enough to be asked later —
which is the argument for writing such notes, and against trusting the checkbox above it.

### Three deliberate content-quality caveats

*"Written for non-technical stakeholders"* and *"No implementation details"* pass in the sense this
repo uses them, unchanged from 012's reading: this feature's user is a contributor adding a
built-in step, and the spec names `expandPattern`, `matchBuiltin`, `matchPhrase`, `resolvePhrases`
and `regexp/syntax` because the feature *is* a property of that code. Stating it more abstractly
would reproduce the failure the feature exists to correct.

**New, third caveat:** *"Success criteria are technology-agnostic"* passes only because SC-007's
`max 74 product states` / `0.36s` figures are labelled a **prototype baseline** — dated evidence
for the decision, not the criterion. The criterion itself (decide every pair; zero intersecting;
record the count and wall clock) is agnostic. A reader who thinks that line is doing more work than
that should treat it as a measurement to re-take, not a target to hit.

### Measurements, taken rather than recalled

2026-09-11, current branch. Generator: 40 patterns × 9 fillers → **1530** unique sentences, max
**54** expansions per pattern×filler, **0** truncations — the 1530 independently reproduces R10's
figure from 012. Decider prototype: **780** pairs decided, **0** intersecting, max **74** product
states, **0.36s** wall including compilation. Construct census across all 40 patterns: `Literal`
155, `Capture` 52, `Concat` 52, `CharClass` 48, `BeginText`/`EndText` 40 each, `Plus` 26, `Star`
22, `Alternate` 17, `Quest` 10 — and **zero** word boundaries, case folding, `BeginLine`/`EndLine`,
`Repeat` or `AnyChar`, with **40/40** anchored at both ends.

The generator's three potential blind spots remain worth stating precisely: the filler set, a depth
cap at 12, and a 4000-combination `OpConcat` truncation. Only the **first** is live — nothing is
deep or wide enough to reach the others. Claiming three live blind spots would overclaim in the
opposite direction from the error this feature was raised to fix.

### What planning should not treat as settled

The old entry here (push back on D1) is **discharged** — D1 was pushed back on and corrected.
Two things replace it:

1. **The decider's negative verdicts are only as good as the decider.** US3 exists for this and is
   not optional; a gate reporting "decided disjoint" from a buggy product automaton is this
   feature's own defect one level up, wearing a stronger word.
2. **The new finding class is a validate-surface change.** FR-014 requires a finding for
   pattern-level overlap but does not name its class key. `012`'s `contracts/validate-surface.md`
   enumerates the classes, so adding one touches a documented contract and the stdout goldens —
   which are `//go:build e2e` and therefore invisible to `make ci`. Name it at plan time and run
   the e2e lane.

### Structural change made during clarification

`User Story 4` was added and the stories renumbered: US3 (widen the filler set) was **superseded**
by the decider rather than deferred, so the decider became US2, its verification US3, and the
records story moved to US4 — where it belongs, since it can only describe the others' outcome.
Story order now matches priority order.


> **Superseded block removed (2026-09-12).** Lines 105-136 of this file repeated the
> content-quality caveats, the measurement table and the planning note in their PRE-clarification
> form — including the very "push back on D1" entry that the section above records as
> **discharged**. The file therefore asserted both that the exact-decision route was Out of Scope
> and that D1 had been corrected and the route adopted as US2. The later, corrected versions are
> kept; the earlier ones are deleted rather than annotated, because a checklist that contradicts
> itself gives a reader no way to tell which half is current. Found in review of PR #43.
