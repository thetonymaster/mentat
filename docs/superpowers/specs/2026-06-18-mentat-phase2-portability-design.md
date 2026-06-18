# Mentat Phase 2 — Portability (microservices) — Design

**Date:** 2026-06-18
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`,
`2026-06-17-mentat-test-harness-design.md`

## 1. Purpose

v1 proved Mentat against an **agent** SUT over the shell transport, correlating by
`OTEL_RESOURCE_ATTRIBUTES`. Phase 2 proves the central portability claim the whole
`Evidence`-only comparator design rests on: **the same comparator suite works against
distributed microservices**, correlating by **W3C baggage** across real process
boundaries. Nothing downstream has exercised the baggage path yet, so it is the
architecturally risky work this phase de-risks.

Two deliverable halves, one per side of the framework/harness split:

- **Framework (Mentat):** an `http` driver adapter, a portable `sequence` comparator,
  the godog grammar for the service path, and `http` target config.
- **Harness (`tracelab/orderflow`):** the dual-mode microservices SUT defined in the
  harness design (§4), exercised through L1–L3.

## 2. Scope

**In scope**

- `http` driver adapter (`internal/driver/http.go`).
- `sequence` comparator generalized with a `Kind` selector (`tool` | `service`).
- godog grammar additions for the service path (§6).
- `http` target config in `mentat.yaml`.
- `tracelab/orderflow` dual-mode SUT, 6 scenarios, baggage propagation.
- L1 goldens + L2 hermetic E2E + L3 meta-test for the microservice path.

**Out of scope** (deferred; named here so the boundary is explicit)

- `shape` comparator + sidecar expectations YAML — **Phase 3**.
- CEL result evaluation — **its own spec, immediately after this one**.
- `grpc` / `mcp` / optional `tracetest` adapters — **Phase 5**.
- A driver-rooted distributed trace via injected `traceparent` (see §3) — deferred
  nicety; not required for correlation.
- `semantic` LLM-judge matcher + `@runs(N)` — **Phase 4**.

## 3. Correlation & propagation

This is the half v1 never exercised, so the contract is stated precisely.

- **The `http` driver injects baggage only.** It sets
  `baggage: test.run.id=<uuid>,test.scenario=<s>` (built via
  `go.opentelemetry.io/otel/propagation.Baggage` for correct W3C encoding) and an
  `X-Scenario: <s>` header. It does **not** inject `traceparent`: the driver is a
  plain, un-instrumented, non-exporting HTTP client, so a `traceparent` it emitted
  would be a dangling parent reference. Driver-rooted traces are a deferred nicety.
- **`orderflow` sets the global propagator to a composite of the W3C tracecontext and
  baggage propagators.** The gateway's `otelhttp` server span has no remote parent, so
  it **roots the trace**; every inter-service call propagates context hop-to-hop. The
  whole flow is therefore **one trace rooted at `gateway`**. The forest-of-≥1 model
  (architecture invariant 2) still holds — a run resolves to a forest that here
  happens to have a single root.
- **A `BaggageSpanProcessor` copies `test.run.id` (and `test.scenario`) onto every span
  as it starts.** Baggage is not auto-stamped onto spans by the SDK, so this processor
  is what makes the tag queryable. Mentat resolves the run by querying
  `{ .test.run.id = "<uuid>" }` — the genuine baggage path end-to-end, not a mock.
- **Each service gets its own `TracerProvider` with its own `service.name`.** That
  `service.name` resource attribute is the identity the `sequence` comparator keys on
  (§5), and the Tempo store already merges resource attributes onto every span.

**Dependency note:** the baggage-copy span processor (contrib `baggagecopy`, or a
~30-line hand-rolled `SpanProcessor` if we prefer zero new deps) and `otelhttp`
instrumentation are **`tracelab`-side only**. Mentat core gains no new direct
dependency beyond what `otel` already provides.

## 4. `http` driver adapter

- `NewHTTP() core.Driver`, registered in `engine.Build` under the adapter name
  `"http"` (resolved by the existing per-seam driver registry, consistent with
  `shell`).
- Reads a new `Target.HTTP{ URL, Method, Headers }` block from config (§7). Builds the
  request, injects the baggage header (the analogue of shell's `resourceAttrs`, but via
  `propagation.Baggage`), sets `X-Scenario` from `RunSpec.Tags`, and sends
  `RunSpec.Input` as the request body.
- Maps the response into the existing boundary `Output`:
  `Output{ Status: resp.StatusCode, Body: <body bytes>, Answer: string(body) }`. Those
  http fields already exist on `core.Output`.
- **No silent fallback (invariant 4).** A non-2xx response is **not** a driver
  error — it is data the comparators judge (e.g. `payment_decline → 402`). Only
  transport-level failures (connection refused, timeout, malformed URL) return a
  wrapped `error`. The client is timeout-bound by default.

## 5. `sequence` comparator generalization

The comparator stays a single type; portability comes from a selection strategy chosen
by the expectation:

```go
type SequenceExpectation struct {
    Kind      string   // "" | "tool" (default) | "service"
    Order     []string
    Forbidden []string
}
```

- **`tool` (and `""`, backward compatible):** today's path — `Trace.ByOp(execute_tool)`,
  name from `gen_ai.tool.name`. Existing agent tests are unaffected.
- **`service`:** group spans by the `service.name` resource attribute; for each distinct
  service take its **first-seen `Start`** (a service cannot emit a span before it
  receives the request, so this is its server-side entry); order the services by that
  time; match the expected **ordered subsequence** (extra services allowed between
  matches) plus the **forbidden** set. A selected span missing `service.name` is a hard,
  descriptive error (invariant 4), mirroring the existing missing-`tool.name` error.
- Failure reasons keep the existing expected-vs-actual formatting.

## 6. godog grammar additions

New steps (service path):

- `Then the services are called in order:` (table) → `SequenceExpectation{Kind:"service", Order:…}`
- `Then the service "<x>" is never called` → `SequenceExpectation{Kind:"service", Forbidden:[x]}`
- `Then the response body json-contains:` (docstring) → `result` comparator, `json-subset`
  matcher (wire the step if not already present).

Reused unchanged (already portable — they read `Output`/`Trace` generically):
`the response status is …`, `total latency is under … ms`, `no span has status "ERROR"`,
`the result contains …`, `the result equals …`.

**When step:** reuse the existing `When I run scenario "<s>"` against an `http` target,
which sets the `X-Scenario` header. This diverges from the harness spec's *illustrative*
`When I send the request with body fixture "order.json"` Gherkin, deliberately, to keep
the grammar consistent with the researchbot path. (Approved divergence.)

## 7. `http` target config

`Target` gains an optional `http` block, used when `adapter: http`:

```yaml
targets:
  checkout:
    adapter: http
    max_concurrency: 8        # http default is already 8
    http:
      url: "http://localhost:8080/orders"
      method: POST
      headers:
        Content-Type: application/json
```

Loader rules: when `adapter: http`, `http.url` and `http.method` are required (missing →
descriptive error, no silent default); `headers` is optional. The existing per-adapter
`max_concurrency` defaults (`http`→8) are unchanged.

## 8. `orderflow` SUT (`tracelab/orderflow`)

Per the harness design §4, restated here for completeness:

- **Topology:** `gateway → auth → inventory → payment → notify`, plus a `legacy-pricing`
  service that must **not** be called on the happy path.
- **Dual-mode, one codebase, config-driven topology:**
  - *in-process* — one binary starts all services on distinct localhost ports, each with
    its own `TracerProvider`/`service.name`; fast and hermetic; used for L2/L3.
  - *container* — each service in its own docker-compose container; validates authentic
    cross-process baggage propagation.
  Because the service code is identical across modes, container mode is nearly free
  (docker-compose entries + a topology config).
- **Instrumentation:** `otelhttp` + composite `tracecontext`+`baggage` propagator +
  `BaggageSpanProcessor` (§3).
- **Scenarios (via `X-Scenario` header):**

  | Scenario | Behaviour | Trips |
  |---|---|---|
  | `happy` | 201, services in order, within SLO | none (all green) |
  | `payment_decline` | payment errors → 402, error span | result / budgets(error) |
  | `inventory_out` | short-circuits, `notify` skipped | (Phase 3 shape; here: sequence/result) |
  | `slow` | deterministic injected latency | budgets (latency) |
  | `legacy_path` | calls `legacy-pricing` | sequence (forbidden service) |
  | `reorder` | `payment` before `inventory` | sequence (order) |

- **Determinism:** scenario fully determines control flow, service order, attributes,
  and output; `slow` uses fixed sleeps; tests assert order/presence/attributes/output,
  not absolute timings (budget tests use generous thresholds or the injected latency).

Note: `inventory_out` is fundamentally a *missing-span* (shape) violation, which lands
in Phase 3. In this phase it is still captured as a golden and asserted via the
sequence/result comparators (e.g. `notify` absent from the service order, or the
response status/body), so the scenario is usable now and ready for `shape` later.

## 9. Test pyramid extension

- **L1 (unit, no infra):** a `tracelab capture` mode runs each orderflow scenario once
  and writes goldens to `testdata/traces/orderflow/<scenario>.json`. Unit tests run
  `sequence{Kind:"service"}`, `budgets`, and `result` against them via the
  `inmem`/`otlp-file` store. Goldens are reviewed-on-change artifacts.
- **L2 (hermetic E2E):** `make harness-up` brings up Tempo + Collector + orderflow
  (in-process mode); `mentat run features/checkout.feature` asserts **green** on `happy`.
- **L3 (meta-test, mandatory):** drive the five bad scenarios (`payment_decline`,
  `inventory_out`, `slow`, `legacy_path`, `reorder`); assert Mentat reports **failure** —
  non-zero exit and the expected `Verdict.Reasons`. This proves the comparators detect
  the violations they claim to, over the baggage/microservice path.
- **Coverage:** ≥80% per new package (invariant from `CLAUDE.md`).

## 10. Suggested plan decomposition

Mirroring the v1 ordering (harness before framework), this one spec yields **two
implementation plans**:

1. **`orderflow-harness` plan** — build the dual-mode orderflow SUT + golden capture.
   Independently buildable and testable against its own goldens; no Mentat changes.
2. **`mentat-portability` plan** — `http` driver + `sequence(service)` generalization +
   grammar + `http` target config + L2/L3 wiring. Consumes the goldens from plan 1 for
   its L1 tests.

## 11. Decisions made (with rationale)

- **Baggage-only correlation, no `traceparent`** — the driver is not an instrumented
  exporter; baggage + `BaggageSpanProcessor` already lands `test.run.id` on every span,
  and the gateway roots a single coherent trace. (Approved.)
- **One `sequence` comparator with a `Kind` selector, service identity = `service.name`**
  — preserves the "one comparator suite, both modalities" principle; aligns with the
  `auth-service` identity in the spec's Gherkin; needs no new span metadata. (Approved.)
- **Reuse `When I run scenario` instead of the spec's literal `body fixture` Gherkin** —
  keeps the grammar consistent with the researchbot path. (Approved divergence.)
- **Dual-mode orderflow from the start** — shared code makes container mode nearly free
  and gives both speed (in-process) and cross-process authenticity (container).
- **Non-2xx is data, not a driver error** — comparators must judge `402`/`5xx`; only
  transport failures are errors.
- **CEL deferred to its own spec** — result evaluation is an independent axis that
  benefits both agents and microservices; keeping it separate keeps each spec focused.
