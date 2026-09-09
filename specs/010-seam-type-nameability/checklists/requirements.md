# Specification Quality Checklist: Seam-Type Nameability

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-09-09
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

### Resolved — 2026-09-09

Both blocking markers were closed by explicit decision, recorded as D1–D4 in the spec's
new **Decisions** section. The two boxes above are now checked.

- **FR-004 → D1.** `Reporter` is widened *and* made registrable. "Widen only" and "narrow
  by removal" were both considered and rejected on the record. This supersedes the
  "no concrete external demand" note at `mentat.go:59-62` for `Reporter` only.
- **FR-005 → D2.** Resolved by *not* publishing `RunReport`. The seam is re-shaped to
  `Report(res Results, w io.Writer) error` on the facade-owned `Results`, which dissolves
  the `ScenarioResult` collision and keeps `core.ScenarioResult`/`RunRecord` off the
  surface — upholding the feature-007 decision instead of reversing it.
- **SC-001 and SC-003 now stand as written**, since the decision went the widen way.
  SC-002 gained a clause: three types reach nameability by being named, `RunReport` by
  leaving the surface.

### Two decisions the resolution added

Neither was in the original spec; both came out of verification during resolution.

- **D3 — one seam, not two.** `Results` was found to be lossy against `RunReport` beyond
  the suite aggregates (7 data points, table in the spec). Rather than let custom
  reporters be permanently second-class, `Results` and the facade `ScenarioResult` widen
  to full fidelity — with a facade-owned `RunRecord` **mirror struct**, not an alias — and
  the built-ins are re-expressed on the single published seam. New: FR-011, FR-012,
  SC-008.
- **D4 — reporters move into the per-engine registry.** Reporters are package-global
  (`registry.go:192`) and registered as *instances*, not factories, unlike every other
  seam. A `WithReporter` writing to that global would reintroduce the reentrancy defect
  007 T010/T011 closed. New: FR-014, SC-010.

### D5 — added 2026-09-09 after adversarial review

`/speckit-analyze` found the D2/D3 mechanism to be an **import cycle**, which invalidated the
whole Phase 5 task list rather than merely complicating it. Verified independently before
accepting: `run.go:11-17` shows root importing `internal/report`, `internal/engine` and
`internal/registry`; `mentat.go:41-125` shows *every* public type on the facade is an alias;
nothing under `internal/` imports root.

D5 moves `Results`, `ScenarioResult`, `RunRecord` and `Reporter` into a new leaf package
`internal/result` and aliases them, collapsing the D2/D3 mirrors into one type. The general
rule it discovered — **only terminal types may be facade-declared; consumed types must be
aliased** — is recorded as a spec edge case and in `new-seam.md` (T050) so the next seam does
not repeat it.

Side effect worth noting: the collapse removes the feature's largest risk. With one struct
there is nothing to keep in tag-and-field-order parity, and the marshalled struct is the same
struct relocated, so the emitted bytes are unchanged by construction.

Also fixed in the same pass: FR-008 vs SC-006 conflicted (both witnesses "MUST be extended"
vs the example module "untouched") — FR-008 now scopes extension to the facade-only test;
SC-008 said "down from 7" while enumerating 8 — now 8.

### Risk raised to a requirement

`jsonReporter` marshals `core.RunReport` wholesale (`json.go:13-17`), the facade mirrors
carry no json tags where their `core` counterparts carry six, and **nothing in the repo
would catch a resulting format change**: the surface golden does not render tags
(boundary 2), `public-surface.golden` is the only golden file in the tree, and
`json_test.go` round-trips through the same struct — symmetric, so structurally incapable
of detecting a tag rename. FR-013 therefore requires the format check to exist and be
green *before* the reporters are rewritten, and SC-009 measures it.

### Interpretation notes on passing items

- *"No implementation details"* — the subject of this feature **is** the published API
  surface, so symbol names (`RunReport`, `Verdict.Detail`) are the domain vocabulary, not
  leaked implementation. The requirements are deliberately written mechanism-neutrally:
  they state what an external author must be able to do, never that the fix is a type
  alias. The Problem section cites file:line evidence because the spec asserts verified
  facts about the current state, and those claims must be checkable.
- *"Non-technical stakeholders"* — the stakeholder for a library-surface feature is an
  extension author. The spec is written so a reader who does not know the codebase can
  follow the user stories and success criteria without reading source.

### Verification performed while drafting

All current-state claims were checked against the working tree at `f2afdda`, not taken
from the feature description:

- The four types confirmed frozen at `public-surface.golden:83,87,115,138` and declared
  at `internal/core/core.go:90,140,292,334`.
- Transitive closure computed: `AggregateDetail`, `HTTPSpec`, `ExtractPolicy` are closed
  (builtins + `*regexp.Regexp`); `RunReport` is not (reaches `core.ScenarioResult` →
  `RunRecord`).
- The `ScenarioResult` collision and its stated rationale confirmed at `run.go:243-247`.
- The absent `WithReporter` confirmed against `run.go:189-210`, `engine/build.go:53`, and
  the rationale at `mentat.go:59-62`.

Two facts absent from the original feature description were surfaced this way (Findings A
and B) and both materially change scope — which is the reason for the two open markers
rather than assumed defaults.

### Verification performed while resolving (2026-09-09, at `f2afdda`)

Every claim added by D3, D4 and FR-013 was checked against the working tree, not inferred:

- `Results` field-by-field against `RunReport` (`run.go:217-224` vs `core.go:334-352`) and
  facade `ScenarioResult` against `core.ScenarioResult` (`run.go:247-259` vs
  `core.go:355-385`) — the 7-item loss table.
- Which fields the built-ins actually render: `html.go:18-19,24,27,29,30-31`,
  `junit.go:51,60,64-65`. `Tags` is rendered by none — carried for parity only.
- `jsonReporter` marshals `core.RunReport` wholesale (`json.go:13-17`); `json_test.go`
  round-trips through the same struct and literal-pins only `"qualifiers"`.
- Golden coverage: three committed goldens exist (`public-surface.golden`,
  `testdata/golden-hermetic.txt`, `cmd/mentat/testdata/golden-green.txt`) and they cover
  the public surface and stdout — **none covers an emitted report file**. The
  `MENTAT_UPDATE_GOLDEN` + `normalizeGoldenStdout` convention is reusable for one.
- Reporters are package-global and never sealed (`registry.go:186-208`), unlike every
  other seam, which lives on the sealed per-engine `*Registry`. Instance-vs-factory is
  *not* the distinction: `RegisterDriver`/`RegisterComparator` take instances,
  `RegisterJudge`/`RegisterStore` take factories.
- Reporters are registered at `report.RegisterBuiltins` (`engine/build.go:56`) and
  emitted at `run.go:457`
  — inside `Run`, contradicting the "the CLI emits them" comments at `engine/build.go:53`
  and `registry.go:187` (hence FR-015).
