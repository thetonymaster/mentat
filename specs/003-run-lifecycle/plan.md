# Implementation Plan: Run Lifecycle — Bounded Runs, Clean Cancellation, No Orphaned SUTs

**Branch**: `003-run-lifecycle` | **Date**: 2026-07-01 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-run-lifecycle/spec.md`

## Summary

Give Mentat control over everything it starts (audit cluster B, B1–B5): bound every
SUT run with a configured timeout and kill its whole process tree on exit/timeout/
cancel (process-group + wait-delay semantics in the shell driver); thread godog's
per-scenario context through drive/compare/aggregate so one budget bounds SUT,
polling, and judge calls; install signal handling in `cmd/mentat` that cancels
in-flight work, reaps the tree, and still writes all reports with an interrupted
marker; cancel doomed parallel `@runs(N)` batches promptly; and make post-build
seam-registry mutation fail loudly instead of racing.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: `os/exec` (`Cmd.Cancel`, `Cmd.WaitDelay`,
`SysProcAttr{Setpgid}` — all stdlib), `os/signal` (`signal.NotifyContext`), godog
v0.15 context-aware step signatures, existing config loader

**Storage**: config additions only (`run_timeout`, `kill_grace`, per-target
override); no data migrations

**Testing**: hermetic unit tests with stub subprocesses (`sleep`-style helper
binaries via `go test` TestMain or `os.Args[0]` re-exec pattern), gomock for seams;
one `//go:build e2e` hung-SUT scenario; race detector on registry tests

**Target Platform**: POSIX (macOS dev, Linux CI); Windows out of scope per spec

**Project Type**: single Go module — CLI over library packages

**Performance Goals**: no e2e wall-clock regression >5% (SC-006); signal path adds
no steady-state cost

**Constraints**: default budgets must not break slow-healthy agent runs (5m/10s
defaults, per-target override, explicit unbounded opt-in); report files must not
be corrupt on interrupt (write-to-temp + rename)

**Scale/Scope**: 6 packages touched (`driver`, `steps`, `engine`, `registry`,
`report`, `config`) + `cmd/mentat` + e2e; ~10 red→green pairs

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS (no violations).*

- **I. Evidence-Only Comparators**: PASS. Comparators receive a context they must
  respect but still consume Evidence only; no store/driver access is added.
- **II. Trace Is a Forest, Tag-First**: PASS — untouched. Resolve keeps its
  contract; this feature only ensures cancellation reaches it.
- **III. Seams Are Interfaces, Wired Once**: PASS — strengthened. FR-009 turns the
  build-once discipline from a comment into enforced behaviour (registries seal at
  the end of `engine.Build`).
- **IV. No Silent Fallbacks**: PASS. Timeouts/interrupts produce descriptive,
  phase-attributed errors; no failure mode is downgraded to a warning. The
  interrupted report is an explicit marker, not silently-missing data.
- **V. Test-First & Hermetic**: PASS. Stub-subprocess unit tests are hermetic; the
  hung-SUT proof is e2e-gated; each finding lands red→green; coverage floor
  re-checked per touched package.

## Project Structure

### Documentation (this feature)

```text
specs/003-run-lifecycle/
├── plan.md              # This file
├── research.md          # Phase 0: process-tree kill, context threading, seal semantics
├── data-model.md        # Phase 1: config fields, report marker, registry states
├── quickstart.md        # Phase 1: validation guide
├── contracts/
│   └── lifecycle-config.md   # config keys, defaults, signal/exit-code contract, error shapes
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/
├── driver/         # shell.go: Setpgid process group, Cmd.Cancel (negative-pid kill), WaitDelay
├── steps/          # steps.go: context-aware step defs (ctx first arg), per-scenario budget
├── engine/         # engine.go: DriveN internal cancellation for parallel batches
├── registry/       # registry.go: Seal() — post-seal Register fails loudly
├── report/         # collector/emit: interrupted marker; atomic file writes
└── config/         # run_timeout / kill_grace / per-target override parsing + defaults

cmd/mentat/         # signal.NotifyContext, report emission on interrupt, exit codes
internal/ctl/       # replay.go: verify caller ctx now reaches steps (dead path revived)
e2e/                # hung-SUT scenario: fails within budget, no surviving process
```

**Structure Decision**: existing single-module layout; lifecycle control lands in
the package that owns each seam; no new packages.

## Complexity Tracking

No constitution violations — table intentionally empty.
