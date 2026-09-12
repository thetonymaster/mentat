# Specification Quality Checklist: Freeze Contributed Phrases During Engine Composition

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-11
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs) — *see Note 1, deliberate deviation*
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders — *see Note 1*
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
- [x] No implementation details leak into specification — *see Note 1*

## Notes

**Note 1 — code locations in the Context section are deliberate and consistent with house style.**
The generic checklist item asks for no implementation detail and no technical audience. This
spec's **Context** section deliberately cites file:line locations, and this is recorded as a
judged deviation rather than silently passed.

Three reasons:

1. **It is a defect-fix spec.** The feature's entire premise is that code contradicts its own
   published documentation. A reader cannot evaluate that claim — or check whether it is still
   true later — without knowing which code and which documentation. Stated abstractly, the
   spec would be unfalsifiable, which the repo's epistemic rules treat as worse than verbose.
2. **It matches established practice.** `specs/012-comparator-gherkin-phrases/spec.md` cites
   code 36 times. This is the documented convention for this repository, not a lapse.
3. **The containment is real.** Every citation sits in **Context**. The eleven functional
   requirements, the three user stories, and all eight success criteria are stated in terms of
   observable behaviour — "invoked exactly once per engine", "every surface observes one
   identical phrase set", "byte-identical output" — and name no Go type, signature, or package.
   A reader could verify all of them through the public surface alone.

All nine file:line citations in the spec were verified against the working tree at `f6bb402`
on 2026-09-11 and each resolves to the claimed text.

**Note 2 — no clarifications were needed.** The one question with genuine design weight was
whether capturing a snapshot at construction would change *when* comparator factories run. It
was resolved by measurement rather than by asking: the registry holds already-constructed
comparator values (a plain map lookup, `internal/registry/registry.go:117`), so it does not.
That finding is recorded in the spec's Assumptions.

**Note 3 — one assumption is a live risk worth carrying into planning.** "A shallow copy is a
complete copy" holds only while a contributed phrase and its binding carry plain text fields
exclusively. Adding a reference-typed field to either structure silently re-opens this defect.
Planning should decide whether that deserves a guard rather than only a note.

> **Resolved 2026-09-11**: planning chose the guard. Task **T032** adds a reflective test over
> the field kinds of `ContributedPhrase` and `PhraseBinding`, so a future reference-typed field
> fails the suite instead of silently voiding FR-002/FR-004. A doc note alone would have left
> research R7's "highest-value cheap guard" language overselling what shipped.

**Note 4 — added 2026-09-11 after cross-artifact analysis: one committed test contradicts
FR-003.** `TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine`
(`internal/steps/phrase_test.go:820`) passes today and asserts the per-call behaviour this
feature removes. It is now recorded as spec **D1**, research **R10**, a Complexity Tracking
entry in `plan.md`, and task **T015a**.

This was a genuine gap in Phase 0, not a documentation nit. Research R2 asked "how many places
*call* this method" (answer: one, correct) and never asked "how many places *assert* this
behaviour". The two questions have different answers, and only the second would have found
this. **A green suite proves no test disagrees with the current code — not that none disagrees
with the plan.** Recorded here because the method generalises beyond this feature.

SC-008 was corrected as a result: three *prose* artifacts need no change, but one *test*
artifact does.

**Note 5 — added 2026-09-11: research R8 was wrong, and the error would have shipped a
no-op task.** R8 claimed two copies were needed — one at capture, one on return — on the
premise that "omitting the capture copy would leave the snapshot aliasing memory the
comparator still owns." Measured false. The engine never stores the comparator's slice; it
builds `[]PhraseBinding` element-wise, and with every field a `string` that transform is
already a complete copy.

The consequences were the dangerous part, not the claim:

- **T024 (copy at capture) was a no-op**, and
- **its paired test could not go red where it was filed.** A comparator mutating its
  returned list is observable *today* (the engine re-invokes) and unobservable after the
  freeze — so the test is red in Phase 3, green in Phase 5. Filed in Phase 5 it would have
  passed on arrival, and "confirm RED" would have been satisfied by a fabricated or
  skipped step.

That is precisely the failure this repo has recorded twice — 011's un-achievable T009 red
and 012's mutation that never fired. The fix moves the test to Phase 3 (**T008a**), where
it genuinely goes red, and deletes T024. This **removes** a Principle V deviation rather
than documenting one, which is why no third Complexity Tracking row was added.

**The method lesson, which generalises**: R8 reasoned about "a slice the comparator owns"
without checking what the engine actually stores. Go's value semantics made the transform
a copy all along. Reason about the concrete types at the boundary, not about an
abstraction of them.

**Note 6 — added 2026-09-11: the plan justified a task with a false claim about the gate.**
`plan.md` and T035 both asserted the `//go:build e2e` lane holds "the only byte-identity
oracle for SC-005", and that `make ci` never compiles it. The second half is true; the
first is false. `TestGoldenHermeticStdout` (`mentat_golden_test.go:70`) sits in the **root
package with no build tag**, byte-compares `mentat.Run`'s normalized stdout against
`testdata/golden-hermetic.txt`, and therefore already runs under `make ci` (`make test` is
`go test ./... -race`). Its own header says it "complements e2e/golden_test.go (which pins
the SAME transform against the live harness)".

The task itself survives — the live-harness golden is a genuinely distinct oracle and the
e2e gap is real — but it was resting on a wrong statement, and a cheap oracle sat
unclaimed. Two related corrections followed:

- **T020 credited T035 with step-reference stdout identity.** No lane renders an *engine*
  step reference to stdout at all: the only renderer (`cmd/mentat/steps_cmd.go:66`) reads
  built-in `steps.StepDocs()`, never an engine. T020's deep-equal check is the whole
  oracle.
- **SC-005 demanded "byte-identical" of three things, two of which are not bytes.**
  `StepReference` returns step documents and `Validate` returns findings. Reworded to
  deep-equal for those two, byte-identical for run output — and its validation-findings
  clause, which no task covered, became **T020b**.

**The method lesson**: "no gate covers this" is a claim about tooling, and tooling is
cheap to check. Read the Makefile and look for the build tag before asserting what CI
does or does not run.
