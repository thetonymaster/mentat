# Mentat Shape Comparator — Design

**Date:** 2026-06-22
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`
**Related:** the master design phases this as **Phase 3** (§4 line 122, §8 lines 395–399,
§13 line 457: "shape (TraceQL-backed): required spans present, forbidden spans absent,
parent/child structure, fan-out counts"). The other two Phase-3 items the master design
groups alongside shape — a **span-attribute result source** (the `result` comparator
reading per-span attrs) and **sidecar expectation YAML** (`expectations/` +
`Then the run matches shape "<name>"`) — are **deferred to their own future specs**;
this spec is the shape comparator and its inline Gherkin grammar only. Built as a
structural sibling of the existing `sequence` comparator (`internal/comparator/sequence.go`).

## 1. Purpose

`sequence` asserts the **flat order** of tool/service calls; `budgets` asserts
**aggregates** (tokens/cost/latency); `result` asserts the **output**. None of them can
assert the **tree structure** of a run: that a span exists at all, that one span is
nested under another, or that a span fanned out into N children. These are exactly the
questions that matter for agent traces (a planner span that spawned ≥3 tool calls) and
microservice traces (the payment service was invoked *under* the order service).

The master design names a `shape` comparator for this and notes it is "TraceQL-backed …
falls back to in-memory tree matching for what TraceQL cannot express" (§8 lines 397–399).
This spec commits to **in-memory tree matching only** for v1 (see §8) and leaves TraceQL
as a reserved, comparator-invisible optimization.

## 2. Scope

**In scope:**

- A `shape` `core.Comparator` in `internal/comparator/shape.go` (`NewShape()`,
  `Name() → "shape"`, `Compare`), registered at the composition root
  (`engine/build.go`) as `"shape"`.
- A **span selector** model: a conjunction of exact-equality `key=value` predicates over
  span attributes and the intrinsic span fields name/status/kind (reserved `span.*` keys).
- Three structural assertion families, one assertion per Gherkin step:
  - **existence / count** — a span matching a selector exists / is absent / occurs
    exactly-or-at-least N times;
  - **containment** — a span matching a selector is a (direct) **child of** or a
    **descendant of** a span matching another selector;
  - **fan-out** — a span matching a selector **has at least / exactly N children**
    (or descendants) matching another selector.
- Focused English Gherkin steps in `internal/steps/steps.go` that build a
  `ShapeExpectation` and call `w.check("shape", exp)`.
- Hermetic table-driven unit tests, a `.feature` exercising the steps, and the
  mandatory **L3 meta-test** proving Mentat goes red on a wrong shape assertion.

**Out of scope (deferred, by design):**

- **Span-attribute result source** and **sidecar expectation YAML** — the other two
  Phase-3 items; separate future specs.
- **Sibling ordering** ("under Y, A precedes B"). Tempo/TraceQL cannot express it and it
  overlaps `sequence`; deferred until a concrete need.
- **TraceQL pull-down.** Matching is in-memory only; `core.StoreCaps.StructuralQuery`
  stays reserved and unread by the comparator (§8).
- **Richer selector *operators*:** wildcard/glob/regex values, numeric inequalities,
  attribute presence-without-value, OR/NOT. v1 is exact string equality, AND-ed. (Intrinsic
  span fields name/status/kind *are* selectable — that is reach, not an operator; §4.)
- **"For all matching parents" fan-out** and **descendant fan-out.** v1 fan-out is
  existential over the parent (§7) and counts direct children only; a universal variant
  and a descendant-counting variant are future options.

## 3. Architecture & placement

The comparator consumes **`Evidence` only** (invariant #1). It reads `ev.Trace`
(a `*trace.Trace` forest) and the run's spans; it never touches a `TraceStore` or
`Driver`. Construction and registration mirror `sequence` exactly:

```go
func NewShape() core.Comparator { return shape{} }
func (shape) Name() string      { return "shape" }
func (shape) Compare(ctx context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error)
```

`engine/build.go` gains one line beside the others:

```go
registry.RegisterComparator("shape", comparator.NewShape())
```

`Trace` is a **forest** (invariant #2): `Trace.Spans` is the flat list, `Trace.Roots`
the roots. The comparator builds, once per `Compare`, an id→span index and a
`parentID → []child` index from `Trace.Spans`, and walks `ParentID` links for ancestry.
Roots (spans with empty `ParentID`, or whose parent is absent from the trace) terminate
upward walks. Nothing assumes a single root.

## 4. Span selector

```go
// Pred is one exact-equality predicate. Key resolves to either an intrinsic span field
// (the reserved keys span.name / span.status / span.kind) or, for any other key, the
// span's attribute map.
type Pred struct{ Key, Value string }

// Selector matches a span iff every predicate holds (exact string equality, AND-ed).
// An empty Selector is invalid (it would match every span).
type Selector []Pred
```

Selectors are written in Gherkin as a quoted, comma-separated list of `key=value`:
`"gen_ai.operation.name=execute_tool, gen_ai.tool.name=search"`. Parsing splits on `,`,
trims each clause, and splits each clause on the **first** `=` (values may contain
`=`; keys may not be empty). `service.name` and other resource attributes are selectable
because the store (Tempo and the fixture loader) merges resource attributes onto every
span — the same property `sequence` relies on (`sequence.go:13–16`).

**Reserved intrinsic-field keys.** A `trace.Span` carries three fields outside its
attribute map — `Name`, `Status`, `Kind`. The selector reaches them through reserved keys
under the `span.` prefix:

| Key | Resolves to |
|---|---|
| `span.name` | `Span.Name` |
| `span.status` | `Span.Status` (e.g. `ERROR`) |
| `span.kind` | `Span.Kind` |

Any other key is an attribute lookup. The `span.` prefix is **reserved**: a key beginning
`span.` that is not one of the three above (e.g. a typo `span.staus`) is a **hard error**,
never a silently-never-matching attribute lookup (invariant #4 — no silent fallback). This
unlocks structural status/kind assertions — e.g. "no `ERROR` span under the payment
service" — that `budgets`' global `MaxErrors` cannot localize.

**Missing attribute → non-match (not an error).** If a span lacks the predicate's key,
`span.Attr(key)` returns `""`, which is unequal to any non-empty `value`, so the span
simply does not match. This **deliberately diverges** from `sequence`, which treats a
missing identity attribute on a relevant span as a hard error (`sequence.go:99`). The
difference is intentional: `sequence` *extracts an identity* from spans it has already
decided are relevant, so a missing identity is corruption; a shape `Selector` is a
*filter* applied across the whole forest, so "this span lacks the attribute" is a normal
negative, not corruption. (Chesterton's-fence note recorded so the divergence is not
later "fixed" into an inconsistency.)

## 5. Expectation contract

One Gherkin step produces one assertion, matching the per-step `w.check` pattern every
other comparator already uses.

```go
type ShapeExpectation struct {
    Kind     string   // "exists" | "absent" | "containment" | "fanout"
    Subject  Selector // the span being asserted about (the matched span / the child)
    Parent   Selector // containment & fanout: the container span; empty otherwise
    Relation string   // containment: "child" (direct) | "descendant". Fan-out is direct children only in v1.
    Count    *Count   // exists & fanout cardinality; nil ⇒ "at least 1"
}

// Count is a cardinality constraint. Op ∈ {">=", "=="} — the two forms the grammar
// emits ("at least N" / "exactly N"). No step emits "<=", so it is not in v1.
type Count struct {
    Op string
    N  int
}
```

`Compare` type-asserts `e.(ShapeExpectation)` and returns a descriptive error on
mismatch, exactly as `sequence` does for `SequenceExpectation` (`sequence.go:32–35`).

## 6. Gherkin grammar (focused English steps)

```gherkin
# existence / count
Then a span matching "gen_ai.tool.name=search" exists
Then no span matching "gen_ai.tool.name=delete" exists
Then at least 2 spans match "gen_ai.operation.name=execute_tool"
Then exactly 1 span matches "service.name=payment"
Then no span matching "span.status=ERROR, service.name=payment" exists

# containment
Then a span matching "service.name=payment" is a child of a span matching "service.name=order"
Then a span matching "gen_ai.tool.name=search" is a descendant of a span matching "gen_ai.operation.name=chat"

# fan-out
Then a span matching "gen_ai.operation.name=chat" has at least 3 children matching "gen_ai.operation.name=execute_tool"
Then a span matching "gen_ai.operation.name=chat" has exactly 2 children matching "gen_ai.tool.name=search"
```

Each step maps to one `ShapeExpectation`:

| Step | Kind | Subject | Parent | Relation | Count |
|---|---|---|---|---|---|
| `a span matching "S" exists` | exists | S | — | — | nil (≥1) |
| `no span matching "S" exists` | absent | S | — | — | — |
| `at least N spans match "S"` | exists | S | — | — | {">=", N} |
| `exactly N spans match "S"` | exists | S | — | — | {"==", N} |
| `…"C" is a child of …"P"` | containment | C | P | child | — |
| `…"C" is a descendant of …"P"` | containment | C | P | descendant | — |
| `…"P" has at least N children matching "C"` | fanout | C | P | child | {">=", N} |
| `…"P" has exactly N children matching "C"` | fanout | C | P | child | {"==", N} |

(Fan-out counts **direct children only** in v1. A descendant fan-out phrasing is a future
addition; containment already covers "somewhere under" via its `descendant` relation.)

## 7. Matching semantics (existential, precise)

Let `match(sel)` = the set of forest spans satisfying selector `sel`.

- **exists / count:** evaluate `|match(Subject)|` against `Count` (default `>= 1`).
- **absent:** pass iff `|match(Subject)| == 0`.
- **containment, child:** pass iff ∃ `c ∈ match(Subject)`, ∃ `p ∈ match(Parent)` with
  `c.ParentID == p.ID`.
- **containment, descendant:** pass iff ∃ `c ∈ match(Subject)`, ∃ `p ∈ match(Parent)`
  where `p` is an ancestor of `c` (walking `c`'s `ParentID` chain up to a root yields
  `p.ID`).
- **fan-out:** pass iff **∃ a single** `p ∈ match(Parent)` whose set of **direct
  children** intersected with `match(Subject)` satisfies `Count`.

The fan-out reading is **existential over the parent**: "a span matching P has ≥3
children matching C" is true when *at least one* P-matching span did. This is the natural
reading of the English ("the planner span spawned ≥3 tool calls") and is the v1
semantics; a "for all P-matching parents" variant is deferred (§2).

All evaluation is forest-aware and order-independent: the comparator inspects structure
(`ParentID`), never start-time order (that is `sequence`'s job).

## 8. Matching engine — in-memory only; TraceQL reserved

Three options were considered:

- **A. In-memory tree matching (chosen).** The trace is already fully materialized in
  `Evidence.Trace`, so matching in Go costs nothing extra, behaves identically for
  filestore fixtures (hermetic tests) and live Tempo, and keeps the comparator
  Evidence-only (invariant #1). `StoreCaps.StructuralQuery` stays reserved and unread.
- **B. TraceQL pre-filter + in-memory verify.** Rejected: the trace is already in memory,
  so a comparator-side pre-filter saves nothing, and pushing the filter to the store
  would force the comparator (or a new resolve-layer stage) to depend on the store —
  a much larger change for no v1 benefit. Premature optimization.
- **C. Pure TraceQL.** Rejected: breaks hermetic tests (filestore has no TraceQL), couples
  the comparator to the store, and cannot express fan-out cardinality cleanly.

If a future need arises to narrow *which traces are fetched* before materialization, that
belongs in the **resolve/store layer** behind `StoreCaps.StructuralQuery`, where it can be
added without the comparator ever knowing. The comparator contract is unaffected.

## 9. Errors & verdicts (no silent fallbacks)

**Behavioural failure** (the run did not have the asserted shape) →
`core.Verdict{Pass: false, Reasons: [...]}` with concrete, value-bearing reasons:

- exists/count: `"expected at least 2 spans matching {gen_ai.operation.name=execute_tool}, found 1"`
- absent: `"forbidden span matching {gen_ai.tool.name=delete} was present (1 occurrence)"`
- containment: `"a span matching {service.name=payment} exists, but none is a child of a span matching {service.name=order}"`
- fan-out: `"expected a span matching {…=chat} with at least 3 children matching {…=execute_tool}; best matching parent had 1"`

**Spec / wiring bug** → `error` (crash, wrapped with `%w`, invariant #4), never a false
pass: `ev.Trace == nil`; empty or malformed `Selector` (empty clause, missing `=`, empty
key); an unknown reserved `span.*` key; unknown `Kind`; negative `Count.N`; unknown
`Relation`. These are author errors in the `.feature` or the step wiring, not behaviours
of the SUT.

Selector formatting in reasons uses a stable canonical form (`{k1=v1, k2=v2}` in
declared order) so failure messages are deterministic.

## 10. Testing

- **Unit (`internal/comparator/shape_test.go`), table-driven, hermetic.** Construct
  `trace.Trace` literals (or reuse `fixtures_test.go` helpers) covering: each Kind ×
  pass/fail; direct-child vs descendant; fan-out `>=`/`==` boundaries (N-1, N, N+1);
  multi-root forest (the asserted parent and child live under different roots → containment
  fails); missing-attr non-match; conjunction selector requiring both predicates;
  malformed-selector error; nil-trace error; unknown-Kind error. No mocks needed — the
  comparator is pure over `Evidence`.
- **Step / BDD.** A hermetic `.feature` (filestore/otlp-file fixture) exercising one step
  of each family against a known trace.
- **L3 meta-test (mandatory, `e2e/`).** A scenario asserting a shape the fixture does
  **not** have (e.g. a child-of relationship that is actually inverted) must make Mentat
  exit non-zero, with the failure reason surfaced in output — proving the framework goes
  red on bad behaviour.
- **Coverage.** `internal/comparator` stays ≥ 80% (it is already well above; the new file
  must not drop it).

## 11. Risks & mitigations

- **Selector operator creep.** Users will want globs/regex/inequalities. v1 says exact
  equality, AND-ed (§2), so the boundary is explicit rather than discovered
  mid-implementation. Note the CEL escape hatch (`the run satisfies`) is **not** a fallback
  here: its environment exposes only flat `tools`/`services` name lists and output/aggregate
  scalars (`internal/cel/cel.go:13–55`), **not** per-span attributes or tree structure — so
  the shape comparator is the *only* place arbitrary span attributes become assertable. That
  makes a thin, exact-match selector a deliberate scoping choice, not a gap papered over by
  CEL; richer value logic, if ever needed, extends shape (or CEL's span model) on its own
  evidence.
- **Fan-out semantics surprise.** The existential-over-parent reading (§7) is documented
  in both the spec and the failure messages ("best matching parent had N") so a user who
  expected universal semantics sees why it passed/failed.
- **Divergence from `sequence` on missing attributes.** Documented in §4 with rationale to
  prevent a future "consistency fix" that would break filter semantics.
