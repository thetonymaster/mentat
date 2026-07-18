# Mentat

A trace-based **behaviour test framework**: write Gherkin specs, drive a
system-under-test (AI/LLM agents or microservices), fetch its OpenTelemetry trace
from Tempo, and run **comparators** that assert *how it behaved* and *what it produced*.

## Quickstart

```bash
# 1. bring up the local Tempo + OTel Collector
make harness-up

# 2. run the behaviour specs against the researchbot agent SUT
go run ./cmd/mentat run features/

# 3. inspect a run manually
go run ./cmd/mentatctl agent run --target research-agent --scenario happy
go run ./cmd/mentatctl agent tools --last

make harness-down
```

## How it works

`Gherkin (.feature) → godog → engine → drive SUT → resolve trace (Tempo) →
comparators (sequence / budgets / result) on the Evidence forest → JUnit`.

A run is tagged with `test.run.id` and may span several traces; correlation resolves
by that tag and merges them. Comparators consume only `Evidence` (trace forest +
captured output), which keeps them portable across agents and microservices.

## Authoring specs (`mentat steps`, `mentat validate`)

```bash
go run ./cmd/mentat steps                 # every registered step, grouped, + grammar (text)
go run ./cmd/mentat steps --format md     # the same, as Markdown (the committed docs/steps.md)
go run ./cmd/mentat validate              # static-check the feature corpus (no SUT/store/judge)
go run ./cmd/mentat validate --format json
```

- **Step reference.** `mentat steps` prints every Gherkin step (pattern, summary,
  example) plus the shared selector/quantifier/ordinal grammar and CEL variables.
  Its Markdown form is generated into [`docs/steps.md`](docs/steps.md) via
  `go generate ./...` and a drift test keeps that file byte-identical — the reference
  can never fall out of sync with the registered steps.
- **Validate.** `mentat validate [paths...]` runs the authoring prechecks statically —
  step binding, CEL precompilation, shape patterns, targets, expectations — over the
  feature corpus without driving a SUT or contacting a store/judge (no network by
  construction). It reports **all** findings and exits 1 on any (or when no feature
  files are found), 0 when clean. `--format json` emits
  `{"findings":[{"file","line","class","message"}]}`.

## Semantic result matcher (`the result means`)

Alongside the deterministic result matchers (`the result contains` / `equals` /
`matches regex`), the **`semantic`** matcher grades the run's answer by *meaning*
through an LLM judge — paraphrase-tolerant, so a correct answer phrased differently
still passes. It is available inline and as a docstring:

```gherkin
Then the result means "RAG augments an LLM with retrieved external documents"
And  the result means:
  """
  multi-line expected meaning
  """
```

The judge grades the run's final answer (`Evidence.Output`). A failing assertion
reports the judge's human-readable reason. An empty expected meaning fails fast at
scenario start; a judge-backend failure (auth, transport, malformed response, vote
tie) is a hard error — never a guessed PASS/FAIL.

### `judge:` configuration (`mentat.yaml`)

All fields are optional; omit the whole block to use the defaults. A project that
never writes `the result means` never needs it.

```yaml
judge:
  backend: claude            # default "claude"
  model: claude-haiku-4-5    # default "claude-haiku-4-5" (fast tier); e.g. claude-sonnet-4-6, claude-opus-4-8 (highest accuracy)
  votes: 1                   # default 1; best-of-N majority, odd N (even N > 1 is rejected)
  temperature: 0             # optional; applied only on models that accept it (Sonnet 4.6 / Haiku 4.5)
  max_cost_usd: 0            # optional; 0 = unlimited. Positive = suite aborts once completed judge spend crosses it
```

| Field | Default | Notes |
| --- | --- | --- |
| `backend` | `claude` | resolved from the judge registry; an unknown name is a hard error at engine build |
| `model` | `claude-haiku-4-5` | fast tier (`config.DefaultJudgeModel`), ~80% cheaper per token than the former Opus default; upgrade accuracy with one line. Passed to the backend; an invalid model surfaces as a judge-call error |
| `votes` | `1` | must be `>= 1` and **odd** (even `votes > 1` is rejected — majority is undefined on a tie). `votes > 1` with `temperature: 0` is a **load error** — near-identical calls; raise the temperature or drop to `votes: 1` |
| `temperature` | `0` | takes effect only on models that accept it (Sonnet 4.6 / Haiku 4.5). Opus-tier rejects a temperature parameter, so determinism there comes from structured output + the vote |
| `max_cost_usd` | `0` (unlimited) | optional judge-spend ceiling in USD. After each scenario, completed judge cost is summed; once it crosses the ceiling the suite aborts naming spent/budget/scenario (reports still emit). Negative is rejected at load |

> **Data egress (opt-in, no redaction in v1).** `ANTHROPIC_API_KEY` is supplied via
> the environment, never in `mentat.yaml` — the secret stays out of config. Selecting
> `the result means` with `backend: claude` **sends the run's result content to the
> Anthropic API**: the step plus the backend are the consent surface. There is no
> content redaction in v1, so the agent's output — which may contain sensitive data —
> leaves the machine for a third-party API.

### Run lifecycle (`run_timeout` / `kill_grace`)

By default every SUT run is bounded (unless `run_timeout` is set to `unbounded`) and
its whole process tree is reaped, so a hung SUT fails the scenario instead of hanging
the suite, and no orphan outlives the run.

```yaml
run_timeout: 5m        # suite default per SUT run; "unbounded" opts out explicitly
kill_grace: 10s        # grace between the polite SIGTERM and the forceful SIGKILL
targets:
  research-agent:
    adapter: shell
    command: ["go", "run", "./tracelab/researchbot/cmd/researchbot"]
    run_timeout: 10m   # optional per-target override
```

| Field | Default | Notes |
| --- | --- | --- |
| `run_timeout` | `5m` | Go duration or the literal `unbounded`. Bounds one SUT run; expiry fails the scenario naming the target, phase (`drive`/`resolve`), and elapsed budget. A typo (any other non-duration string) is a hard error at config load. |
| `targets.<name>.run_timeout` | inherits suite | per-target override of the above |
| `kill_grace` | `10s` | suite-wide; must be `> 0`. On run end/timeout/cancel the SUT's process group gets SIGTERM, then SIGKILL after this grace. For a finite `run_timeout`, worst-case wall time per run is `run_timeout + kill_grace`; an `unbounded` run has no finite upper bound. |

**Interrupting a run.** SIGINT/SIGTERM cancels in-flight work, reaps the SUT tree,
still writes every configured report (`--report-json` / `--report-html` / `--junit`)
containing the completed scenarios plus an explicit interrupted marker (JSON
`"interrupted": true`, an HTML banner, a JUnit suite `<property>`), and exits `130`.
A second signal force-quits. Reports are written atomically (temp file + rename), so
an interrupt never leaves a truncated report. (POSIX only; Windows is out of scope.)

## Offline replay (file store)

The `file` store replays saved run fixtures from a directory instead of querying a
live Tempo, so a suite can be re-evaluated with **no infrastructure and no network**:

```yaml
store: file
storePath: fixtures/   # directory of saved-run fixtures (from `mentatctl agent run --save`)
```

Each fixture is keyed by its recorded `runScenario` field — the run id captured when
it was saved. Because resolution is by that exact id, offline replay runs on the
**pinned path** (`mentatctl agent replay <saved-run-id> --feature <f> --config <file-store-config>`),
which resolves the saved id from the store without driving anything. The live
`mentat run` path injects a *fresh* run id per run that matches no saved fixture, so
it deliberately fails loud (not-found naming the dir + id) rather than serving the
wrong trace. A `@runs(N>1)` scenario is a hard error — the store holds one recorded
sample per id. Offline replay is proven hermetically by
`internal/steps/filestore_replay_test.go`: a saved fixture drives a suite green with
no Docker, its trace resolved entirely from the local file store.

## Verbosity & diagnostics

Both `mentat` and `mentatctl` are **silent by default** — stdout stays reserved for
report/godog output, so piping or golden-diffing a run stays byte-stable. Two flags
turn on narration, and **all narration goes to stderr**:

| Flag | Level | Shows |
| --- | --- | --- |
| (none) | silent | nothing on stderr; happy-path output unchanged |
| `-v` | Info | drive/resolve lifecycle: `drive.start`, `resolve.start`, `resolve.done` (one line per stage) |
| `-vv` | Debug | everything `-v` shows, plus the injected SUT env (`drive.env` — Mentat-set keys only, including the merged `OTEL_RESOURCE_ATTRIBUTES`) and per-poll rounds (`resolve.poll`: round, spans seen, stable streak) |

```bash
go run ./cmd/mentat run -vv features/     # local debugging: injected env + poll rounds
go run ./cmd/mentatctl -v agent run --target research-agent --scenario happy
```

`-vv` never logs inherited environment beyond the keys Mentat itself sets, so ambient
secrets in the runner's environment do not leak into narration.

### Trace-not-found diagnosis

Correlation failures are **self-diagnosing without any verbosity flag**. When a run's
trace never appears within the poll timeout, the error names the store endpoint it
queried, the exact TraceQL query it issued, and a three-item triage checklist:

```text
correlate: no trace for run "…" within 30s (0 spans seen)
	store: http://localhost:3200
	query: { .test.run.id = "…" }
	checklist: (1) is the collector/Tempo up? (deploy: make harness-up)
	           (2) does the SUT export OTLP to the endpoint above?
	           (3) were OTEL_RESOURCE_ATTRIBUTES applied? (run with -vv to see injected env)
```

An *unstable* trace (spans present but still growing at the deadline) reports the same
`store:`/`query:` lines but omits the checklist — the trace exists, so it is a
stability problem, not a "where is it" one.

## Extending Mentat

Mentat's seams — driver, store, comparator, judge — are public interfaces on the
`github.com/thetonymaster/mentat` facade, so you can drive or grade anything
*without forking*: register a custom adapter at the `mentat.Run` call and it works in
`mentat.yaml` and feature files like a built-in. The guides under
[`docs/extending`](docs/extending) walk each seam:

- [Writing a custom Driver](docs/extending/driver.md)
- [Writing a custom TraceStore](docs/extending/store.md)
- [Writing a custom Comparator](docs/extending/comparator.md)
- [Writing a custom Judge](docs/extending/judge.md)
- [The Evidence a comparator inspects](docs/extending/evidence.md) — the shared vocabulary
- [Stability policy (pre-1.0)](docs/extending/stability.md) — how the public surface changes

[`examples/kafkaecho`](examples/kafkaecho) is a standalone module (its own `go.mod`)
that imports only the facade and drives a feature green — the CI-enforced proof the
surface suffices.

## Layout

- `cmd/mentat` — the behaviour-test runner (embeds godog)
- `cmd/mentatctl` — manual driver: `agent run/trace/tools/replay/diff`
- `internal/` — `engine`, `driver`, `correlate`, `store`, `comparator`, `steps`, `ctl`
- `tracelab/` — deterministic SUTs (`researchbot`); `deploy/` — Tempo + Collector
- `docs/superpowers/specs` — design; `docs/superpowers/plans` — implementation plans;
  `docs/architecture/mentat-architecture.html` — interactive diagram

## Development

`CLAUDE.md` is the contributor guide: uber gomock, table-driven tests, 80% coverage
floor, no silent fallbacks, interfaces + manual DI. `make ci` runs the full gate.
Subagents (`go-test-writer`, `go-coder`, `go-reviewer`) and skills (`/traces`,
`/coverage`) live under `.claude/`.
