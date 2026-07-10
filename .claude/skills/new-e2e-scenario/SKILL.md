---
name: new-e2e-scenario
description: >-
  Add a live-Tempo e2e scenario to Mentat's `//go:build e2e` suite the right way —
  exec the prebuilt mentatBin (never `go run`), t.Parallel top + each t.Run, a
  backing SUT scenario, and (for the mandatory L3 meta-test) a `{feature, reason}`
  row that proves Mentat goes RED. Use whenever the user wants an end-to-end test:
  "add an e2e scenario", "new .feature test against Tempo", "prove Mentat fails on
  <bad behaviour>", "wire up a happy-path e2e", or when a new comparator needs its
  L3 meta-test. Reach for it before hand-writing an e2e test so the build-tag,
  parallelism, and exec conventions are right by construction.
---

# Adding an e2e scenario

Mentat's e2e suite drives the **whole pipeline** — a `.feature` → engine → driver →
live Tempo → comparators → exit code — against real trace ingestion. Two things
make these tests special: they are I/O-bound (each blocks on Tempo ingesting the
run's spans), and the framework's credibility rests on the **L3 meta-tests**, which
prove Mentat exits non-zero on deliberately bad behaviour. A test framework that
can't go red is decoration.

## Read these first (the conventions live in the code, not in copies)

This table is **illustrative, not exhaustive** — run `ls e2e/` first. Every suite
is a file there (agent vs service, happy vs meta are split across `*_test.go` /
`*_meta_test.go`), and a new one may already cover what you're about to add.

| File | Why |
| --- | --- |
| `e2e/main_test.go` | `TestMain` builds `mentatBin` once. Every scenario execs that binary. |
| `e2e/meta_test.go` | Agent-path L3 meta-table (`TestBadScenariosAreCaught`) — the shape to copy for a "must go red" case. |
| `e2e/orderflow_meta_test.go` | Service-path (http/baggage) L3 meta-table — same shape over the microservice SUT. |
| `e2e/e2e_test.go` | `TestHappyScenarioPasses` — the agent-path "must pass" shape. |
| `e2e/orderflow_test.go` | `TestOrderflowHappyPasses` — the service-path (http/baggage) "must pass" shape. |
| `mentat.yaml` | The targets: `research-agent` (shell/researchbot) and `checkout` (http/orderflow). Your `Given … target` names one of these. |
| `features/` and `features/meta/` | Existing `.feature` files and the Gherkin steps you compose from — don't invent new `Then` phrases here (that's a comparator + step-def change; see `new-comparator`). |

## The non-negotiable test conventions

Every e2e test file/row follows this shape — deviating breaks the suite's speed or
correctness. Copy an existing table (e.g. `orderflow_meta_test.go`) for structure,
but heed the loopvar note below:

- **`//go:build e2e`** at the top, `package e2e`. Unit CI never compiles these.
- **Exec `mentatBin`, never `go run ./cmd/mentat`.** `go run` recompiles+relinks
  the CLI per scenario and, under `-parallel`, serializes every scenario on the
  first cold build. `mentatBin` is built once in `TestMain`.
- **`cmd.Dir = ".."`** — the binary runs from the repo root so relative feature
  paths and `mentat.yaml` resolve.
- **`t.Parallel()` at the top of the test AND inside each `t.Run`.** Here this is a
  real ~7× wall-clock win (not a unit-test style preference): scenarios overlap
  their Tempo-ingestion waits. Skip it only if a case uses `t.Setenv`/`t.Chdir`.
- **2-minute `context.WithTimeout`**, and distinguish `DeadlineExceeded` from a
  genuine failure in the assertion (a timeout is a different bug than a wrong exit
  code — say so in the `t.Fatalf`, and make the message name the suite so agent and
  service timeouts read differently).
- **Omit the loopvar capture (`c := c` / `tc := tc`).** Every existing e2e table
  still carries it, but it's unnecessary since Go 1.22 and CLAUDE.md forbids it
  (this module is on 1.25). Copy the *structure* of those files, not that line.

## Pick the scenario kind

**First check it isn't already covered.** Run `grep -rl '<feature>.feature' e2e/`.
If a test already drives that feature, refactor *that* test to the canonical shape
(table-driven, both `t.Parallel()` positions) instead of adding a duplicate — a
second test that passes for the same reason is noise, not coverage.

**A) "Must go red" (L3 meta-test) — the mandatory kind for a new assertion.**
The feature drives a scenario that violates *exactly one* assertion, and the test
asserts mentat exits non-zero AND its combined output contains a `reason`
substring. Steps:
1. Create `features/meta/<name>.feature` (agent path) or
   `features/meta/orderflow/<name>.feature` (service path) that trips one comparator.
2. Add a `{feature, reason}` row to the matching table (`TestBadScenariosAreCaught`
   or `TestOrderflowBadScenariosAreCaught`).
3. Set `reason` to an actual substring of the failure mentat prints — verify by
   running mentat on the feature and reading the output, not by guessing. A wrong
   substring makes the test pass for the wrong reason or fail spuriously.

**B) "Must pass" (happy path).** The feature drives a good scenario; the test
asserts exit zero. Put it in the file matching the SUT path, mirroring the
`*_meta_test.go` split — agent-path happy → `e2e/e2e_test.go`
(`TestHappyScenarioPasses`); service-path happy → `e2e/orderflow_test.go`
(`TestOrderflowHappyPasses`). Don't mix a `checkout.feature` row into the agent
table (it would compile and pass, but it muddies the agent/service separation).

## The backing SUT scenario must exist

`When I run scenario "<name>"` resolves to a scenario the SUT actually knows —
these tests do not mock. If `<name>` doesn't exist yet, that is a prerequisite:

- **Agent path (`research-agent`, shell):** add
  `tracelab/researchbot/scenarios/<name>.yaml` (see the existing `happy.yaml`,
  `wrong_order.yaml`, …). The shell SUT emits `gen_ai.*` spans for it.
- **Service path (`checkout`/orderflow, http+baggage):** the orderflow microservice
  must produce the behaviour; `testdata/traces/orderflow/<name>.json` is the
  captured fixture used by unit tests. Requires `make harness-up` (Tempo +
  Collector) and the orderflow service running.

Adding a SUT scenario or a captured fixture is `tracelab`/scaffolding work —
route it to `go-coder`. Wiring a brand-new assertion is `new-comparator` work.

## Run it (observable reality, not "should pass")

```
make harness-up                                   # Tempo + Collector (+ orderflow for service path)
go test -tags e2e ./e2e/ -run <TestName> -parallel 16 -v
make harness-down
```

Confirm the new case behaves: a "must go red" case FAILS mentat and prints the
`reason`; a "must pass" case exits zero. Paste the run output — don't assert
success from memory.

## Definition of done

- New file/row is `//go:build e2e`, execs `mentatBin`, `cmd.Dir=".."`, `t.Parallel()`
  top + each `t.Run`, 2-min ctx with a distinct timeout message.
- The backing SUT scenario (agent yaml or service fixture) exists.
- For a meta-test: the `reason` substring is copied from real mentat output.
- `go test -tags e2e ./e2e/` green locally with `make harness-up`.
- `gofmt`/`vet`/lint clean; Conventional Commit (`test(e2e): …`), files staged
  individually, no AI attribution.
