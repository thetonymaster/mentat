---
name: traces
description: Query and display OpenTelemetry traces from the Mentat dev Tempo (the deploy/ stack). Resolve a behaviour-test run by its test.run.id into a merged span forest, show gen_ai.* tool-call sequences, tokens, cost, and errors. Use when debugging why a Mentat scenario passed or failed, or inspecting what an agent SUT actually did.
user_invocable: true
---

# Mentat Trace Viewer

Query traces from the local Tempo started by `make harness-up` (the `deploy/`
stack, query API on `http://localhost:3200`). Driven by `traces.py` in this
directory — stdlib-only. Built for Mentat's correlation model: a run is tagged
with `test.run.id` and may span **several** traces (multi-turn / sub-agent), so
`run` fetches and merges them all into one forest.

## Usage

Run `traces.py` from this directory (or by absolute path). Output is plain text,
ready to paste into chat.

| Slash command | Script call | What it shows |
| --- | --- | --- |
| `/traces run <id>` | `traces.py run <test.run.id>` | **All** traces for a run, merged into one span forest |
| `/traces tools <id>` | `traces.py tools <test.run.id>` | Tool-call sequence for a run (name · order · duration · tokens) |
| `/traces` | `traces.py list` | 5 most recent `invoke_agent` traces |
| `/traces all` | `traces.py all` | 10 most recent traces of any kind |
| `/traces get <traceID>` | `traces.py get <traceID>` | Span tree for a single trace ID |
| `/traces errors` | `traces.py errors` | Traces with `status=error` |
| `/traces query <traceQL>` | `traces.py query '<TraceQL>'` | Arbitrary TraceQL search |

Flags: `--endpoint URL` (default `http://localhost:3200`), `--limit N`,
`--service NAME`. `traces.py --help` for the full reference.

### Endpoint selection

Defaults to the local dev Tempo. Override with `--endpoint`, or set `TEMPO_ENDPOINT`.
For a Tempo behind basic auth (e.g. Grafana Cloud), set `TEMPO_USER` and
`TEMPO_TOKEN` — the script sends them as HTTP basic auth. It prints a clear error
and exits 2 if the endpoint is unreachable.

## The correlation model (why `run` ≠ `get`)

Mentat injects `test.run.id=<uuid>` on every span of a run — a **resource
attribute** (via `OTEL_RESOURCE_ATTRIBUTES`) for shell-spawned agents, **baggage**
for http/grpc services. Because an agent can start its own root trace and emit
several, the canonical lookup is **by run id, not trace id**:

- `run <id>` → `GET /api/search?q={ .test.run.id = "<id>" }` → fetch each matching
  trace → merge spans → render one forest (multiple roots are normal).
- `get <traceID>` → a single trace by its hex ID (use when you already have one).

## TraceQL reference (Mentat spans)

- `{ .test.run.id = "<uuid>" }` — every span of one run (resource OR span scope)
- `{ .test.scenario = "happy" }` — all runs of a scenario
- `{ span.gen_ai.operation.name = "invoke_agent" }` — agent root spans
- `{ span.gen_ai.operation.name = "execute_tool" }` — tool-call spans
- `{ span.gen_ai.tool.name = "delete_record" }` — a specific tool was called
- `{ span.gen_ai.usage.output_tokens > 1000 }` — high-output spans
- `{ status = error }` — error spans
- `{ span.gen_ai.operation.name = "invoke_agent" } >> { span.gen_ai.tool.name = "search" }` —
  structural: agent runs containing a `search` tool descendant

## Highlighted attributes

The span-tree formatter surfaces these keys when present (edit `INTERESTING_ATTRS`
at the top of `traces.py` to add more):

`gen_ai.operation.name`, `gen_ai.agent.name`, `gen_ai.request.model`,
`gen_ai.response.finish_reasons`, `gen_ai.tool.name`, `gen_ai.tool.call.arguments`,
`gen_ai.tool.call.result`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`,
`gen_ai.usage.cost_usd`, `test.run.id`, `test.scenario`, `test.case`.

## Tempo API (raw)

```bash
GET http://localhost:3200/api/search?q=<URL-encoded TraceQL>&limit=N
GET http://localhost:3200/api/traces/<traceID>
```

Gotchas the script handles: trace/span IDs come back base64 in OTLP JSON (hex-decoded
for display); `/api/traces` may return `batches` or `resourceSpans` depending on Tempo
version (both parsed); `parentSpanId` is `null` for roots; `status.code` is `"STATUS_CODE_ERROR"` or int `2`.
