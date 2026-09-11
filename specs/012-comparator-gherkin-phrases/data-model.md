# Data Model: Comparator-Contributed Gherkin Phrases (012)

**Date**: 2026-09-10 | **Baseline**: `0f9dcea` | Grounded in [research.md](./research.md)

Three entities, one derived value, and the resolution order that ties them together. Nothing
here persists — every value is built during engine composition and lives as long as the engine.

---

## 1. `ContributedPhrase`

One Gherkin sentence a comparator offers, plus the documentation the reference renderer needs.

| Field | Type | Meaning |
|---|---|---|
| `Pattern` | `string` | The regexp godog matches against. Anchored `^…$` (FR-007a). |
| `Group` | `string` | Reference section heading. Non-empty (D5). |
| `Summary` | `string` | One-line description of what the step asserts. Non-empty (D5). |
| `Example` | `string` | One valid Gherkin usage. Non-empty (D5). |

**Deliberately absent: a comparator name field.** The phrase is resolved *through* the
comparator that declared it, so the binding is structural. A name field would be a second,
forgeable statement of the same fact — and the first thing an author could get wrong.

**Mirrors `stepDef`** (`internal/steps/metadata.go:14-30`) field-for-field minus `handler`,
because `StepDoc` (`metadata.go:36-41`) is what the renderers consume and the contributed rows
must be renderable through the same path (FR-012, FR-014).

### Validation rules (all enforced at engine build, all loud — FR-006/007/007a/008)

| # | Rule | Error names |
|---|---|---|
| V1 | `Pattern` compiles as a regexp | contributor, pattern, compile error |
| V2 | `Pattern` begins `^` and ends with an unescaped `$` (R8) | contributor, pattern |
| V3 | `Pattern` is not identical to another contributed pattern | **both** contributors, pattern |
| V4 | `Pattern` is not identical to a `stepDefs` row's pattern | contributor, the built-in step |
| V5 | `Group`, `Summary`, `Example` are all non-blank | contributor, the missing field |

V3/V4 catch only *identical* patterns. Genuine overlap between two well-formed anchored
patterns is caught at match time by godog's strict matcher (R1) — see `Collision` below.

---

## 2. Phrase-contributor seam

The optional interface a comparator implements to declare phrases. Discovered by **type
assertion**, never by registration — the same discovery model 011 chose for `ExpectationParser`
(`internal/core/core.go:114-132`), so a comparator that does not implement it keeps working
exactly as today (FR-004, D1).

Declared in `internal/core` (R2) because 010's D5 forbids declaring it at the facade —
`internal/steps` consumes it.

> **Corrected 2026-09-11 (R11).** This paragraph also claimed the type "appears in a published
> seam's method set, so 010's nameability sweep demands a facade alias automatically (SC-008)".
> **Measured false.** `TestFacadeNameabilitySweep` is SEEDED from the aliases that already exist
> and walks outward, so deleting an alias deletes its seed and nothing fires. Both seams are
> optional and discovered by type assertion, so no published type references them either. What
> actually guards the aliases, both measured: `TestPublicSurfaceGolden` (which reports "symbols
> in golden but NOT present now") and the compile-time witnesses in
> `custom_phrase_facade_test.go` — the same pair 011 used for `ExpectationParser`. See R11 and
> tasks.md T062.

## 3. Capture-parser seam

The sibling of 011's `ExpectationParser`, receiving the phrase's regex captures rather than a
docstring body (D6). Returns the comparator's own `Expectation`.

**Routing is a property of the pattern, not a runtime guess** (D6):

| Pattern has captures | Step carries docstring | Seam used |
|---|---|---|
| no | yes | 011's `ExpectationParser` — unchanged |
| yes | no | capture-parser |
| yes | yes | capture-parser, docstring appended as the final argument |
| no | no | capture-parser with an empty capture list (a constant expectation) |

**Error contract, inherited from 011's D5 and non-negotiable**: a parser returning a nil
expectation with no error is refused rather than trusted (`steps.go:615-650` already does this
for the docstring path), and a parse error is wrapped with `%w` naming the comparator. Never a
zero-value expectation, never a passing verdict for a step that asserted nothing.

---

## 4. `EngineStepSet` (derived)

The union of the built-in `stepDefs` rows and the contributed phrases of **one** engine. It is a
**value derived from an engine, never a package-level singleton** (D3) — the property US2 exists
to prove and the reason `precheck.go:76-91`'s `sync.Once` is deleted rather than adapted (R6).

Four consumers, all of which must see the same set:

| Consumer | Today | After |
|---|---|---|
| godog registration | `registerSteps(reg, w)` iterates `stepDefs` | takes the phrase set as a **parameter** (R5) |
| Drift gate | bidirectional equality vs `stepDefs` | **partition** assertion (FR-005, D4) |
| Step-binding precheck | package `sync.Once` cache | per-engine pattern set (FR-009/010) |
| Reference renderer | `StepDocs()` | engine-scoped renderer; `mentat steps` stays built-in-only (FR-013, R9) |

### Ordering is load-bearing, not cosmetic

Registration order decides which pattern wins a collision, because godog returns the
**first** match (R1). Two rules follow:

1. **Built-ins register before contributed phrases**, so a colliding contributed phrase can
   never displace a built-in. Under `Strict` this surfaces as an ambiguity failure rather than
   the silent shadowing that happens today.
2. **Contributed phrases enumerate in sorted comparator-name order**, via
   `Registry.Comparators()` (`registry.go:125-137`), which already sorts. 011 added that sort
   for a deterministic error message; here it becomes a **correctness** requirement — map
   iteration order would make collision resolution vary between runs of an unchanged suite (R3).

### Grouping invariant

Contributed phrases render in contiguous group blocks after the built-in groups, **regardless of
the group names authors choose**. `TestStepDocsGroupsAreContiguous` (`docs_test.go:43`) lets the
markdown generator emit one heading per group by watching the group change; a contributed phrase
declaring an existing name (e.g. `"Shape"`) would otherwise produce a duplicated heading (R9,
FR-014).

---

## 5. `Collision`

Two entries in one `EngineStepSet` whose patterns can match the same sentence. Detected in two
places, deliberately (D8):

| Kind | When | Mechanism |
|---|---|---|
| Identical patterns (V3/V4) | engine build | Mentat's own check, naming both contributors |
| Genuine overlap between well-formed anchored patterns | step match | godog's strict matcher, naming **every** matching expression |

The second exists because overlap is not cheaply decidable in general, and because R1 proved
godog's matcher does it exactly once `Strict` is on. The measured message shape:

```
ambiguous step definition, step text: the widget is green
    matches:
        ^the widget is (\w+)$
        ^the widget is green$
```

It reaches Mentat as a non-nil `stepErr` in the After hook (`steps.go:126-129`), so
`Pass: stepErr == nil` yields a FAILED scenario carrying that text as its reason — **no new
surfacing mechanism** (R1).

---

## 6. `Finding` (existing, re-scoped)

`steps.Finding` (`precheck.go:23-28`) is unchanged in shape — `File`, `Line`, `Class`,
`Message`. What changes is who can produce a trustworthy `unbound-step`:

- **Engine-aware path** (scenario-init, and D7's library validate entry point): sees contributed
  phrases, so `unbound-step` means what it says.
- **`mentat validate` binary**: structurally cannot see a consumer's registrations
  (`validate.go:117` builds a `checker` with no registry, and the registrations live in another
  module). It keeps its current strictness for built-ins and **documents** the limit; it gains
  no manifest flag and no second source of phrase truth (D7, FR-011a).

`Finding` is aliased on the facade for D7's return type — legal under 010's D5 because it is
declared in `internal/steps`, beneath root, not at it (R7).

---

## Entity relationships

```
Comparator (existing seam)
   ├── optionally implements ── phrase-contributor seam ──▶ []ContributedPhrase
   ├── optionally implements ── capture-parser seam ───────▶ Expectation   (012, D6)
   └── optionally implements ── ExpectationParser ─────────▶ Expectation   (011, untouched)

Engine ──(sorted names, R3)──▶ resolves contributors ──▶ EngineStepSet
                                                            │
                     ┌──────────────────┬───────────────────┼────────────────────┐
                     ▼                  ▼                   ▼                    ▼
              godog registration   drift partition   binding precheck    reference renderer
                     │
                     └── collision ──▶ build-time (identical) │ match-time (overlap, Strict)
```

## What this model does not add

- No new registry (R3) — a seventh registry for data reachable through the sixth would be
  over-abstraction with no second implementation.
- No aggregate-comparator phrases — no `WithAggregateComparator` facade option exists, so there
  is nothing registerable to contribute one (D2, inheriting 011's D4).
- No change to `type Expectation = any`, to 011's generic `Extend` row, or to any built-in
  comparator's existing step (Out of Scope).
