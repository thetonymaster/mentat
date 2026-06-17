# TBT Test Harness (`tracelab`) — Design

**Date:** 2026-06-17
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`

## 1. Purpose & guiding principle

`tracelab` is the **system-under-test we build TBT against** — realistic,
deterministic, OpenTelemetry-instrumented, baggage-aware. It exists before the
framework so TBT always has a concrete target to develop and dogfood against.

**Guiding principle:** a test framework is only trustworthy if we can prove it goes
**red on bad behaviour**, not merely green on good. So every SUT must produce both
**known-good and known-bad** behaviour on command, covering each comparator's pass
*and* fail path. "Testing the tester" (Section 7, L3) is a first-class goal, not an
afterthought.

## 2. Components

| Component | Role | Built |
|---|---|---|
| `researchbot` | Agent SUT; emits `gen_ai.*` spans; **is the deterministic agent fixture** the framework spec calls `fakeagent`. | first (with v1) |
| `orderflow` | Microservices SUT; order/checkout flow across several services. | Phase 2 |
| `deploy/` | docker-compose: Tempo + OTel Collector + SUTs. | first |
| golden capture | Run scenarios, dump emitted OTLP → `testdata/traces/*.json`. | first |

Repo location: top-level `tracelab/` (`tracelab/researchbot/`,
`tracelab/orderflow/`, `tracelab/deploy/`). Golden traces are captured into the
framework's `testdata/traces/`. `researchbot` supersedes the `testdata/fakeagent/`
placeholder in the framework spec.

## 3. `researchbot` — the agent SUT

A Go program that emits a realistic agent trace **without a real LLM** (hermetic,
no API key, fully deterministic). Given a prompt and `--scenario`, it replays a
**data-driven scripted plan**:

```yaml
scenario: happy
output: "Q3 revenue grew 12% to $4.2M, driven by ..."
tokens: { input: 1200, output: 600 }
cost_usd: 0.018
steps:
  - chat: { model: claude-x, finish: tool_calls }
  - tool: { name: search,    args: {...}, result: "..." }
  - tool: { name: fetch_doc, args: {...}, result: "..." }
  - chat: { model: claude-x, finish: tool_calls }
  - tool: { name: summarize, args: {...}, result: "..." }
  - chat: { model: claude-x, finish: stop }
```

It emits an `invoke_agent` root span with `chat` and `execute_tool {name}` children
carrying OTel GenAI attributes (`gen_ai.tool.name`, `gen_ai.tool.call.arguments`/
`result`, `gen_ai.usage.input_tokens`/`output_tokens`, `gen_ai.usage.cost_usd`,
`gen_ai.response.finish_reasons`) and prints the final answer to stdout (the
boundary `Output`). It reads injected baggage and runs a `BaggageSpanProcessor`
(Section 6).

**Scenario coverage:**

| Scenario | Behaviour | Exercises |
|---|---|---|
| `happy` | correct tools in order, within budget, good answer | sequence ✓ budgets ✓ result ✓ |
| `extra_tool` | also calls `delete_record` | forbidden-tool → sequence ✗ |
| `wrong_order` | `summarize` before `search` | sequence ✗ |
| `over_budget` | high token/cost totals | budgets ✗ |
| `bad_answer` | wrong / empty final output | result ✗ |

**Optional `--live` mode** (not default, not in the hermetic path): call a real
model (Claude) for demos/manual exploration. The hermetic test suite always uses
the scripted plans.

## 4. `orderflow` — the microservices SUT

An order/checkout flow: `gateway → auth → inventory → payment → notify`, plus a
`legacy-pricing` service that must **not** be called on the happy path.

**Dual-mode by design (same code, config-driven topology).** Every service is an
HTTP server instrumented with `otelhttp` + the W3C baggage propagator +
`BaggageSpanProcessor`. Services always talk over **real HTTP**, so context/baggage
propagation is genuine in both modes; only the bind topology differs:

- **In-process mode:** one binary starts all services on distinct localhost ports;
  each gets its own `TracerProvider` with its own `service.name`. Fast, hermetic,
  used for unit/E2E speed.
- **Container mode:** each service in its own container in docker-compose; validates
  authentic cross-process propagation.

A `topology` config (service name → address) selects the mode. The service code is
identical across modes.

**Scenario selection** via `X-Scenario` request header:

| Scenario | Behaviour | Exercises |
|---|---|---|
| `happy` | 201, services in order, within SLO | sequence ✓ shape ✓ budgets ✓ result ✓ |
| `payment_decline` | payment errors → 402, error span | result ✗ / budgets(error) ✗ |
| `inventory_out` | short-circuits, `notify` skipped | shape ✗ (missing required span) |
| `slow` | deterministic injected latency | budgets ✗ |
| `legacy_path` | calls `legacy-pricing` | shape/sequence ✗ (forbidden span) |
| `reorder` | `payment` before `inventory` | sequence ✗ |

## 5. Determinism strategy

- No randomness: scenario selectors fully determine control flow, tool/service
  order, emitted attributes, and final output.
- Latency injection is fixed sleeps keyed by scenario (so `slow` reliably breaches a
  budget; `happy` reliably stays under).
- Span *durations* still vary slightly run-to-run; tests depend on **order,
  presence, attributes, and output** — not absolute timings — except budget tests,
  which use generous thresholds or the injected fixed latencies.
- Same scenario in → same trace structure + same `Output` out.

## 6. Baggage (the integration contract, exercised for real)

Both SUTs implement exactly what the framework requires of any real SUT:
1. The W3C **baggage propagator** (extract inbound `test.run.id`, `test.scenario`,
   `test.case`).
2. A **`BaggageSpanProcessor`** that stamps those entries onto every span as
   attributes, making them queryable (`{ test.run.id = "<uuid>" }`).

This means the hermetic E2E exercises the genuine baggage correlation path, not a
mock — validating the framework's correlation against a faithful target.

## 7. Test pyramid this enables

1. **L1 — unit (no infra):** comparators run against golden JSON traces loaded via
   the `inmem` / `otlp-file` `TraceStore`. Fast, deterministic, no Tempo.
2. **L2 — hermetic E2E:** `make harness-up` (Tempo + Collector + SUT); `agentctl`
   (researchbot) or `http` (orderflow) drives a scenario; full pipeline runs; assert
   **green** on the good scenarios.
3. **L3 — meta-test (testing the tester):** drive the **bad** scenarios and assert
   TBT reports **failure** — non-zero exit and the expected `Verdict.Reasons`. This
   proves the comparators detect the violations they claim to.

## 8. Golden-trace capture

A `tracelab capture` mode runs each scenario once and writes the emitted OTLP spans
to `testdata/traces/<sut>/<scenario>.json`. These goldens feed L1 unit tests. They
are regenerated deliberately (not on every run) and reviewed on change, so a drift
in emitted telemetry is a visible diff, not a silent surprise.

## 9. Phasing

1. **With framework v1:** `researchbot` (scripted plans for the 5 agent scenarios),
   `deploy/` (Tempo + Collector), golden capture, L1 + L2 + L3 for the agent path.
2. **With framework Phase 2:** `orderflow` (dual-mode in-process/container, 6
   microservice scenarios), extending L1–L3 to the microservice path.
3. **Later, opportunistic:** `researchbot --live` demo mode; additional scenarios as
   new comparators (shape, semantic) need targeted good/bad cases.

## 10. Decisions made (with rationale)

- **`researchbot` is the `fakeagent` fixture** — one component, scenario-driven, no
  duplication. Built first because v1 is agent-first.
- **`orderflow` is dual-mode (in-process + containers) from the start**, sharing one
  codebase over real HTTP, so baggage/context propagation is identical in both and
  we get speed *and* cross-process authenticity.
- **No real LLM in the hermetic path** — scripted plans keep agent tests
  deterministic and key-free; `--live` is an opt-in demo only.
- **Every SUT ships known-bad scenarios** so the meta-test layer can prove TBT fails
  correctly. The harness is co-designed with the comparators it must trip.
- **Goldens are captured artifacts, reviewed on change** — telemetry drift is a
  visible diff.
