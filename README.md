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
