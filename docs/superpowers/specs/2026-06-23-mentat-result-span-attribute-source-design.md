# Mentat Result Span-Attribute Source — Design

**Date:** 2026-06-23
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`
**Related:** the master design groups three items under **Phase 3** (§8 lines 389–393,
§13 lines 457–459): the **shape comparator** (shipped, #17), **sidecar expectation YAML**
(shipped, #18), and this — the **`result` comparator's span-attribute source**. This is the
third and final Phase-3 item. It extends the existing `result` comparator
(`internal/comparator/result.go`) rather than adding a new comparator.

## 1. Purpose

Today the `result` comparator asserts on the **driver-captured boundary output** only
(`Evidence.Output`: the agent's final answer / the HTTP response body+status). It cannot
assert on **intermediate, per-tool results** that live inside the trace — e.g. "the
`web_search` tool returned a payload containing `Q3 revenue`", or "the `payments` service
span carried a response body that `json-contains` `{approved: true}`".

The master design names this the second source of the `result` comparator (§8 lines
389–393):

> In **Phase 3** the result comparator gains a second source — **span-attribute results**
> (`gen_ai.tool.call.result`, captured response-body attributes) — so intermediate and
> per-tool results can also be asserted. The expectation selects the target (the final
> result vs. the result of a named tool/span).

This spec commits to that second source, reusing the deterministic matchers
(`exact`/`contains`/`regex`/`json-subset`/`schema`) the comparator already owns and the
span `Selector` the shape comparator already established. **No new comparator, no new seam,
no change to the `core.Matcher` interface.**

## 2. Scope

**In scope:**

- An optional `Source` on `ResultExpectation` selecting a **span-attribute** value instead
  of the boundary output. `Source == nil` preserves today's boundary path byte-for-byte.
- Source resolution inside `result.Compare` (Approach 1 of §7): walk the trace forest by a
  `Selector`, order matches by start time, pick/quantify, extract a named attribute,
  synthesize a derived `Evidence`, and dispatch to the **unchanged** matcher.
- Two Gherkin step families (§4):
  - **Tool convenience form** — selects `gen_ai.tool.name = "X"`, extracts
    `gen_ai.tool.call.result`.
  - **General selector form** — selects a `k=v` span `Selector`, extracts a **named**
    attribute (microservice response-body attrs, etc.).
- **Addressing** for repeated matches (a tool legitimately called N times):
  - *bare* — exactly one match expected;
  - *ordinal* — `first` / `last` / `Nth` by start order;
  - *quantifier* — `every` (all must satisfy, AND) / `any` (≥1 satisfies, OR).
- Matcher verbs for the span source: `contains`, `equals`, `matches regex`,
  `json-contains`, `matches schema`.
- `genai.ToolResult = "gen_ai.tool.call.result"` added to `internal/genai/keys.go`
  (researchbot already emits it; `tracelab/researchbot/attrs.go` is the SUT-side mirror).

**Out of scope (v1):**

- **Sidecar-YAML integration** for span results. The span-attribute source is **inline
  grammar only** for v1, mirroring how `shape` shipped inline first (#17) and gained the
  sidecar separately (#18). A future spec may add named result expectations to
  `expectations/` if demand appears (YAGNI).
- The `status` matcher against a span source. `status` asserts an HTTP/exit **int**, which
  a span attribute is not; a span's OK/ERROR status is reachable via the selector
  (`span.status=ERROR`) or the shape comparator. `status` stays **boundary-only** and is a
  hard error in combination with a span source.
- The `semantic` (LLM-judge) matcher — that is Phase 4 and is unaffected by this change.
- TraceQL pushdown. All matching is in-memory over the already-materialized
  `Evidence.Trace`, exactly like the shape comparator (§7).

## 3. Background: what already exists

This feature is deliberately a thin extension over shipped machinery:

- **`result` comparator** (`internal/comparator/result.go`) — type-asserts a
  `ResultExpectation{Matcher, Want, Target}`, looks up the named `Matcher` in the registry,
  calls `m.Match(ctx, ev, want, target)`. It reads `ev.Output` only.
- **Matchers** (`internal/comparator/matchers.go`) — `exact`, `contains`, `regex`,
  `json-subset` (step `json-contains`), `status`, `schema`. Value matchers route through
  `targetString(target, ev)` (`"answer"` → `ev.Output.Answer`, `"status"` →
  `ev.Output.Status`); structural matchers read `ev.Output.Body`/`Status` directly.
- **`Selector`** (`internal/comparator/shape_selector.go`) — `[]Pred{Key,Value}`,
  AND-ed exact-equality. `ParseSelector("k1=v1, k2=v2")` parses the quoted conjunction with
  hard errors for empty/malformed clauses and unknown reserved `span.*` keys.
  `spanValue(sp, key)` resolves reserved `span.name/status/kind` to intrinsics and any other
  key to an attribute lookup. `matchSpan` uses **filter** semantics (missing attr → "",
  so → non-match, not an error).
- **`matchingSpans(tr, sel)`** (`internal/comparator/shape.go`) — returns every span in the
  forest (`tr.Spans`) satisfying a selector.
- **Start ordering** — `trace.Trace.ByOp` establishes the idiom:
  `sort.SliceStable(out, func(i,j int) bool { return out[i].Start.Before(out[j].Start) })`.
  Stable sort preserves emit order for timestamp-free fixtures.
- **`Span.Attr(k)`** preserves every ingested attribute, so `gen_ai.tool.call.result` and
  arbitrary response-body attributes are already present on spans when the SUT emits them —
  the data path exists; this feature only adds the read path.

## 4. Grammar

Two new step families on the `result` comparator. The **span-spec slot** (which spans) is a
single regex group; the **matcher verb** (how to compare) is the suffix — so every matcher
composes with every addressing form from a small number of step regexes.

### 4.1 Tool convenience form

Selects spans where `gen_ai.tool.name = "X"`; extracts `gen_ai.tool.call.result`.

```gherkin
# string-arg matchers: contains | equals | matches regex
the result of tool "web_search" contains "Q3 revenue"
the result of the first call to tool "web_search" equals "<exact payload>"
the result of the 3rd call to tool "web_search" matches regex "^\d+$"
the result of every call to tool "web_search" contains "http"
the result of any call to tool "web_search" contains "cache-hit"

# docstring matchers: json-contains | matches schema
the result of the last call to tool "lookup" json-contains:
  """
  { "status": "ok" }
  """
the result of tool "lookup" matches schema:
  """
  { "type": "object", "required": ["status"] }
  """
```

### 4.2 General selector form

Selects spans matching a `k=v` `Selector` (reserved `span.*` keys allowed); extracts a
**named** attribute.

```gherkin
attribute "http.response.body" of the span matching "service.name=payments" contains "approved"
attribute "gen_ai.tool.call.result" of the last span matching "gen_ai.tool.name=web_search" json-contains:
  """
  { "hits": 3 }
  """
attribute "span.status" of every span matching "service.name=cart" equals "OK"
```

### 4.3 The span-spec slot

In both families the span-spec slot is exactly one of:

| Form | Tool phrasing | Selector phrasing | Meaning |
|---|---|---|---|
| bare | `tool "X"` | `the span matching "SEL"` | exactly one match expected |
| ordinal | `the {first\|last\|Nth} call to tool "X"` | `the {first\|last\|Nth} span matching "SEL"` | one span, start order |
| quantifier-all | `every call to tool "X"` | `every span matching "SEL"` | all matches must satisfy (AND) |
| quantifier-any | `any call to tool "X"` | `any span matching "SEL"` | ≥1 match satisfies (OR) |

`Nth` accepts ordinal words `first`/`last` and numeric ordinals `1st`/`2nd`/`3rd`/`4th`/…
Ordinals are **1-based** over the start-ordered match list.

### 4.4 Step regexes (illustrative)

The variable parts (span-spec, name/selector, matcher arg) are capture groups; one regex
per `(family × matcher-arg-shape)`:

```
# tool form, string-arg matchers
^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (contains|equals|matches regex) "([^"]*)"$
# tool form, docstring matchers
^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (json-contains|matches schema):$
# selector form, string-arg matchers
^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?span matching "([^"]+)" (contains|equals|matches regex) "([^"]*)"$
# selector form, docstring matchers
^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?span matching "([^"]+)" (json-contains|matches schema):$
```

A missing span-spec group (the `(?:…)?` did not match) is `bare` → `QuantOne`. The step
function maps the matcher verb to the registered matcher name (`contains`→`contains`,
`equals`→`exact`, `matches regex`→`regex`, `json-contains`→`json-subset`,
`matches schema`→`schema`).

## 5. Data model

```go
// ResultExpectation (extended). Source == nil preserves the boundary path exactly.
type ResultExpectation struct {
    Matcher string      // exact | contains | regex | json-subset | schema | status
    Want    string
    Target  string      // boundary only: "answer"(default) | "status"; ignored when Source != nil
    Source  *SpanSource // nil => driver Output (default); set => span-attribute source
}

// SpanSource selects a span-attribute result value.
type SpanSource struct {
    Selector Selector // tool form compiles to {gen_ai.tool.name = X}
    Attr     string   // tool form => genai.ToolResult; selector form => the named attr
    Quant    Quant    // how to resolve multiple matches
    Index    int      // QuantNth only, 1-based
}

type Quant int
const (
    QuantOne   Quant = iota // bare: exactly one match (else hard error)
    QuantFirst             // first by start order
    QuantLast              // last by start order
    QuantNth               // Index-th by start order
    QuantEvery             // all matches must satisfy (AND)
    QuantAny               // ≥1 match satisfies (OR)
)
```

The step layer builds `Source`. The tool form sets
`Selector{{Key: genai.ToolName, Value: "X"}}` and `Attr: genai.ToolResult`. The selector
form sets `Selector = ParseSelector("SEL")` and `Attr = "<named attr>"`.

## 6. Resolution algorithm (`result.Compare`, `Source != nil`)

1. **Guard.** `ev.Trace == nil` → hard error (`result: Evidence.Trace is nil`).
2. **Match.** `spans := matchingSpans(ev.Trace, Source.Selector)` (reuse shape's forest
   walk). The selector uses **filter** semantics (`matchSpan`): a missing predicate
   attribute is a non-match, never an error.
3. **Order.** Stable-sort `spans` by `Start` (the `ByOp` idiom).
4. **Select** target span(s) by `Quant`:
   - `QuantOne` — require `len(spans) == 1`; `0` → "matched no spans", `>1` → "ambiguous".
   - `QuantFirst` / `QuantLast` — `spans[0]` / `spans[len-1]`; `0` → "matched no spans".
   - `QuantNth` — `spans[Index-1]`; `0` → "matched no spans", `Index > len` → "out of range".
   - `QuantEvery` / `QuantAny` — keep the whole set; `0` → "matched no spans".
5. **Extract** `Source.Attr` from each selected span via `spanValue` (reserved `span.*` →
   intrinsics, else attribute). A reserved-key intrinsic is always present; for a
   non-reserved key, **absence is a hard error** ("span … has no attribute …") — this is
   *extraction* semantics, not the filter semantics of step 2.
6. **Synthesize + dispatch.** For each selected span build a derived `core.Evidence` =
   shallow copy of `ev` with `Output.Answer` and `Output.Body` both set to the extracted
   attribute value, then call `matcher.Match(ctx, derived, exp.Want, "answer")` (the
   `Target` argument is forced to `"answer"`; `exp.Target` is not consulted under a span
   source).
   - Value matchers (`exact`/`contains`/`regex`) read the synthesized `Answer` via
     `targetString("answer", …)`.
   - Structural matchers (`json-subset`/`schema`) read the synthesized `Body`.
   - The matcher is **unchanged** and never learns the value came from a span.
7. **Combine** verdicts:
   - `One`/`First`/`Last`/`Nth` — return the single matcher verdict; on failure, prefix the
     reason with the span identity (tool name + ordinal/index, or selector + attr).
   - `Every` — **AND**: pass iff all pass; collect a reason per failing span.
   - `Any` — **OR**: pass iff ≥1 passes; if none pass, collect all per-span reasons.

The `status` matcher with `Source != nil` is rejected up front (§7 / §2 out-of-scope).

## 7. Why Approach 1 (alternatives considered)

Three ways to thread the span source through the matcher seam were weighed:

- **Approach 1 — resolve source in `result.Compare`, synthesize derived `Evidence`
  (chosen).** The comparator owns "which value"; matchers own "how to compare." Zero
  `core.Matcher` change (and no mock regen); all content matchers
  (`exact`/`contains`/`regex`/`json-subset`/`schema`) work against a span result for free —
  `json-subset`/`schema` over a tool's JSON result is the high-value case; quantifier
  AND/OR logic lives in exactly one place; resolution failures are hard errors
  (invariant #4). Mild cost: `Output` is reused as a value carrier, and `status` doesn't map
  (excluded by design).
- **Approach 2 — change `core.Matcher` to take a resolved `Value`.** Arguably the cleanest
  long-term contract, but a breaking change to the interface, all six matchers, the
  generated mocks, and the Phase-4 semantic matcher that will share the seam. Large blast
  radius for identical capability. Rejected.
- **Approach 3 — encode the source in the `target` string** (e.g. `tool:web_search#last`).
  Smallest signature change, but cramming selector+ordinal+attribute into one opaque string
  is brittle, and **quantifiers (`every`/`any`) cannot be expressed** as a single target
  returning one value — they need multi-span iteration the matcher cannot do. Fails the
  chosen addressing model. Rejected.

## 8. Error handling (invariant #4 — no silent fallback)

Returned as `error` (author/config bug or trace defect), never a false verdict:

| Condition | Message shape |
|---|---|
| `Trace` nil under a span source | `result: Evidence.Trace is nil` |
| Zero matches (any Quant) | `result: tool "web_search" matched no spans` / `result: selector {…} matched no spans` |
| Ambiguous bare (`One`, N>1) | `result: tool "web_search" matched 3 spans; use first/last/Nth, or every/any` |
| Nth out of range | `result: 3rd call to tool "web_search" out of range (2 calls)` |
| Missing result attribute | `result: span {gen_ai.tool.name=web_search} has no attribute "gen_ai.tool.call.result"` |
| `status` matcher + span source | `result: status matcher is boundary-only; not valid with a span source` |
| Selector parse error | from `ParseSelector` (e.g. `shape: predicate "x" missing '='`) |
| Unknown matcher / bad regex / invalid schema | existing matcher errors, unchanged |

A value that is present but does not match the matcher is a normal
`Verdict{Pass:false, Reasons:[…]}` naming the span and the computed-vs-expected — **not** an
error. The dividing line: *the trace/spec is wrong* → `error`; *the SUT behaved wrongly* →
failing `Verdict`.

**Zero-match is always a hard error**, including for `every`/`any`. Logical vacuous-truth
("every member of the empty set passes") is almost always a masked mistake — asserting about
a tool that was never called. Surfacing it distinctly (invariant #4) beats a silent pass.

## 9. The semantic distinction (selector filter vs attribute extraction)

A single key plays two roles, with deliberately different missing-value semantics:

- **Selector predicate** (step 2) — *filter*. A missing attribute yields `""`, which does
  not equal a non-empty predicate value, so the span is simply not selected. This is
  `matchSpan`'s shipped behaviour and mirrors the shape comparator.
- **Result attribute** (step 5) — *extraction*. The attribute is the value under test; its
  absence on an otherwise-selected span is a hard error, mirroring the `sequence`
  comparator's missing-tool-name handling (`sequence: span[%d] (%q) missing …`).

This distinction is the one subtle invariant a reviewer must hold: filter-missing is a
non-match; extraction-missing is an error.

## 10. Components & files

| File | Change |
|---|---|
| `internal/genai/keys.go` | add `ToolResult = "gen_ai.tool.call.result"` |
| `internal/comparator/result.go` | extend `ResultExpectation`; add `SpanSource`, `Quant`; span-source resolution + quantifier combination in `Compare` |
| `internal/comparator/result_span_test.go` (new) or extend `result_test.go` | unit tests (§11) |
| `internal/steps/steps.go` | register the four new step regexes; step funcs build `ResultExpectation{Source:…}` |
| `internal/steps/steps_test.go` | step-mapping tests |
| `internal/steps/testdata/*.feature` + L3 meta-test | red-on-bad-behaviour scenario |
| `e2e/*` | `//go:build e2e` scenario exercising the tool form against a researchbot trace |

No change to `core.Matcher`, the matcher registry, the composition root (`engine.Build`),
or any generated mock.

## 11. Testing

Per `CLAUDE.md` (go-test-writer owns the red→green→refactor loop; go-reviewer gates):

- **Unit, table-driven** (`internal/comparator`):
  - each `Quant` (`One`/`First`/`Last`/`Nth`/`Every`/`Any`) × representative matchers;
  - `Every` AND (one failing span fails the verdict) and `Any` OR (one passing span passes);
  - missing-attribute → error; zero-match → error; ambiguous-bare → error;
    Nth-out-of-range → error;
  - selector form with a named attribute; reserved `span.*` extraction
    (e.g. `span.status`);
  - `status` + span-source → error;
  - boundary path unchanged (a `Source == nil` regression case).
  - `t.Parallel()` where no shared mutable state (soft default).
- **Step layer** (`internal/steps/steps_test.go`): each new regex → the expected
  `ResultExpectation{Source:…}` (right `Selector`, `Attr`, `Quant`, `Index`).
- **L3 meta-test (mandatory):** a scenario that *should* go red — e.g.
  `the result of the last call to tool "X" contains "Z"` against a fixture where the last
  result is not `Z` — asserting Mentat reports failure. A test framework must prove it goes
  red on bad behaviour.
- **e2e** (`//go:build e2e`): exec the prebuilt `mentatBin`; tool form against a live
  researchbot trace; `t.Parallel()` (top + each `t.Run`) per the hermetic-suite convention.
- **Coverage:** ≥80% per touched package (`comparator`, `steps`), via the `coverage` skill.

## 12. Routing

Feature work (behaviour change) → **go-test-writer** (TDD). The one scaffolding sliver — the
`genai.ToolResult` constant — is trivial and can ride along in the first red→green cycle.
Pre-commit audit → **go-reviewer** (`gate`).
