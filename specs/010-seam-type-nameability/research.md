# Research: Seam-Type Nameability (010)

**Date**: 2026-09-09 | **Spec**: [spec.md](./spec.md) | **Verified at**: `f2afdda`

The spec's two `[NEEDS CLARIFICATION]` markers were closed by decision before planning
(D1–D4 in the spec). This document resolves the *implementation* unknowns those decisions
opened, and records three findings that contradicted the initial model and changed the
plan.

---

## R1 — How is the emitted report format protected? (FR-013, SC-009)

**Decision**: Add a golden test over the bytes each built-in reporter emits, reusing the
repo's existing convention: a committed fixture, `MENTAT_UPDATE_GOLDEN=1` to regenerate,
and a normalization pass that replaces nondeterministic tokens with fixed placeholders.
It lands and goes green **against today's reporters**, before any of D3 is written.

**Rationale**: The format is a real compatibility promise (anyone parsing the JSON report
in CI) and it is currently unprotected. Three goldens exist —
`specs/007-public-extension-api/contracts/public-surface.golden`,
`testdata/golden-hermetic.txt`, `cmd/mentat/testdata/golden-green.txt` — and they cover
the public surface and stdout. None covers a *report file*. `internal/report/json_test.go`
round-trips a `core.RunReport` through encode/decode, which is symmetric and therefore
structurally incapable of catching a renamed tag; its only literal assertion is a count of
the `"qualifiers"` key.

**Determinism**: `RunReport.StartedAt` (`time.Time`) and `.Duration` (`time.Duration`) are
untagged and therefore serialized. `normalizeGoldenStdout` (`mentat_golden_test.go:44-46`)
already solves exactly this shape of problem for godog's duration line; the report golden
uses the same device on the timestamp and duration fields. A fixed fixture `RunReport`
built in the test — rather than a real run — removes every other source of variance.

**Alternatives considered**:
- *Assert field names inline in unit tests.* Rejected: that is what `json_test.go` does
  today and it demonstrably does not catch the failure mode.
- *Add the check after the rewrite.* Rejected: a golden regenerated after the change
  records the new bytes and proves nothing. FR-013 makes the ordering a requirement, not a
  preference.
- *Freeze tags in the surface golden instead.* Rejected: that is stability boundary 2, a
  deliberately accepted gap whose closure is out of scope for this feature.

---

## R2 — Where do reporters live after D4?

**Decision**: Move reporters onto the per-engine `*Registry` with the same
register/resolve/seal discipline as the other seams, and add `WithReporter(name, factory)`
to the facade. Delete the package-global map.

**Rationale — and a correction to the initial model**: the first reading of this gap was
that reporters differ by being *instance*-registered while other seams use factories. That
is wrong. `RegisterDriver` and `RegisterComparator` also take instances
(`registry.go:81,109`); `RegisterJudge` and `RegisterStore` take factories
(`registry.go:159,172`). The `Factory` types on the facade are a facade-level concept that
`Run` resolves before touching the registry.

The actual distinction is **global versus per-engine**: `registry.go:190-193` holds
reporters in a package-level map under a private mutex, documented as "never sealed", while
every other seam lives on a `*Registry` value that `engine.Build` seals. That is what makes
a per-run `WithReporter` unsafe — two concurrent `Run`s would write the same global map,
re-opening the reentrancy defect 007 closed in T010/T011, with no seal to catch it.

The comment justifying the global (`registry.go:186-188`) says reporters "cannot be
per-engine" because `cmd/mentat` emits them after `Run` returns. That call path no longer
exists: emission is at `run.go:457`, inside `Run`, since the 007 recompose. The stated
reason for the global is obsolete, which is what makes the move safe rather than merely
desirable.

**Alternatives considered**:
- *Keep the global, have `WithReporter` swap and restore it.* Rejected: mutating global
  state around a call is the exact reentrancy pattern 007 removed.
- *Per-run map threaded through `runOptions` without touching `Registry`.* Rejected: it
  leaves two registration mechanisms for six seams and contradicts Constitution III
  (all wiring at one composition root).

---

## R3 — How is the emitted JSON preserved byte-for-byte? *(largely dissolved by D5)*

**Decision**: Under D5 the marshalled struct **is** the same struct, moved to
`internal/result` and renamed. Go does not serialize type names, so the bytes are unchanged
by construction. One rule survives from the original analysis:
**`ScenarioResult.RunIDs` is tagged `json:"-"`**.

**Rationale**: this question originally mattered a great deal. With mirrors (D2/D3 as first
written), preserving bytes constrained both tags and declaration order, because
`encoding/json` emits keys in declaration order:

- The facade ordered `Results` as `Scenarios, Passed, Failed, Interrupted, TotalCost,
  JudgeTotal`; `core.RunReport` ordered them `Scenarios, Total, Passed, Failed, TotalCost,
  StartedAt, Duration, Interrupted, JudgeTotal`. New fields could not simply be appended.
- Six `omitempty` tags had to be replicated exactly on the mirrors.

Collapsing to one type removes both constraints — there is no second struct to keep in
parity. What remains is that the collapsed type gains `RunIDs`, which the old facade
`ScenarioResult` had and `core.ScenarioResult` did not. Without `json:"-"` that would emit a
`"RunIDs"` key the reports never had. It is derived from `Runs`, so it is a Go-level
convenience only.

**Consequence for the reader**: the field order *of the public type* still changes relative
to today's `mentat.Results`, because the collapsed type keeps `RunReport`'s order. That is a
source-compatibility note for unkeyed literals (R7), not a wire-format concern.

**Alternatives considered**:
- *Drop `RunIDs` in favour of `Runs`.* Rejected: a breaking change to a field existing `Run`
  callers read, for no gain.
- *Keep two structs and enforce parity by test.* Rejected — see D5's rejected alternatives.

---

## R4 — Where do the result types live, and under what names? *(superseded by D5)*

**Superseded.** This entry originally decided `mentat.RunRecord` as a facade-**owned mirror
struct, not an alias**, on the grounds that a mirror is the device `ScenarioResult` already
uses and keeps `core` types off the surface.

That reasoning was correct about the device and wrong about where it can be applied — see
[R8](#r8--can-the-seam-types-be-facade-declared-at-all-no--this-is-why-d5-exists). The decision is replaced by D5:

| Type | Home | Facade |
|---|---|---|
| `Results` (was `core.RunReport`) | `internal/result` | `type Results = result.Results` |
| `ScenarioResult` | `internal/result` | `type ScenarioResult = result.ScenarioResult` |
| `RunRecord` | `internal/result` | `type RunRecord = result.RunRecord` |
| `Reporter` | `internal/result` | `type Reporter = result.Reporter` |

`internal/result` imports `internal/core` for `AggregateDetail` and `JudgeUsage`; `core`
imports nothing back. Verified: `RunReport`, `ScenarioResult`, `RunRecord` and `Reporter`
(`core.go:334-399`) are referenced only by `internal/report`, `internal/registry`, the root
package and the generated mock — nothing inside `core` depends on them, so they move
cleanly. `Results.ExitCode()` moves with the type.

---

## R5 — What does the surface golden diff look like?

**Decision**: Expect a small line-count diff and review it field by field rather than by
size.

**Rationale**: Facade-*declared* structs render as a single inline declaration
(`public-surface.golden:169` for `Results`, `:173` for `ScenarioResult`); only *aliases*
expand to one `field (X)[nn]` line each (e.g. `RunSpec` at `:78-87`). So the `Results` and
`ScenarioResult` widening rewrites two existing lines rather than adding a dozen. The
changes that add lines are the three new aliases (US2/US3) and the new `RunRecord` and
`WithReporter` declarations. One line changes meaning rather than appearing:
`:138 method (Reporter) Report(rep RunReport, w io.Writer) error`.

Regeneration is `MENTAT_UPDATE_GOLDEN=1 go test -run TestPublicSurfaceGolden`
(`surface_test.go:168-171`). There is no `make` target for it.

---

## R6 — Sequencing

**Decision**: `US4 gate` last, `US1` second-to-last, format golden first.

```
1. Report-format golden (FR-013)      — must be green before anything moves
2. US2 + US3 aliases (P2/P3)          — three closed types, no decisions, independently shippable
3. US1 Reporter re-shape (D1–D4)      — seam signature, registry move, reporter rewrites
4. US4 reachability gate (P4)         — can only go green once RunReport leaves the surface
```

**Rationale**: US4's acceptance scenario 1 requires the widened check to report **zero**
unnameable types. While `Reporter.Report` still names `RunReport`, that is impossible — so
the root-cause gate cannot land before US1, even though it is the fix that closes the
defect class. The spec's P1–P4 numbering reflects importance; this is execution order.

Steps 2 and 3 are independent of each other and could run in parallel, but they touch the
same golden file, so serializing them avoids a merge conflict in a reviewed artifact.

---

## R7 — How is the breaking change carried? (FR-009)

**Decision**: The `Reporter` signature change is breaking and takes all three acts —
regenerated golden, `CHANGELOG` entry, and a migration note. The `Results` /
`ScenarioResult` widening is additive and takes the first two.

**Rationale**: Under the stability policy a removal or signature change is breaking; a new
field is not, with one caveat worth naming in the migration note — **unkeyed composite
literals** of `Results` or `ScenarioResult` will fail to compile, and R3 reorders fields,
so an unkeyed literal that still compiled would silently mean something different. Keyed
literals (the overwhelmingly normal form, and the only form used in this repo's own tests)
are unaffected.

`examples/kafkaecho` must keep compiling untouched (SC-006). It is a facade-only external
module and constructs neither type, so no edit is expected — but it is the witness, so it
is checked rather than assumed.

---

## R8 — Can the seam types be facade-declared at all? *(no — this is why D5 exists)*

**Decision**: No. Types that internal packages must **consume** live in an internal package
and are aliased on the facade. Only **terminal** types — produced at the facade and never
passed back down — may be declared there.

**Rationale**: D2/D3 as first written were undeliverable. They put `Results`,
`ScenarioResult`, `RunRecord` and `Reporter` in package `mentat` (root), but:

- `internal/report` must name `Results` to render it,
- `internal/registry` must hold `Reporter` to resolve it,
- `internal/engine` must take `ReporterFactory func(Config) (Reporter, error)` to wire it,

and root imports all three (`run.go:11-17`). Each is an import cycle.

**Why the existing seams don't hit this**: verified at `mentat.go:41-125` — *every* public
type on the facade is an alias (`Driver = core.Driver`, `Comparator = core.Comparator`,
`Config = config.Config`, and seven more). Internal packages name the internal type; they
never name a facade type. Nothing under `internal/` imports the root package at all.

**Why `Results` and `ScenarioResult` get away with being declared today**: they are
terminal. `toResults` (`run.go:483`) produces them in the root package and hands them to the
caller; no internal package consumes them. A seam is by definition consumed, so the same
device cannot carry it.

**How it was missed**: the initial reasoning was "a facade-owned struct is the device
`ScenarioResult` already uses", which is true — and then applied it to a type with the
opposite dependency direction. No artifact caught it; it surfaced under adversarial review.
The general rule is now recorded as a spec edge case so the next seam does not repeat it.

**Alternatives considered**: see D5's rejected alternatives (keep two types; invert the
dependency).

---

## Open questions

None. Both spec-level markers are closed by D1–D4; R1–R8 close the implementation-level
unknowns.

## Corrections this research made to the spec

Four claims written into the spec before verification turned out to be wrong and were fixed
in place. The last is the one that mattered:

1. **Instance-vs-factory** was named as what sets reporters apart. It is not (R2).
2. **"`public-surface.golden` is the only golden file"** — there are three (R1).
3. **"each added field is a new golden line"** — facade-declared structs render inline, so
   added fields extend an existing line (R5). Under D5 these types become *aliases*, so they
   expand per-field after all — the original claim ends up right for the wrong reason, and
   the line-count expectation changes again.
4. **"facade-owned struct, not an alias"** was undeliverable — it is an import cycle (R8).
   This invalidated the entire Phase 5 task list and forced D5.
