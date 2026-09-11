# Specification Quality Checklist: Comparator-Contributed Gherkin Phrases

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-10
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

All items pass. The three clarifications this spec opened with were resolved on 2026-09-10 and
are recorded as decisions D6–D8, with a summary table in *Resolved Questions*.

**Two content-quality caveats, both deliberate.**

*"Written for non-technical stakeholders"* is marked pass in the sense this repo uses it.
Mentat's users are Go developers writing behaviour tests, and the *Verified current state* and
*Verified godog behaviour* sections are grounded in file:line evidence on purpose — 011's
retrospective records four corrections it had to make to spec text written without that
grounding, and this spec found a fifth. It is not written for a business audience.

*"No implementation details"* is marked pass with one exception: D9 names `reflect.MakeFunc`
and the godog findings quote source. That is not leakage — godog's arity and strictness rules
are external constraints that decide what is buildable, and recording them as prose would lose
the precision that makes them checkable.

**Findings this spec adds beyond what 011's D1 anticipated**, all verified at `0f9dcea` /
`godog@v0.15.1`:

1. `stepDefs` has **five** consumers, not the three D1 named. The two unnamed ones — the
   step-binding precheck and `mentat validate` — shape the work more than the drift test does.
2. `internal/steps/precheck.go:76-91` caches compiled patterns in a package-level `sync.Once`,
   so per-engine phrases are wrong by default in the naive implementation. Same defect class 007
   closed for registries. FR-010 requires deleting it, not adapting it.
3. **D1's ambiguity claim is refuted.** godog gates ambiguity detection on `Strict`
   (`suite.go:547-553`) and `mentat.Run` does not set it (`run.go:411-419`), so two colliding
   patterns resolve **first-wins in silence** today. Because built-ins register before
   contributed phrases, a colliding contributed phrase would be silently shadowed by a built-in.
   This is a **live defect**, not only a 012 concern — hence SC-011 is written as a regression
   test that must be seen failing against `0f9dcea` first.
4. godog accepts no `[]string` or variadic handler (`internal/models/stepdef.go:222-233`) and
   silently discards surplus captures (`stepdef.go:58`), so Mentat must synthesize each handler
   with an arity derived from the pattern (D9). This constrains the bridge only, not the
   comparator-facing seam — which is why D6 was free to choose a sibling interface.

**The one risk planning must not treat as settled.** D8 enables `Strict: true` on the run path.
The prediction is zero golden churn (Mentat has no pending steps; undefined steps already fail
via the collector). That is a prediction, not a result. SC-012 requires a recorded before/after
across the committed goldens **including the `//go:build e2e` stdout goldens, which `make ci`
does not compile** — a green `make ci` is explicitly not accepted as evidence.
