# Contract: the contributed-phrase and capture-parser seams

**Fulfils**: FR-001, FR-003, FR-004, FR-006, FR-012, FR-015. Decisions: [D5](../spec.md),
[D6](../spec.md), [D9](../spec.md). Research: [R2](../research.md), [R4](../research.md).

**Placement**: `internal/core`, aliased on the facade. Not declarable at the facade — 010's D5
forbids it because `internal/steps` consumes these types and root imports `internal/steps`.
`internal/core` imports only stdlib plus `internal/trace`, so no cycle is created (R2, verified).

---

## The two seams

Both are **optional** and **discovered by type assertion**, never by registration — the model
011 established for `ExpectationParser` (`internal/core/core.go:114-132`). A comparator
implementing neither keeps working exactly as it does today.

### Contributed-phrase seam

Returns the Gherkin sentences this comparator offers, each with the documentation the reference
renderer needs (`ContributedPhrase`: `Pattern`, `Group`, `Summary`, `Example` — see
[data-model.md](../data-model.md)).

Called **once per engine build**, never per scenario. The returned slice is treated as
immutable by Mentat; a comparator that mutates it afterwards has no effect on the built engine,
which is the correct outcome — the engine is sealed (`registry.go:71-79`).

### Capture-parser seam

Turns a phrase's regex captures into the comparator's own `Expectation`. The sibling of 011's
`ExpectationParser`, **which keeps its exact signature and role** (D6, FR-004).

Which seam serves a phrase is a property of its pattern, decided at build, not a runtime guess:

| Captures | Docstring | Seam |
|---|---|---|
| no | yes | `ExpectationParser` (011) — unchanged |
| yes | no | capture-parser |
| yes | yes | capture-parser, docstring appended as the final argument |
| no | no | capture-parser, empty capture list (constant expectation) |

**Why a sibling rather than widening 011's interface.** Widening breaks a public seam shipped
three commits ago (`ec4efbc`) for zero capability gain — Mentat synthesizes the godog bridge
either way (R4), so nothing forces the comparator-facing signature. Joining captures into one
string is lossy at the delimiter and makes `[]` and `[""]` indistinguishable, which is
Constitution IV's silent-corruption shape.

---

## Error contract (Constitution IV — every path loud, none silent)

Inherited from 011's D5 and extended. Each error names the comparator and the offending value.

| Situation | Required behaviour |
|---|---|
| Parse returns an error | Surfaced wrapped with `%w`, naming the comparator, so the author sees which comparator rejected their sentence and why |
| Parse returns a nil expectation with **no** error | **Refused**, not trusted. Forwarding it would let a comparator tolerating nil return a passing verdict for a step that asserted nothing. `steps.go:615-650` already does exactly this for the docstring path; the captures path matches it |
| Comparator contributes a phrase but implements no capture-parser | Hard error at engine build naming the comparator — a phrase that can never produce an expectation is an authoring defect, not a runtime surprise |
| Any validation rule V1–V5 fails | Engine build fails; **no scenario executes** ([data-model.md](../data-model.md) §1) |

The nil check is for an **untyped** nil only. A typed nil pointer is a non-nil interface and
reaches `Compare`, where it is the comparator's own type assertion and its own bug — the same
boundary 011 drew, restated here so it is not re-litigated.

---

## The godog bridge (D9, R4)

Mentat synthesizes each phrase's handler with `reflect.MakeFunc` over
`reflect.FuncOf(N × string, error)`, N taken from the compiled pattern's `NumSubexp()`; a
docstring-carrying phrase appends `*messages.PickleDocString`.

**This is forced, not chosen.** godog rejects `[]string` and variadic handlers — `reflect.Slice`
accepts `[]byte` only (`internal/models/stepdef.go:222-233`) — and its arity check is one-sided
(`stepdef.go:58`): too few arguments error, **surplus arguments are silently discarded** because
the conversion loop runs `i < numIn`. A hand-written fixed-arity handler would therefore lose
captures without any diagnostic.

Deriving N from the same regex godog matches against makes the two structurally unable to
disagree, converting a silent-drop hazard into an impossibility (SC-013).

**Containment**: the bridge lives in one file (`internal/steps/phrase.go`) and nothing above it
sees godog's signature rules. That containment is exactly what frees the capture-parser seam to
take a `[]string` — godog's restriction is invisible to extension authors.

---

## Stability obligations

- Both seams appear in the **public-surface golden** with their full method sets (SC-008,
  FR-015). The golden is what demands them, together with the compile-time witnesses in
  `custom_phrase_facade_test.go` (`var _ mentat.PhraseContributor = …` in an external test
  package, so a missing alias is a build failure).

  > **Corrected 2026-09-11 (R11).** This bullet claimed the facade nameability sweep "demands
  > their aliases automatically because they appear in a published seam's method set — 010
  > paying for itself again". **Measured false**, twice over: `TestFacadeNameabilitySweep` is
  > SEEDED from the aliases that already exist, so removing one removes its seed and nothing
  > fires; and both seams are **optional**, discovered by type assertion, so they appear in no
  > published seam's method set to be walked to. 010 does not pay for itself here. SC-008 still
  > holds — via the golden plus the witnesses, the same pair 011 used — but it holds because
  > someone chose those guards, not automatically. **A gate's coverage is a property to measure,
  > not to infer from its name.**
- `ContributedPhrase`'s exported fields are frozen in the golden as struct field lines (009's
  US1 widened the golden to cover exported struct fields).
- 011's `ExpectationParser` line in the golden **must not change**. Its stability is the
  operational test of D6's claim that this feature adds a sibling rather than a replacement —
  the same shape as 011's own "the golden changed exactly once, by exactly two lines" invariant.
