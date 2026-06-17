# Mentat — Trace Behaviour Test Framework — Design

**Date:** 2026-06-16
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)

## 1. Purpose

A behaviour-driven test framework that asserts *how a system behaved* by inspecting
its OpenTelemetry trace. You write `Given/When/Then` scenarios in Gherkin; the
framework drives the system under test (SUT), fetches the resulting trace from
**Grafana Tempo**, and runs **comparators** that encode what "correct behaviour"
means.

- **Primary target:** AI/LLM agents (assert tool-call sequence, trace shape,
  budgets, and the run's result — both deterministic and semantic).
- **Secondary target:** distributed microservices (assert service-call order,
  required/forbidden spans, latency SLOs, and the response result).

Both modalities assert on the **result** of the run, not just on the path taken to
produce it. Result-testing is a first-class, cross-modality concern (Section 8).

The unifying idea: a comparator consumes the run `Evidence` (a normalized in-memory
`Trace` plus the driver-captured `Output`) and an `Expectation`, and returns a
`Verdict`. It does not know or care who fetched the trace or drove the SUT. This is
what makes the same comparator suite work across agents and microservices.

## 2. Goals and non-goals

**Goals**
- Author behaviour specs in Gherkin, readable by non-implementers.
- Assert on agent runs: tool-call sequence, trace structure, budgets
  (tokens/cost/latency), and the run's result (deterministic and semantic).
- Reuse the same comparators for microservices via a swappable trace source.
- Deterministic, hermetic end-to-end test of the framework itself.
- Fail loudly with actionable messages. No silent fallbacks.

**Non-goals**
- We do not build a trace store. Tempo owns storage/query and is one
  implementation behind a pluggable `TraceStore` interface (Section 7).
- We do not build a Gherkin parser/runner (godog owns parsing + execution).
- We do not own the agents/services under test; users register adapters. We
  maintain only a deterministic fake-agent fixture for our own tests.
- We do not depend on Tracetest as the engine. It is, at most, one optional
  trace-source adapter.

## 3. Background research (verified 2026-06-16)

These findings shaped the architecture and are recorded so the rationale is not
lost.

**Tracetest** (github.com/kubeshop/tracetest):
- Server + CLI monolith. **No public Go SDK** and **no plugin/extensibility
  interface** for custom assertions. We can only shell out to its CLI and parse
  output.
- Supports Tempo as a data store; triggers include HTTP/gRPC/Kafka/GraphQL,
  Playwright, and a **TraceID/manual trigger** (lets an external driver run a SUT
  out-of-band and hand Tracetest a trace ID).
- Assertion DSL: span selectors (`span[attr=val]`, `:first/:last/:nth_child`,
  parent→child nesting) with comparators (`=,!=,>,>=,<,<=,contains,startsWith,
  endsWith,json-contains`) and `tracetest.selected_spans.count`.
- **Cannot** assert ordered lateral (sibling) sequences, subtree/structural
  pattern matching, negated selectors, or run custom comparator logic.

**Grafana Tempo + TraceQL:**
- TraceQL supports structural queries: child `>`, descendant `>>`, ancestor/parent
  `<`/`<<`, sibling `~`, and **negation** `!>>`. Plus attribute and duration
  filters.
- **Cannot** express sibling *ordering* ("A before B" among siblings) — siblings
  are a set, not a sequence. Parent/child nesting is robust.
- This makes TraceQL stronger than Tracetest for *structure* (shape comparator),
  but **sequence ordering must be implemented by us** regardless of tool.

**Agent instrumentation:**
- OTel GenAI semantic conventions exist (experimental but production-proven):
  `gen_ai.operation.name` (`chat`/`invoke_agent`/`invoke_workflow`/`execute_tool`),
  `gen_ai.tool.name`, `gen_ai.tool.call.arguments`/`result`,
  `gen_ai.usage.input_tokens`/`output_tokens`, `gen_ai.usage.cost_usd`,
  `gen_ai.response.finish_reasons`.
- Tool calls surface as identifiable child spans; multi-agent flows render as a
  span tree. OpenLLMetry, OpenInference, and native SDK instrumentation
  (LangChain, LlamaIndex, Anthropic, CrewAI) all emit these.
- **Prerequisite:** the SUT must be instrumented. This is well-trodden but is the
  user's responsibility, not ours.

**Consequences for design:**
1. Tracetest cannot host our comparators → we build our own Go engine.
2. Shape comparator leans on TraceQL; sequence comparator is unavoidably ours.
3. Comparators must operate on a normalized in-memory `Trace` for portability.
4. Tracetest is demoted to an optional microservices trace-source adapter.
5. The trace backend sits behind a pluggable `TraceStore` interface; Tempo is one
   implementation (Section 7). Comparators staying in-memory keeps a new store to
   ~two methods.
6. Correlation is **tag-first**: a unique `test.run.id` lands on every span (a
   resource attribute via `OTEL_RESOURCE_ATTRIBUTES` for spawned agents, baggage for
   http/grpc) and we resolve by querying that tag — merging the ≥1 traces a run may
   produce. Survives the SUT starting its own root trace, which `traceparent` alone
   does not (Section 5).

## 4. Architecture

```
.feature (Gherkin)
   |  godog
   v
Step grammar (Go)  -- inline expectations + optional sidecar YAML
   |
   v
Engine  -- scenario lifecycle: configure -> drive -> fetch -> compare -> report
   |
   +--> Driver layer -- Driver interface + adapters
   |        - shell adapter (Phase 1)
   |        - http/grpc adapter (Phase 2/5)
   |        - mcp adapter (Phase 5)
   |        - tracetest adapter (Phase 5, optional)
   |        injects run-id tag (env/baggage) + traceparent, returns {RunID, Output}
   |
   +--> Trace layer -- TraceStore iface (Tempo impl; jaeger/file/inmem) -> Trace
   |
   +--> Comparator core -- consumes the normalized Trace + RunResult.Output
   |        sequence | budgets | result(deterministic)    (Phase 1)
   |        shape (TraceQL-backed)                         (Phase 3)
   |        result(span-attr source)                       (Phase 3)
   |        result(semantic LLM-judge matcher)             (Phase 4)
   |
   v
Reporter -- godog JUnit + console, enriched with comparator verdict reasons
```

**Key invariant:** a comparator takes the run **`Evidence`** (the in-memory
`Trace` plus the driver-captured `Output`) + an `Expectation` and returns a
`Verdict`. It never talks to Tempo or the driver directly. This boundary is what
makes comparators portable across agents and microservices. Structural comparators
(sequence/shape/budgets) read `Evidence.Trace`; the result comparator reads
`Evidence.Output` (and, from Phase 3, span-attribute results in `Evidence.Trace`).

### 4.1 Engine concurrency

Concurrency safety is **structural**: every run carries a unique `test.run.id` tag
on every span (Section 5), so concurrent runs are independently resolvable in the
trace store — no cross-talk. That property is what makes any parallelism safe.

- **Unit of work = the scenario.** Steps run sequentially within a scenario; one
  `When` = one run (v1). `@runs(N)` repeats are sub-units (Section 9).
- **Two-level control:** (1) global scheduler width via godog's scenario-level
  parallelism (`--concurrency`); (2) a **per-target cap**
  (`targets.<name>.max_concurrency`) enforced by a weighted semaphore around the
  drive step, protecting individual SUTs regardless of scheduler width.
- **Smart per-adapter defaults:** the scheduler is parallel by default, but the
  per-target cap defaults by adapter *kind* — **agents (shell/mcp) default to 1**
  (serial per target; cost/rate-limit safe), **microservices (http/grpc) default
  high**. Mixed suites parallelize the cheap parts and throttle the expensive ones
  with no configuration.
- **`@runs(N)` repeats run serially by default** (bounds instantaneous cost),
  opt-in parallel under the per-target cap.
- **Failure isolation:** run-all-report-all by default; `--fail-fast` opt-in.
- **Goroutine-safety contract:** the `TraceStore` client, comparators (stateless),
  and reporter must be concurrency-safe; per-scenario state lives in godog's
  scenario `context.Context`; step defs share no mutable globals.

## 5. Correlation: getting *this run's* trace

**Tag-first, resolved by run id.** We do not rely on the SUT adopting our injected
trace id — many agent frameworks mint their own root trace and ignore `traceparent`.
Instead the driver tags the run with a unique `test.run.id` (UUID) that lands on
**every** span, and we resolve by querying the store for that tag. `traceparent` is
still minted as a *complement* (when the SUT adopts it we also get a clean primary
trace id), but it is never required.

**Injection is per-adapter** — the tag reaches spans differently per transport:

| Adapter | Driver injects | SUT contract |
|---|---|---|
| shell / mcp (spawned process) | `OTEL_RESOURCE_ATTRIBUTES=test.run.id=<uuid>,…` env (+ `TRACEPARENT`) | standard OTel init — the default resource honors `OTEL_RESOURCE_ATTRIBUTES`, so every span carries `test.run.id` as a **resource attribute**. No custom code. |
| http / grpc (request) | `baggage: test.run.id=<uuid>,…` header (+ `traceparent`) | W3C baggage propagator **+** a `BaggageSpanProcessor` to copy baggage → span attribute (baggage is not auto-stamped). |

**Resolution (uniform across adapters):**
1. `Correlator.Inject` mints `test.run.id` (+ `test.scenario`, `test.case`) into the
   `RunSpec`; the adapter applies it via its transport above.
2. Drive the SUT.
3. `Correlator.Resolve` queries the store for **all** traces carrying the tag
   (Tempo: `{ resource.test.run.id = "<id>" || span.test.run.id = "<id>" }`) and
   **merges them into one `Evidence.Trace` forest** — a run legitimately spans ≥1
   root trace (multi-turn / sub-agent). Poll until the span count is stable for K
   iterations (ingestion lag) or timeout.

**Multiple traces are normal, not an error.** The unique per-run tag means every
matching trace belongs to this run, so merging is unambiguous. The hard-failure
cases are **zero** traces within timeout, or a store/query error — reported with run
id, elapsed time, and last-seen span count. **No silent fallbacks; we never guess.**

## 6. Authoring UX (hybrid)

Common assertions are inline in Gherkin via a curated step grammar (with data
tables and docstrings). Complex structural trace-shape patterns go in an optional
referenced sidecar YAML file.

Agent example:

```gherkin
Feature: Research agent behaviour
  Scenario: summarizes Q3 revenue
    Given the agent adapter "shell:research-agent"
    When I run the agent with prompt "Summarize Q3 revenue"
    Then the agent calls tools in order:
      | search    |
      | fetch_doc |
      | summarize |
    And the tool "delete_record" is never called
    And total tokens are under 5000
    And total cost is under 0.05 USD
    And the result contains "Q3 revenue"
    And the result semantically matches "a summary of third-quarter revenue figures"
```

(`result contains` is a deterministic matcher available in v1; `result
semantically matches` is the Phase 4 LLM-judge matcher.)

Microservice example:

```gherkin
Feature: Checkout service behaviour
  Scenario: order placement hits services in order, within SLO
    Given the http target "POST https://checkout.svc/orders"
    When I send the request with body fixture "order.json"
    Then the service calls in order:
      | auth-service      |
      | inventory-service |
      | payment-service   |
    And the span "legacy-pricing" is never called
    And total latency is under 800 ms
    And no span has status "ERROR"
    And the response status is 201
    And the response body json-contains:
      """
      { "status": "confirmed" }
      """
```

The `When` step drives the SUT, fetches the trace, and stashes the run `Evidence`
(`Trace` + captured `Output`) in scenario context. Each `Then` step parses its
inline expectation, invokes the relevant comparator against the stashed `Evidence`,
and fails the step with the comparator's verdict reasons if it does not pass. The
sidecar escape hatch (e.g. `Then the run matches shape "fanout-summarize"`) lands
in Phase 3.

## 7. Module layout (Go monorepo)

```
cmd/mentatctl/         generic SUT driver CLI (agent/service subcommands)
cmd/mentat/              test runner (embeds godog) — thin in v1
internal/engine/      scenario lifecycle + composition root (engine.Build)
internal/registry/    per-seam registries (RegisterStore/Driver/Comparator/...)
internal/steps/       godog step definitions + step grammar
internal/driver/      Driver iface + shell adapter (Phase 1); resolved by URI scheme
internal/trace/       normalized Trace model
internal/store/       TraceStore iface + tempo impl; otlp-file / inmem (test)
internal/correlate/   Correlator iface: baggage+tag (primary), traceparent; stable-poll
internal/comparator/  Comparator iface + sequence, budgets, result (Phase 1);
                      result Matcher iface; Judge iface (Phase 4)
internal/report/      Reporter iface: JUnit + console w/ verdict reasons
features/             example .feature files
expectations/         sidecar YAML pattern specs (Phase 3)
deploy/               docker-compose: Tempo + OTel Collector (local/dev/CI)
testdata/traces/      golden OTLP fixtures captured from tracelab (feed inmem store)
tracelab/             test-harness SUTs: researchbot (agent), orderflow (microsvc)
```

The `tracelab` test harness (the SUTs we develop Mentat against, including the
deterministic agent fixture) has its own design:
`2026-06-17-mentat-test-harness-design.md`.

Core contracts:

```go
// Driver launches/triggers the SUT and returns the run id it tagged plus the SUT's
// boundary output. The adapter applies spec.RunID via its transport (env for shell,
// baggage header for http) — see Section 5.
type Driver interface {
    Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

type RunResult struct {
    RunID          string // the correlation tag applied to this run (test.run.id)
    PrimaryTraceID string // optional: set only if the SUT adopted our traceparent
    Output         Output // captured stdout / response body, status code, exit code
}

// Evidence is everything a comparator may inspect about a single run.
type Evidence struct {
    RunID  string
    Trace  *Trace // a FOREST: >=1 root traces merged by run id (Section 5)
    Output Output
}

// Comparator evaluates one behavioural dimension against the run evidence.
type Comparator interface {
    Name() string
    Compare(ctx context.Context, ev Evidence, e Expectation) (Verdict, error)
}

type Verdict struct {
    Pass    bool
    Reasons []string // human-readable; surfaced in the report on failure
}

// Swappable trace backend. A new store needs only these — comparators never
// touch it directly. Caps() advertises optional features (e.g. structural query).
type TraceStore interface {
    GetByID(ctx context.Context, id string) (*Trace, error)
    Query(ctx context.Context, q TraceQuery) ([]TraceRef, error) // backend-agnostic, e.g. by tag
    Caps() StoreCaps
}

// Correlation: tag the run, then resolve + merge all of its traces.
type Correlator interface {
    Inject(ctx context.Context, spec *RunSpec) (runID string)                    // mints test.run.id into spec; adapter applies it
    Resolve(ctx context.Context, store TraceStore, runID string) (*Trace, error) // fetch + merge ALL traces tagged runID
}

type Matcher  interface { Match(got, want Value) (Verdict, error) }                 // inside result comparator
type Judge    interface { Judge(ctx context.Context, got, criteria string) (Verdict, error) } // semantic matcher backend
type Reporter interface { Report(r ScenarioResult) error }
```

`Expectation` is comparator-specific config parsed from the Gherkin step (or the
sidecar YAML). The `Trace` is a **forest**: one or more root spans (a run may emit
several root traces — multi-turn / sub-agent) plus a flat index of every span (ID,
parent ID, name, kind, start/end, duration, status, attributes), built from the
`TraceStore`. Comparators read the flat span set and the per-tree links; they never
assume a single root. `Output` is the driver-captured boundary result (Section 8).
`RunSpec` carries the target/adapter, the input (prompt or request), and the
correlation tag the adapter injects.

### 7.1 Dependency injection & extensibility seams

Every seam above is an interface; concrete implementations are injected at a single
**composition root** (`engine.Build(cfg)`), which reads `mentat.yaml`, resolves each
dependency *by name* from its registry, and constructor-injects it into the Engine.
The Engine depends only on the interfaces — never on a concrete Tempo, shell
adapter, or Claude.

Idiomatic Go DI, **no framework**: interfaces + constructor injection + one
registry per seam. The registries are the extension points:

```
RegisterStore(name, factory)     RegisterComparator(name, factory)
RegisterDriver(scheme, factory)  RegisterMatcher(name, factory)
RegisterCorrelator(name, factory) RegisterJudge(name, factory)
RegisterReporter(name, factory)
```

Adding a capability = implement the interface + register a factory, with **no
Engine change** (open/closed). Built-ins register themselves per phase. The
`otlp-file` and `inmem` stores are not just future-proofing: they let comparator
unit tests load a known trace from a fixture and run with **zero infrastructure**.
`google/wire` is deliberately *not* used — for ~7 seams, manual wiring at the root
is trivial and debuggable; revisit only if the graph deepens.

## 8. Comparators

**Phase 1 — sequence (deterministic):** select tool/service spans
(`gen_ai.operation.name = "execute_tool"` with name from `gen_ai.tool.name` for
agents; service spans for microservices), sort by start time, and match against an
expected **ordered subsequence** (extra spans allowed between matches) plus a
**forbidden** set. Failure reasons show expected-vs-actual order.

**Phase 1 — budgets (deterministic):** numeric thresholds over aggregates — total
tokens (sum of `gen_ai.usage.*_tokens`), total cost (sum of `gen_ai.usage.cost_usd`,
or tokens × a pricing table in `mentat.yaml` when the attribute is absent),
wall-clock latency (the run *envelope*: `max(end) − min(start)` across all spans,
since a run may span multiple traces), and error-span count. Thresholds parsed from
the Gherkin step.

**Phase 1 — result (cross-modality, deterministic matchers first):** asserts on
the *result* of the run, not the path taken. One comparator with pluggable
matchers, applied to both agents and microservices:

| Matcher | Deterministic? | Phase | Typical use |
|---|---|---|---|
| `exact` / `equals` | yes | 1 | microservice response, deterministic agent output |
| `contains` / `regex` | yes | 1 | partial string / format checks |
| `json-subset` (step: `json-contains`) | yes | 1 | response payload contains expected fields |
| `status` / `code` | yes | 1 | HTTP status, exit code, error flag |
| `schema` | yes | 1 | response conforms to a JSON schema |
| `semantic` (LLM-judge) | no | 4 | fuzzy agent answers — "means the same as…" |

The result source is the **driver-captured boundary output** (`Evidence.Output`):
the agent's final answer (stdout) or the HTTP response body+status. In **Phase 3**
the result comparator gains a second source — **span-attribute results**
(`gen_ai.tool.call.result`, captured response-body attributes) — so intermediate
and per-tool results can also be asserted. The expectation selects the target
(the final result vs. the result of a named tool/span).

**Phase 3 — shape (deterministic, TraceQL-backed):** required spans present,
forbidden spans absent, parent/child and fan-out relationships. Expressed inline
for simple cases and in sidecar YAML for complex subtree patterns. Uses TraceQL
where it maps cleanly; falls back to in-memory tree matching for what TraceQL
cannot express.

**Phase 4 — result/semantic matcher (non-deterministic, LLM-judge):** the
`semantic` matcher of the result comparator. Judges whether the result content
matches an expected meaning. Backed by **Claude (Anthropic API)** behind a
pluggable `Judge` interface so the backend can be swapped. This is the only
matcher that needs the non-determinism handling in Section 9.

## 9. Non-determinism

Designed for now, built in Phase 4. A scenario tag `@runs(N)` causes the engine to
execute the scenario N times and apply a pass policy (`all` / `majority` /
threshold). Phase 1 deterministic comparators (sequence, budgets, deterministic
result matchers) run once; the engine reserves the repeat hook so the semantic
result matcher can use it without redesign.

## 10. Reporting

Use godog's built-in JUnit and pretty/console formatters. On comparator failure,
the step error carries the `Verdict.Reasons` (e.g. expected vs actual tool order),
so JUnit output and console both explain *why* a behaviour assertion failed, not
just that it did.

## 11. Error handling

Per project conventions, no silent fallbacks. Hard, descriptive failures for:
trace not found within timeout; ambiguous tag match; adapter invocation failure
(surface stderr/exit code); malformed expectation in a step; missing required span
attribute referenced by a budget. Crashes are data.

## 12. Testing strategy (TDD)

We develop against the `tracelab` harness (separate spec), which provides
deterministic, baggage-aware SUTs that emit **known-good and known-bad** behaviour
on command. Three layers:

- **L1 — unit (no infra):** comparators against golden OTLP fixtures
  (`testdata/traces/`) loaded via the `inmem`/`otlp-file` `TraceStore`; correlation
  against simulated ingestion lag.
- **L2 — hermetic E2E (Phase 1 acceptance):** `deploy/docker-compose.yml` stands up
  Tempo + OTel Collector; `tracelab/researchbot` emits a known trace **and a known
  final output**; running `features/` must produce a passing scenario (exercising
  sequence + budgets + a deterministic result matcher).
- **L3 — meta-test (testing the tester):** drive `tracelab`'s deliberately *bad*
  scenarios and assert Mentat reports **failure** (non-zero exit + expected
  `Verdict.Reasons`). This proves comparators detect the violations they claim to.

Production users point the framework at their own Tempo; the compose file is
dev/test infrastructure only.

## 13. Phasing

1. **v1 slice:** godog -> `mentatctl` shell adapter (captures trace ID + output) ->
   correlate + stable-poll -> **sequence + budgets + deterministic result**
   comparators -> JUnit. Hermetic E2E green/red proof.
2. **Portability:** minimal **`http` driver adapter** -> a microservice runs
   through the *same* sequence + budgets + result comparators (response
   body/status). Validates the swappable trace-source claim.
3. **Shape + span-attr results:** TraceQL-backed **shape comparator** + sidecar
   YAML expectations + the result comparator's **span-attribute source**
   (intermediate / per-tool results).
4. **Semantic:** the result comparator's **LLM-judge `semantic` matcher** (Claude,
   pluggable `Judge`) + `@runs(N)` non-determinism.
5. **Breadth:** **grpc / mcp** adapters + optional **tracetest** adapter for teams
   already invested in Tracetest test definitions.

## 14. Decisions made (with rationale)

- **Own Go engine; Tempo behind a pluggable `TraceStore`; Tracetest optional.**
  Tracetest has no Go SDK or plugin interface; TraceQL is stronger for structure;
  sequence/semantic are ours regardless. (Section 3.)
- **Generic driver + adapters; external SUT.** Maximizes reuse as a framework;
  users register adapters. A deterministic fake-agent fixture covers our own tests.
- **Hybrid authoring.** Inline Gherkin for common cases, sidecar YAML for complex
  shape patterns.
- **Comparators evaluate the run `Evidence`** (in-memory `Trace` + driver-captured
  `Output`). TraceQL is an optimization/pre-filter (Phase 3+), not the comparator
  substrate, to preserve portability.
- **Result-testing is one cross-modality comparator with pluggable matchers**
  (deterministic in v1, `semantic` in Phase 4), not a separate agent-only feature.
  Source is driver-captured boundary output first; span-attribute results in
  Phase 3.
- **Runner embeds godog** rather than relying on raw `go test`, for nicer UX
  (tags, reporters, config).
- **Semantic result matcher = Claude** behind a pluggable `Judge` interface
  (Phase 4).
- **Correlation is tag-first, resolved by run id, merging ≥1 traces.** A unique
  `test.run.id` lands on every span — resource attribute via
  `OTEL_RESOURCE_ATTRIBUTES` for spawned agents, baggage + processor for http/grpc —
  and we query that tag. `traceparent` is an optional complement. Multiple traces
  per run is normal, not an error. (Section 5.)
- **Pluggable `TraceStore`.** Tempo is one implementation; `jaeger`/`otlp-file`/
  `inmem` are others. Comparators stay in-memory, so a new store is ~two methods.
- **Extensibility via DI registries + a single composition root**, no framework
  (`google/wire` rejected for now). Every seam (store, driver, comparator, matcher,
  judge, correlator, reporter) is an interface resolved by name. (Section 7.1.)
- **Concurrency: parallel scheduler + smart per-adapter per-target caps** (agents
  serial, microservices parallel). Safe because each run is uniquely tagged via
  baggage. (Section 4.1.)

## 15. Naming & identifiers (decided)

- **Name: Mentat.** Runner CLI `mentat`; config file `mentat.yaml`. A single
  driver CLI `mentatctl` with domain subcommands — `mentatctl agent …`
  (shell/mcp adapters) and `mentatctl service …` (http/grpc adapters) — over one
  generic `Driver` interface.
- **`mentatctl agent` is the first-class v1 surface, optimised for ergonomics.**
  Commands: **`run`** (drive + correlate + fetch; prints the answer plus a compact
  trace summary — tools in order, tokens, cost, latency, trace ID), **`trace
  [id|--last]`** (pretty-print the span tree), **`tools [id|--last]`** (tool-call
  sequence: name · order · duration · tokens), **`replay <id>`** (re-run comparators
  against a stored/fetched trace *without* re-driving the agent — free and fast),
  **`diff <idA> <idB>`** (compare tool sequence / shape between two runs).
  Conveniences: `--target NAME` (named agents from `mentat.yaml`), `--last` (most
  recent run, cached in `~/.mentat/last`), prompt via `--prompt`/`--prompt-file`/
  stdin, `--scenario` (harness SUTs), output `--json`/`--quiet`/`-o file` (human
  default, TTY-aware colour), polling `--wait`/`--timeout`/`--poll-interval`,
  `--save NAME` (capture this run as a golden fixture — shares the harness
  `tracelab capture` mechanism), shell completion, and machine-friendly exit codes.
  `mentatctl service` mirrors this surface at Phase 2.
- **Module path:** `github.com/thetonymaster/mentat`.

## 16. Known limitations & risks (accepted)

- **Sibling ordering depends on timestamps.** The sequence comparator sorts spans by
  start time; across processes with clock skew (distributed `orderflow`, real agents)
  sibling order can be unreliable. Same-process runs (e.g. `researchbot`) are fine.
  We prefer parent-child happens-before where available and document the limit.
- **GenAI semconv is experimental.** `gen_ai.*` attribute names may drift; a thin
  attribute-mapping layer (config) isolates comparators + harness from churn rather
  than hardcoding names in many places.
- **Cost may be derived, not emitted.** When `gen_ai.usage.cost_usd` is absent,
  budgets fall back to tokens × a `mentat.yaml` pricing table; otherwise a cost
  assertion fails with a clear "cost not available" message.
- **Judge non-determinism (Phase 4).** The LLM judge can itself flip verdicts; we use
  temperature 0 + structured output and may add a judge vote. `@runs(N)` covers SUT
  variance, not judge variance.
- **`--last` is for interactive single runs** — the `~/.mentat/last` pointer races
  under parallel suite runs and is not used by the runner.
- **Captured goldens may contain sensitive data** (`tool.call.*`, response bodies).
  Synthetic harness data is safe; `--save` on real agents needs care.
- **Secrets:** the Phase-4 judge API key is read from env, never persisted or logged.

## 17. To pin during planning (deferred specs)

These are referenced above but defined as explicit deliverables in the v1 plan:

- **Step grammar** — the finite v1 step set and each step's mapping to a
  comparator + `Expectation`.
- **`Output` shape + agent answer-extraction** — how the agent's "result" is
  delimited from incidental stdout (e.g. last line, marker, or configured extractor).
- **`mentat.yaml` schema** — targets (name → adapter + invocation), Tempo endpoint,
  polling, per-target `max_concurrency`, pricing table; and how a scenario binds to a
  target (named target vs inline adapter in the `Given` step).
- **`mentat` runner CLI** — `mentat run [features/] --config --concurrency --tags
  --junit --fail-fast`.
- **L3 meta-test mechanism** — how "assert Mentat reports failure" is expressed and
  run (e.g. a Go test shelling out to `mentat` against known-bad scenarios).
