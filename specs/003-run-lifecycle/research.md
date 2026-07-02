# Phase 0 Research: Run Lifecycle

Design forks resolved against Go stdlib semantics, godog v0.15 capabilities
(verified in the module cache during the audit), and the audit's failure analysis.

## R1. Process-tree termination mechanism (B1)

**Decision**: In the shell driver, start the SUT in its own process group
(`SysProcAttr{Setpgid: true}`), override `Cmd.Cancel` to signal the whole group
(SIGTERM to `-pgid`, escalation to SIGKILL after the kill-grace), and set
`Cmd.WaitDelay = kill_grace` so `Wait` returns even when a descendant still holds
the stdio pipes (Go closes the pipes and returns `ErrWaitDelay` after the delay).

**Rationale**: This is the stdlib-blessed combination for exactly the audit's
failure: `CommandContext` alone kills only the direct child (`go run`), and
buffered stdio waits on grandchildren forever without `WaitDelay`. Process-group
kill covers the tree without external dependencies.

**Alternatives considered**: (a) PTY allocation — heavier, changes SUT behaviour
(TTY detection); (b) pidfd/cgroup-based reaping — Linux-only, out of proportion;
(c) `exec.CommandContext` default behaviour — demonstrated insufficient (audit B1).

## R2. Where the run budget lives and how it's resolved (B1, B2)

**Decision**: Config gains `run_timeout` and `kill_grace` at suite level with
per-target overrides; defaults 5m / 10s; explicit string `"unbounded"` opts out of
the timeout. The engine derives the per-run context (`context.WithTimeout`) inside
`driveOnce` from the scenario context, so the budget composes with scenario
cancellation and the poll timeout (whichever fires first wins).

**Rationale**: The engine is the only place that knows both the target config and
the run boundary; deriving there keeps drivers dumb (they just honor ctx) and
keeps FR-007 phase attribution possible (engine knows which call was in flight).

**Alternatives considered**: godog's own `--stop-on-failure`/format timeouts — do
not exist per-scenario; per-driver hardcoded timeouts — hides policy in the wrong
seam and can't be overridden per target.

## R3. Scenario context threading (B2)

**Decision**: Convert step definitions to godog's context-aware signature
(`func(ctx context.Context, ...) error` — supported since godog v0.13), store the
scenario context in `world` at `sc.Before`, and pass it through `DriveN`,
`Compare`, and `Aggregate`. `ctl.ReplayFeature` already threads its caller context
into godog's `DefaultContext`; once steps stop rebuilding `context.Background()`,
that path works as designed.

**Rationale**: godog cancels the scenario context on suite interruption; using it
makes FR-004/FR-005 fall out of the runner's own machinery instead of a parallel
signal-plumbing system.

**Alternatives considered**: package-level cancellable context set by main —
rejected: breaks service-mode replay (two sources of truth) and testability.

## R4. Signal handling and interrupted reports (B3)

**Decision**: `cmd/mentat` wraps execution in `signal.NotifyContext(ctx, SIGINT,
SIGTERM)`; the godog suite runs with that context (via `DefaultContext`), so
scenario contexts cancel on signal. After `suite.Run()` returns — normally or by
cancellation — report emission always runs; the collector gains an
`Interrupted bool` set when the signal context was cancelled, rendered in JSON
(`"interrupted": true`), HTML banner, and JUnit (suite-level property). Report
files are written to a temp file in the target directory then renamed. A second
signal uses `signal.Reset` semantics (NotifyContext's stop + re-raise) for
immediate exit.

**Rationale**: reports-after-run is the existing structure; making emission
unconditional plus a marker is the smallest change satisfying FR-006 and the
atomicity edge case.

**Alternatives considered**: incremental report writing per scenario — better
crash-resilience but a much larger reporter redesign; deferred (noted as future
work in the contract doc).

## R5. Parallel batch cancellation (B4)

**Decision**: `DriveN`'s parallel path wraps the batch in
`context.WithCancel(ctx)`; the first structural error cancels siblings. Iterations
check the batch context before acquiring the semaphore and before driving.
Already-running SUT processes are allowed to finish their (now bounded by R1/R2)
run — the guarantee is "not yet started ⇒ never starts", per spec FR-008.

**Rationale**: matches the spec's "promptly cancel iterations that have not
started"; killing mid-flight sibling runs for a *structural* error adds complexity
with no verdict value since structural errors abort the batch anyway.

## R6. Registry sealing (B5)

**Decision**: `engine.Build`/`BuildStore` call `registry.Seal()` when wiring
completes; `Register*` after seal panics with a descriptive message
(`"registry: Register called after engine build — registries are sealed at the composition root"`).
Registry maps also move behind an RWMutex so even pre-seal concurrent misuse is
race-free. Tests that legitimately build multiple engines use a test-only
`registry.ResetForTest(t)` helper that requires a `*testing.T`.

**Rationale**: constitution IV allows panics for "true, caller-unreachable
invariants" — post-build registration is exactly a programming error, not runtime
input. The mutex removes the documented race class (steps_test.go:1087) outright.

**Alternatives considered**: returning errors from `Register` — pollutes every
call site for a can't-happen-in-production case; freezing via copied immutable
maps at Build — doesn't protect the pre-Build concurrent case the audit test hit.

## R7. Hermetic subprocess testing pattern

**Decision**: Unit tests exercise the shell driver against helper processes using
the `os.Args[0]` re-exec pattern (`TestHelperProcess` style, as used across the Go
stdlib): scenarios include never-exits, exits-but-grandchild-holds-pipe, and
ignores-SIGTERM. The e2e hung-SUT test uses a `sleep`-forever target in
`features/meta/` with a 2s budget and asserts wall time < budget+grace and no
surviving pgid members.

**Rationale**: re-exec avoids shipping fixture binaries and stays hermetic; the
e2e proves the promise on a real platform.
