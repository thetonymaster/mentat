# T001 Baseline — Run Lifecycle

Recorded at the start of feature `003-run-lifecycle` implementation, on branch
`003-run-lifecycle`, before any lifecycle code was added. Reference for the
SC-006 no-regression gate (T025).

## Environment

- Go 1.26.1 (module targets 1.25), darwin/arm64.
- golangci-lint 2.12.2; mockgen present; Docker running (`make harness-up` OK).

## `make ci` — GREEN

- `golangci-lint run ./...`: clean.
- `go test ./... -race`: all packages pass.
- Coverage gate: **PASS**, total **80.9%**, every non-exempt package ≥ 80%.

## e2e wall-clock baseline (SC-006)

```text
go test -tags e2e ./e2e/   →   ok  ...e2e  51.831s
```

- **Baseline: 51.83s** (harness up, Tempo ingesting).
- SC-006 bound (+5%): a post-feature e2e run must stay **≤ ~54.4s** wall clock.
- Note: wall time is dominated by per-scenario Tempo trace-ingestion waits; it is
  sensitive to machine load. Re-measure T025 on the same machine, harness warm.

## T025 result (SC-006) — no regression

At implementation end the same e2e suite measured **~60s** — above the +5% bound.
Investigation (RULE 0) showed this is **not** a feature-003 regression but early-vs-late
session machine load:

- The shell driver was profiled in isolation: `cmd.Wait`=147ms, `copyWG`=83ns per run
  — the lifecycle additions (Setpgid / Cancel / WaitDelay / group-reap) add no
  measurable per-run cost; e2e wall time is dominated by Tempo resolve polling
  (`stableFor` ingestion waits, ~10s/HTTP scenario), which this feature does not touch.
- Controlled A/B: `git stash`-ing ALL feature-003 work and re-running the *original*
  code's existing e2e under the same conditions also measured **~60s** (60.5s, 59.9s),
  identical to the feature branch (~60s). So the 51.83s baseline was an early-session
  low-load measurement; under identical current load, original == feature-003.

**Conclusion: SC-006 satisfied — feature 003 introduces no e2e wall-clock regression.**
The absolute delta from 51.83s is environmental (machine load), verified by A/B.
