# Mentat v1 Gap Closure — Design

**Date:** 2026-06-18
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`
**Related:** `2026-06-18-mentat-seam-registries-design.md` (Spec B — sequence A1 after it)

## 1. Purpose

Phases 1–2 and the CEL engine shipped, but five capabilities the v1 design called
for never reached the code. This spec closes that gap — it adds nothing the original
design did not already promise; it makes the implementation match it.

The five gaps, with evidence:

| # | Gap | Designed in | Reality today |
|---|---|---|---|
| A1 | `schema` result matcher (JSON-Schema validation) | main spec §8 (v1 deterministic matcher) | `internal/comparator/result.go:26` matchers are `exact \| contains \| regex \| json-subset \| status` — no `schema` |
| A2 | Cost pricing-table fallback | main spec §14/§16 (derive cost from `tokens × pricing` when `cost_usd` absent) | `internal/comparator/budgets.go:130` hard-errors "cost not available… add a pricing table"; `internal/config/config.go` has no pricing field |
| A3 | `mentatctl service` CLI | main spec §15 (service CLI mirrors `agent` at Phase 2) | `cmd/mentatctl/main.go:21` hard-requires `os.Args[1] == "agent"`; the http driver only runs via `mentat run` |
| A4 | `regex` Gherkin step | main spec §17 grammar | `result.go:71` implements the `regex` matcher, but no step in `internal/steps/steps.go` exposes it — a dead capability |
| A5 | traceparent complement | main spec §5 (optional complement minted alongside baggage) | `core.go:64` keeps a `PrimaryTraceID` field that nothing populates |

## 2. Scope

**In scope:** A1–A5 below.

**Out of scope (later phases, unchanged):**

- `shape` comparator + sidecar YAML + span-attribute result source — Phase 3.
- `semantic` matcher + `Judge` + `@runs(N)` — Phase 4.
- `grpc`/`mcp`/`tracetest` drivers, `jaeger` store — Phase 5.
- A file-reference form of the schema step (`…matches schema file "x.json"`) — additive
  later against a concrete need; the docstring form (A1) ships first.

**New dependency:** `github.com/santhosh-tekuri/jsonschema/v6` — actively maintained,
supports JSON Schema draft 2020-12 and draft-07. One direct dependency; pure Go, no
cgo. Accepted in exchange for a standard, well-specified validator over a hand-rolled
type/required checker.

## 3. A1 — `schema` result matcher

A new deterministic matcher that validates the run's structured output against a
JSON Schema.

- **Source.** Reads `ev.Output.Body` (the http response body), exactly like
  `json-subset`. `Target` is not consulted — documented alongside the existing
  `json-subset`/`status` note at `result.go:18-24`.
- **Behaviour.** Compile the schema in `Want`; an **invalid schema is a hard error**
  (`result: schema: invalid JSON Schema: …`) — never a silent pass (invariant 4).
  Validate the body; on failure the `Verdict.Reasons` carries the validator's
  per-instance errors (e.g. `body: /total: expected number, got string`), not a bare
  "schema mismatch".
- **Empty/invalid body.** An empty body validated against a non-trivial schema fails
  with a descriptive reason; a body that is not valid JSON is a hard error
  (`result: schema: response body is not valid JSON: …`), mirroring the CEL `body`
  decision — no guessed `{}`.
- **Grammar.** `internal/steps/steps.go` gains:

  ```gherkin
  Then the response body matches schema:
    """
    { "type": "object", "required": ["orderId","total"],
      "properties": { "total": { "type": "number" } } }
    """
  ```

  This maps to `ResultExpectation{Matcher: "schema", Want: <docstring>}`, the analog
  of the existing `the response body json-contains:` step.

**Sequencing.** If Spec B (the matcher registry) lands first — recommended — `schema`
is implemented as a registered `core.Matcher`, not a new `switch` case. If A1 lands
first, it is a `switch` case in `result.go` that Spec B later migrates. Either order
produces identical behaviour; §9 records the recommended order.

## 4. A2 — cost pricing-table fallback

When no span carries `gen_ai.usage.cost_usd`, `budgets` (and CEL's `cost` variable)
currently hard-error. The design's fallback is to derive cost from token counts and a
per-model pricing table in `mentat.yaml`.

### 4.1 Config

```yaml
pricing:
  claude-opus-4-8:   { inputPerMTok: 15.0, outputPerMTok: 75.0 }
  claude-sonnet-4-6: { inputPerMTok: 3.0,  outputPerMTok: 15.0 }
```

`config.Config` gains a `Pricing` field (yaml-tagged). Following the established
`config.HTTP` ↔ `core.HTTPSpec` mirroring (`core.go:54-55`), the engine converts it
to a transport-free `core.Pricing` so the comparator layer keeps importing only
`core`/`genai`/`trace`, never `config`:

```go
// internal/core
type ModelRate struct { InputPerMTok, OutputPerMTok float64 }
type Pricing   map[string]ModelRate // keyed by model name
```

### 4.2 New attribute key

`internal/genai/keys.go` gains `RequestModel = "gen_ai.request.model"` — the per-span
model used to look up a rate.

### 4.3 Derivation (per span, with precedence)

`costSum` is generalised to take the pricing table. For each span:

1. If the span carries a valid `gen_ai.usage.cost_usd` → **use the emitted value**
   (current behaviour; emitted cost always wins).
2. Else, if the span carries token attributes (`input_tokens`/`output_tokens`) — i.e.
   it is an LLM call — derive:
   `cost += in/1e6 × inputPerMTok + out/1e6 × outputPerMTok`, where the rate comes
   from `pricing[span.Attr(RequestModel)]`.
3. A token-bearing span with no emitted cost **and** whose model is empty or absent
   from the table → **hard error** naming the span and model
   (`budgets: span[%d] (%q): cannot derive cost: model %q not in pricing table`).
   No default rate (per the per-model decision).
4. Spans with neither cost nor tokens (e.g. tool spans) contribute 0 — they are not
   LLM calls.

When `pricing` is empty/unconfigured, behaviour is exactly as today: sum emitted
`cost_usd`, hard-error if none present. The error text at `budgets.go:130` already
points the user at "add a pricing table" — this makes that advice real.

### 4.4 Plumbing (second-order effects, flagged)

- `costSum(t)` → `costSum(t, core.Pricing)`.
- `comparator.NewBudgets()` → `NewBudgets(core.Pricing)`; `comparator.NewCEL()` →
  `NewCEL(core.Pricing)` (CEL's `cost` var reuses the same aggregation per the CEL
  spec §5, so it must derive identically — single source of truth).
- `engine.Build` (`build.go:22-25`) passes `cfg`-derived pricing into both
  constructors.
- Tests that construct `NewBudgets()`/`NewCEL()` directly update to pass a (possibly
  empty) table.

## 5. A3 — `mentatctl service` (full mirror)

Refactor the single-domain dispatcher into `mentatctl <agent|service> <verb>`.

- **Dispatch.** `main.go:21` stops hard-requiring `agent`. `domain ∈ {agent, service}`
  is parsed first, then the verb. Usage: `mentatctl service <run|trace|services|replay|diff>`.
- **Shared verbs (domain-agnostic):** `run`, `trace`, `replay`, `diff` reuse the
  existing `ctl.Run`/`ctl.Resolve`/`ctl.FormatForest`/`ctl.ReplayFeature` paths —
  the engine already drives the `http` adapter, so `service run` needs no new drive
  logic, only an http `--target` from `mentat.yaml`.
- **Domain-specific verb:** `tools` (agent) vs **new `services`** (service). A new
  `ctl.FormatServices` mirrors `ctl.FormatTools` but lists the service-call sequence
  by `service.name` first-seen start, reusing the `sequence` comparator's
  `Kind:"service"` selection (same source of truth as the `services` CEL variable).
- **`diff` becomes domain-aware:** agent diff compares tool sequences (today's
  behaviour); service diff compares service sequences via the same `Kind:"service"`
  selection.
- **Structure.** Extract the shared verb handling so `agent` and `service` differ only
  in the selection helper they pass — no duplicated drive/resolve/replay code.

## 6. A4 — `regex` Gherkin step

Pure grammar wiring; the matcher already exists (`result.go:45,71`).

```gherkin
Then the result matches regex "<re>"
```

maps to `ResultExpectation{Matcher: "regex", Want: <re>}` (default `Target:"answer"`,
consistent with `the result contains`/`the result equals`). A bad regex remains the
existing hard error (`result: bad regex …`).

## 7. A5 — traceparent: document and reserve (not built)

`PrimaryTraceID` (`core.go:64`) is never populated. This is closed by **deciding it
explicitly**, not by adding an unused feature:

- **Rationale.** Baggage tag-first correlation is the *invariant* — it survives the
  SUT starting its own root trace, which `traceparent` alone cannot (main spec §5).
  `PrimaryTraceID` is a pure optimisation (a clean primary trace id when a SUT adopts
  an injected `traceparent`), and nothing in `correlate.Resolve` consumes it. Building
  injection now is a feature with no consumer (YAGNI).
- **Action.** Document `PrimaryTraceID` in `core.go` as *reserved for a future
  traceparent complement; intentionally unset under the baggage-only correlation
  path*. Add/keep a test asserting drivers do **not** inject `traceparent`
  (`internal/driver/http_test.go:77` already does; add the shell equivalent).
- **Trigger to revisit.** When a second correlator (a traceparent complement) is
  actually built, it populates this field and `correlate.Resolve` gains a fast-path
  that prefers it — at which point the correlator registry in Spec B §6 also becomes
  justified.

## 8. Testing

- **A1:** L1 unit + comparator tests against orderflow goldens — valid-schema pass,
  validation-failure red-on-bad (with expected reasons), invalid-schema hard error,
  non-JSON-body hard error.
- **A2:** table-driven `costSum` tests — emitted-cost path unchanged, derived path
  (per-model), mixed emitted+derived, model-not-in-table hard error, empty-table
  legacy error. CEL `cost` parity test against `budgets` for the same trace.
- **A3:** `ctl` tests for `FormatServices` and domain-aware `diff`; a dispatcher test
  that `service <verb>` routes correctly and an unknown domain errors. The orderflow
  e2e already exercises the http drive path end-to-end.
- **A4:** a step test plus a feature using `the result matches regex`.
- **A5:** the no-traceparent driver assertions; no behavioural code to test beyond
  the doc/test.
- **Coverage:** ≥80% per touched package; hermetic (no Tempo/network) for all unit
  and comparator tests.

## 9. Sequencing & relationship to Spec B

- **Recommended order:** Spec B's **matcher registry first**, then A1 — so `schema`
  ships as a registered `core.Matcher` rather than a `switch` case that is immediately
  refactored.
- A2, A3, A4, A5 are independent of Spec B and of each other; they may land in any
  order. A2 is the most invasive (two constructor signatures); A4/A5 are the smallest.

## 10. Decisions made (with rationale)

- **`schema` is a deterministic `result` matcher reading `Body`** — it is the v1
  design's matcher, output-only, so it fits `result` (unlike trace-aware CEL).
  (Approved.)
- **`santhosh-tekuri/jsonschema/v6`** — maintained, draft-2020-12, pure Go. (Approved.)
- **Per-model pricing, emitted-cost-wins precedence, no default rate** — most accurate;
  a missing model is a hard error, not a silent guess (invariant 4). (Approved.)
- **Pricing mirrored into `core.Pricing`** — keeps the comparator layer free of a
  `config` import, following the existing `core.HTTPSpec` pattern. (Approved.)
- **Full `mentatctl service` mirror with a shared verb handler** — `run/trace/replay`
  reuse adapter-agnostic paths; only `tools`↔`services` and `diff` selection differ.
  (Approved.)
- **traceparent is documented-and-reserved, not implemented** — baggage tag-first is
  the invariant; `PrimaryTraceID` has no consumer; building it now is premature.
  (Approved.)
