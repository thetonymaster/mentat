# Quickstart Validation: Run Lifecycle

Details: [data-model.md](./data-model.md), [contracts/lifecycle-config.md](./contracts/lifecycle-config.md).

## Prerequisites

- Go 1.25; `make ci` green before starting. POSIX platform (macOS/Linux).
- For e2e: Docker + `make harness-up`.

## Hermetic validation

```sh
go test -race ./internal/driver/ ./internal/steps/ ./internal/engine/ \
        ./internal/registry/ ./internal/report/ ./internal/config/ ./cmd/mentat/
```

Expected red→green coverage:

- driver: helper-process table — never-exits (killed at timeout, pgid empty after
  grace), grandchild-holds-pipe (Wait returns after WaitDelay, output preserved),
  ignores-SIGTERM (SIGKILL escalation).
- engine: run-timeout failure names target/phase/elapsed; parallel batch drives
  < N iterations after a structural error (drive-count assertion).
- steps: scenario ctx reaches Drive/Compare/Aggregate (mock observes ctx deadline);
  judge-phase timeout attribution.
- registry: post-seal Register panics with the sealed message; `-race` clean with
  concurrent readers.
- report: interrupted marker in JSON/HTML/JUnit; temp+rename atomicity.
- config: defaults, per-target override, `"unbounded"` opt-in, typo → parse error.

## Signal handling (automated, hermetic)

```sh
go test ./cmd/mentat/ -run TestSignal
```

Expected: test builds/starts a `mentat run` child on a two-scenario suite with a
slow second scenario, sends SIGTERM, asserts: exit 130, JSON+JUnit reports exist,
scenario 1's result present, `"interrupted": true`, no surviving SUT process.

## Live-harness validation (e2e)

```sh
make harness-up
go test -tags e2e ./e2e/ -run TestHungSUT -v
```

Expected: `sleep`-forever target with `run_timeout: 2s` fails in < 2s+grace wall
clock, failure text names the target and phase, `pgrep` finds no survivor.

## Regression + coverage gate

```sh
make ci        # full suite; e2e wall clock within +5% of baseline
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -3
```
