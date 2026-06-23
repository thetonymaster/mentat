# Mentat Sidecar Expectations YAML — Design

**Date:** 2026-06-23
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`
**Builds on:** `2026-06-22-mentat-shape-comparator-design.md` (the shape comparator and its
inline Gherkin grammar, shipped in PR #17).
**Related:** the master design names three Phase-3 items — the `shape` comparator (done),
a **span-attribute result source**, and **sidecar expectation YAML**. This spec is the
**sidecar expectation YAML** item. The master design hints the grammar
(`Then the run matches shape "fanout-summarize"`, §6 line 244) and lists `expectations/`
in the module layout (§7 line 263). The span-attribute result source remains its own
future spec.

## 1. Purpose

The inline shape grammar asserts **one structural fact per Gherkin step**
(`a span matching "S" exists`, `…is a child of…`, `…has at least N children…`). A run
with a non-trivial expected structure — a planner that fans out to ≥3 tools, summarizes
under the chat span, and produces no `ERROR` span — needs five or six such steps, repeated
verbatim in every scenario that wants the same shape. That is noisy in the `.feature` and
impossible to reuse.

Sidecar expectations YAML gives that bundle a **name**. A file under `expectations/`
declares an ordered list of shape clauses; a single new step,
`Then the run matches shape "<name>"`, evaluates the whole bundle as a conjunction.
It is a **packaging and reuse layer over the shipped shape comparator** — the same
assertion families, the same selectors, the same in-memory matching — not a new matching
engine.

## 2. Scope

**In scope:**

- A new `internal/expectations` package: the YAML schema, `Load(dir) (Patterns, error)`,
  and translation of each clause into a validated `comparator.ShapeExpectation`.
- One pattern per `*.yaml` file: a required `name`, optional `description`, and a non-empty
  `clauses` list. Pattern names are unique across all files in the directory.
- A clause grammar that mirrors the four shape families already shipped — `exists`/`absent`
  (with optional `count`), `child`/`descendant` containment (via `of`), and `fanout`
  (direct-child cardinality) — using human-readable discriminator keys.
- A new `comparator.ShapePatternExpectation{Name, Clauses}` that the existing `shape`
  comparator's `Compare` evaluates clause-by-clause, **aggregating every failing clause**
  into one verdict. No new comparator is registered; no new matching logic.
- A config field `expectations` (default `expectations/`) on `config.Config`; patterns
  loaded and fully validated at the composition root (`engine.Build`).
- One new Gherkin step `^the run matches shape "([^"]*)"$` bound in `internal/steps`, plus
  a `sc.Before` pre-check that an unknown pattern name fails before the SUT is driven
  (mirroring the existing CEL `precompileScenario`).
- Hermetic table-driven unit tests for the loader and the new expectation, a `.feature`
  exercising the step, and the mandatory **L3 meta-test** proving Mentat goes red on a
  pattern the run does not satisfy.

**Out of scope (deferred, by design):**

- **Span-attribute result source** — the third Phase-3 item; its own future spec.
- **Richer matching power.** The bundle expresses only what the inline grammar already
  expresses. No nested subtree templates, sibling ordering, glob/regex/inequality
  selectors, or OR/NOT — those remain shape-comparator concerns governed by that spec's §2.
- **Multiple patterns per file.** One pattern per file in v1; a top-level list form is a
  noted, additive future extension.
- **Inline docstring of clauses** on the step (a `the run matches shape:` + YAML docstring
  form). File-only in v1; the docstring form is additive later against a concrete need.
- **Parameterized patterns** (selector placeholders filled per scenario). Deferred until a
  concrete need; v1 patterns are fully literal.
- **`count` operators beyond `>=` / `==`.** The shape comparator's `Count` supports only
  those two (`shape` spec §5); the YAML `count` grammar matches it exactly.

## 3. Architecture & placement

The new package depends on `comparator` one-way (for `ShapeExpectation`, `Selector`,
`ParseSelector`, `Count`); `comparator` never imports it. `engine` already imports
`comparator` (`engine/build.go:4`), so `engine → expectations → comparator` introduces no
cycle. `comparator` stays free of YAML and file IO, preserving invariant #1's spirit (the
comparator is pure over `Evidence`).

```
internal/expectations/   YAML schema + Load(dir) + clause→ShapeExpectation translation
internal/comparator/     gains ShapePatternExpectation + pattern branch in shape.Compare
internal/engine/         Build loads patterns; Engine.ShapePattern(name) exposes them
internal/steps/          one step binding + handler + sc.Before name pre-check
internal/config/         Config.Expectations field (default "expectations")
```

**Data flow (four stages):**

1. **Load (once, in `engine.Build`).** `expectations.Load(cfg.Expectations)` reads every
   `*.yaml` under the directory, parses each, and **validates every clause** by
   translating it into a real `comparator.ShapeExpectation` and running the comparator's
   own validation (selector parse via `ParseSelector`, count op, relation, kind). Any
   error returns from `Build`; `cmd/mentat` already turns a `Build` error into a non-zero
   exit (`main.go:61-65`) — so a malformed pattern fails **before any scenario runs**.
2. **Attach.** `Engine` gains a `patterns` field (`expectations.Patterns`); `Build`
   stores the loaded set. `Engine.ShapePattern(name) ([]comparator.ShapeExpectation, bool)`
   exposes it to the step layer.
3. **Pre-check (in `sc.Before`).** The step initializer scans each scenario's steps for
   `the run matches shape "X"`; if `X` is not a loaded pattern, it returns a hard error
   before the SUT is driven — the same fail-early discipline as `precompileScenario`
   (`steps.go:83`, `:383`).
4. **Evaluate (at the step).** `w.matchesShape(name)` looks up the clauses and calls
   `w.check("shape", comparator.ShapePatternExpectation{Name: name, Clauses: clauses})`
   (`steps.go:141` dispatch, unchanged).

## 4. YAML schema

One file is one pattern. The filename is organizational only; the `name` field is
authoritative.

```yaml
name: fanout-summarize
description: planner fans out to >=3 tools, then summarizes under chat   # optional
clauses:
  - exists: "gen_ai.operation.name=execute_tool"
    count:  ">=3"            # optional; omit ⇒ "at least 1"
  - absent: "span.status=ERROR"
  - child:  "gen_ai.tool.name=summarize"
    of:     "gen_ai.operation.name=chat"
  - descendant: "gen_ai.tool.name=search"
    of:         "gen_ai.operation.name=invoke_agent"
  - fanout:
      parent: "gen_ai.operation.name=chat"
      child:  "gen_ai.operation.name=execute_tool"
      count:  ">=3"          # REQUIRED for fanout
```

**Clause forms.** Each clause is a mapping with **exactly one** discriminator key. The key
chosen selects the shape family and maps 1:1 to a `ShapeExpectation`:

| Clause keys | Kind | Subject | Parent | Relation | Count |
|---|---|---|---|---|---|
| `exists: S` [`count: C`] | exists | S | — | — | C or nil (≥1) |
| `absent: S` | absent | S | — | — | — |
| `child: C`, `of: P` | containment | C | P | child | — |
| `descendant: C`, `of: P` | containment | C | P | descendant | — |
| `fanout: {parent: P, child: C, count: C}` | fanout | C | P | child | C (required) |

A selector value (`S`, `C`, `P`) is the exact same quoted `key=value[, …]` conjunction the
inline grammar uses, parsed by `comparator.ParseSelector` — including the reserved
`span.name`/`span.status`/`span.kind` intrinsic keys (`shape` spec §4).

**`count` grammar.** A string of the form `>=N` or `==N` (whitespace trimmed), `N` a
non-negative integer. This is the surface form of `comparator.Count{Op, N}`, whose `Op` is
`">="` or `"=="` only. Any other operator (`>`, `<`, `<=`, `!=`) or a non-integer `N` is a
hard error. `count` is optional for `exists` (absent ⇒ "at least 1"); required for
`fanout`; rejected for `absent`/`child`/`descendant`.

## 5. Expectation contract

```go
// ShapePatternExpectation is a named bundle of shape clauses evaluated as a conjunction.
// Each clause is a fully-formed ShapeExpectation produced by the expectations loader.
type ShapePatternExpectation struct {
    Name    string
    Clauses []ShapeExpectation
}
```

The current `shape.Compare` validates and dispatches a single `ShapeExpectation`. This
spec refactors that per-clause "validate + evaluate" body into an internal helper
`evalClause(tr *trace.Trace, exp ShapeExpectation) (core.Verdict, error)`, leaving the
shipped inline behaviour byte-for-byte identical. `Compare` then type-switches on `e`:

- `ShapeExpectation` → `evalClause` (unchanged path for the eight inline steps).
- `ShapePatternExpectation` → iterate `Clauses`:
  - a clause that returns an **`error`** (malformed: bad kind/relation/count, empty
    selector) propagates as a hard `error` wrapped with the pattern name and clause index —
    this is an author bug, not a behaviour. (In practice the loader has already validated,
    so this is defense-in-depth.)
  - a clause whose verdict is `Pass:false` contributes its reason(s) as elements of the
    pattern verdict's `Reasons`, each prefixed `pattern "<name>" clause N (<kind>): …`
    (N is 1-based). One element per failing clause keeps the existing `check` join (§8)
    readable and names the pattern even when a scenario asserts several.
  - empty `Clauses` → `error` (an empty pattern is meaningless; the loader rejects it too).
  - if any reasons were collected → `Verdict{Pass:false, Reasons:[…]}`; else `Pass:true`.

`Compare`'s existing wrong-type error path stays: a `core.Expectation` that is neither
`ShapeExpectation` nor `ShapePatternExpectation` is a wiring bug → `error`.

## 6. Gherkin grammar

One new step, bound alongside the eight inline shape steps in
`InitializerWithCollector` (`steps.go:72-79`):

```gherkin
Then the run matches shape "fanout-summarize"
```

```go
sc.Step(`^the run matches shape "([^"]*)"$`, w.matchesShape)
```

```go
func (w *world) matchesShape(name string) error {
    clauses, ok := w.eng.ShapePattern(name)
    if !ok {
        return fmt.Errorf("unknown shape pattern %q (no such file under the expectations dir)", name)
    }
    return w.check("shape", comparator.ShapePatternExpectation{Name: name, Clauses: clauses})
}
```

The `!ok` branch is a safety net; the `sc.Before` pre-check (§7) makes the unknown-name
case fail before the SUT runs, so in normal flow `matchesShape` is reached only for a
known name.

## 7. Loading & validation

**When.** `engine.Build` calls `expectations.Load(cfg.Expectations)` after registering
comparators, before returning the `Engine`. The unknown-pattern-name pre-check runs in the
step initializer's `sc.Before`, before the `When` step drives the SUT.

**Directory resolution and the missing-directory case.**

- `config.Load` defaults `cfg.Expectations` to `"expectations"` when the YAML omits it
  (mirroring the `Store` default, `config.go:62-63`). Real CLI runs therefore always have
  it set.
- An **empty** `cfg.Expectations` (a programmatically-built `config.Config{}`, e.g. in
  step/engine tests that bypass `config.Load`) loads **zero patterns** — an explicit opt-out,
  not a directory read.
- A configured directory that **does not exist or is empty** loads **zero patterns and is
  not an error.** *Rationale (recorded so it is not later "hardened" into a regression):*
  the default `expectations/` will be absent in every project that does not use sidecar
  patterns; making the default a hard error would break those projects. The real safety net
  is the unknown-name pre-check — referencing `"foo"` when no pattern `foo` was loaded is a
  hard error at the reference point (§3 stage 3), so a missing or mistyped directory cannot
  silently pass an assertion.
- A directory that exists but contains a **malformed** `*.yaml` (parse error, bad clause,
  bad selector, bad count, missing `name`, empty `clauses`, two discriminators, unknown
  clause key) is a **hard error** from `Load` → `Build` → non-zero exit.

**Duplicate names.** Two files declaring the same `name` is a hard error from `Load`
(naming the two paths), never last-write-wins.

## 8. Errors & verdicts (no silent fallbacks)

**Spec / wiring bugs → hard `error` (crash, wrapped with `%w`, invariant #4):**

- Load time (in `Build`): unreadable directory entry; YAML parse error; missing/blank
  `name`; empty `clauses`; a clause with zero or ≥2 discriminator keys; unknown clause key;
  `count` not `>=N`/`==N`; `count` present on `absent`/`child`/`descendant`; `count` absent
  on `fanout`; `of` missing on `child`/`descendant`; a `Selector` that `ParseSelector`
  rejects; duplicate pattern name.
- Scenario-init time (`sc.Before`): `the run matches shape "X"` where `X` is not loaded.
- Compare time (defense-in-depth): wrong expectation type; empty `Clauses`; a clause that
  fails the shape comparator's own structural validation.

**Behavioural mismatch → `Verdict{Pass:false, Reasons:[…]}`**, aggregating every failing
clause: one `Reasons` element per failing clause (§5). The existing `check`
(`steps.go:154`) joins `Reasons` with `"; "` behind a `shape failed: ` prefix, so all
failures appear in the single step error:

```
shape failed: pattern "fanout-summarize" clause 3 (containment): a span matching {gen_ai.tool.name=summarize} exists, but none is a child of a span matching {gen_ai.operation.name=chat}; pattern "fanout-summarize" clause 5 (fanout): expected a span matching {gen_ai.operation.name=chat} with at least 3 children matching {gen_ai.operation.name=execute_tool}; best matching parent had 1
```

Each clause's underlying reason reuses the shape comparator's existing canonical-form
messages (`shape` spec §9) verbatim, so a clause inside a pattern reads identically to the
same assertion written inline.

## 9. Module layout & wiring (the concrete edits)

- **`internal/config/config.go`:** add `Expectations string \`yaml:"expectations"\`` to
  `Config`; default it to `"expectations"` in `Load`.
- **`internal/expectations/expectations.go`:** the schema structs, `Load(dir) (Patterns, error)`,
  clause translation/validation, and a `Patterns` type with `Get(name) ([]comparator.ShapeExpectation, bool)`.
- **`internal/comparator/shape.go`:** add `ShapePatternExpectation`; extract `evalClause`;
  add the pattern branch to `Compare`.
- **`internal/engine/engine.go`:** add the `patterns` field and `ShapePattern` accessor.
- **`internal/engine/build.go`:** call `expectations.Load(cfg.Expectations)` and store the
  result on the `Engine` (returning any error).
- **`internal/steps/steps.go`:** bind the one new step; add `matchesShape`; extend the
  `sc.Before` scan with the unknown-pattern-name pre-check.
- **`cmd/mentat/main.go`:** no change — it already loads config and surfaces `Build`
  errors as a non-zero exit.

## 10. Testing

- **`internal/expectations` unit tests (table-driven, hermetic, `t.TempDir`):** valid
  single pattern; every clause family round-trips to the expected `ShapeExpectation`;
  `count` `>=N`/`==N` parse; duplicate name across two files; missing `name`; empty
  `clauses`; unknown clause key; two discriminators in one clause; bad count (`>5`, `==x`);
  `count` on `absent`; `fanout` without `count`; `child` without `of`; malformed selector;
  malformed YAML; non-existent dir → empty; empty dir → empty; empty `Expectations` string →
  empty.
- **`internal/comparator` unit tests:** `ShapePatternExpectation` all-clauses-pass;
  multi-clause partial failure aggregates **all** failing reasons in order; a malformed
  clause → `error`; empty `Clauses` → `error`; wrong expectation type → `error`. Assert the
  inline `ShapeExpectation` path is unchanged (existing tests stay green). `internal/comparator`
  stays ≥ 80% coverage.
- **Step / BDD (hermetic, filestore/otlp-file fixture + a loaded pattern):** a `.feature`
  with `Then the run matches shape "<name>"` against a fixture the pattern satisfies
  (passing) and one it does not (red, asserting the aggregated reason surfaces). Build the
  trace as a literal with explicit `ID`/`ParentID` (the shape spec's fixture note — the
  filestore `LoadFixture` leaves `ID=""`).
- **L3 meta-test (mandatory, `e2e/`, `//go:build e2e`):** an `expectations/*.yaml` asserting
  structure the live fixture lacks (e.g. an inverted containment), referenced from a meta
  `.feature`, must make `mentat run` exit non-zero with the aggregated reason in output —
  proving the framework goes red on bad behaviour. Add one row to `e2e/meta_test.go`'s
  `cases`, matching `"shape failed"`.

## 11. Risks & mitigations

- **Missing-directory soft-fallback (§7).** A configured-but-absent dir loading zero
  patterns is the one deliberate non-erroring fallback. It is bounded and justified: the
  default must not break pattern-free projects, and the unknown-name pre-check converts any
  *use* of a missing/mistyped pattern into a hard error. Documented here and in code so it
  is not "fixed" into a regression.
- **Schema creep.** Authors will want parameterized patterns, nested subtrees, multiple
  patterns per file, or richer selectors. §2 draws the boundary explicitly so each is a
  deliberate future addition, not discovered mid-implementation. Matching power lives in the
  shape comparator and its spec; this layer only bundles.
- **Reason verbosity on large patterns.** Aggregating every failing clause can produce a
  long message for a big pattern. Accepted: a complete failure report is more useful than a
  first-failure one (the chosen behaviour), and the `clause N (<kind>)` prefix keeps it
  scannable.
- **Name/file divergence.** `name` being authoritative while the filename is free means a
  file `foo.yaml` can declare `name: bar`. The duplicate-name check and the unknown-name
  pre-check keep this unambiguous; a future lint could warn when they differ, but it is not
  required for correctness.
