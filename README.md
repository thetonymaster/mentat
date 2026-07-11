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
  model: claude-opus-4-8     # default "claude-opus-4-8"; e.g. claude-haiku-4-5 (cheapest), claude-sonnet-4-6
  votes: 1                   # default 1; best-of-N majority, odd N (even N > 1 is rejected)
  temperature: 0             # optional; applied only on models that accept it (Sonnet 4.6 / Haiku 4.5)
```

| Field | Default | Notes |
| --- | --- | --- |
| `backend` | `claude` | resolved from the judge registry; an unknown name is a hard error at engine build |
| `model` | `claude-opus-4-8` | passed to the backend; an invalid model surfaces as a judge-call error |
| `votes` | `1` | must be `>= 1` and **odd**; an even `votes > 1` is rejected (majority is undefined on a tie) |
| `temperature` | `0` | takes effect only on models that accept it (Sonnet 4.6 / Haiku 4.5). Opus-tier rejects a temperature parameter, so determinism there comes from structured output + the vote |

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
