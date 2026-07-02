# Feature Specification: Run Lifecycle — Bounded Runs, Clean Cancellation, No Orphaned SUTs

**Feature Branch**: `003-run-lifecycle`

**Created**: 2026-07-01

**Status**: Draft

**Input**: User description: "Run lifecycle — bounded runs, clean cancellation, no orphaned SUTs. The 2026-07-01 audit (docs/audits/2026-07-01-codebase-audit.md, cluster B, findings B1–B5) found that Mentat cannot bound or cancel anything it starts: hung SUTs hang the whole run forever and leak processes; scenario timeouts cannot exist because the per-scenario context is discarded; SIGTERM orphans the SUT (still exporting spans, still spending LLM tokens) and loses all reports; parallel @runs keeps driving doomed iterations; seam registries rely on a comment for thread safety."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A hung SUT fails the scenario instead of hanging Mentat (Priority: P1)

A test author's SUT (an agent CLI or a service under `go run`) stops responding —
it never exits, or a child process it spawned keeps running after it exits. Today
the whole `mentat run` blocks forever (audit B1) because nothing bounds the run and
the runner waits on output pipes held open by surviving child processes. After this
feature, every SUT run is bounded by a configurable timeout; when it expires the
scenario fails with a descriptive timeout error naming the target and elapsed time,
and the SUT's **entire process tree** is terminated — no orphan survives.

**Why this priority**: A CI job that hangs until the CI-level kill is the most
expensive failure mode: it wastes the full job timeout, loses all reporting, and
for LLM SUTs burns tokens the whole time.

**Independent Test**: Configure a target whose command sleeps forever (spawning a
child that also sleeps); set a short run timeout; the scenario must fail within the
timeout and no process from the tree may remain afterwards.

**Acceptance Scenarios**:

1. **Given** a target whose process never exits, **When** the configured run
   timeout expires, **Then** the scenario fails with an error naming the target and
   the elapsed time, and `mentat run` proceeds to the next scenario.
2. **Given** a target that spawns a grandchild holding the output pipes and then
   exits, **When** the run completes or is cancelled, **Then** the runner does not
   wait on the grandchild beyond a bounded grace period and the grandchild is
   terminated with the rest of the process tree.
3. **Given** no timeout configured, **When** a scenario runs, **Then** a documented
   default run timeout applies (unbounded waits require explicit opt-in, not
   silence).

---

### User Story 2 - Interrupting Mentat is safe: tree reaped, reports preserved (Priority: P1)

An operator (or CI) sends SIGINT/SIGTERM to `mentat run` mid-suite. Today the SUT
process is orphaned — it keeps exporting spans into the shared trace store and, for
agent SUTs, keeps spending LLM tokens — and every report format is lost because
reports are written only after the full suite returns (audit B3). After this
feature, an interrupt stops in-flight work promptly, terminates the SUT process
tree, and still writes all configured reports containing the scenarios that
completed, with the interrupted run clearly marked.

**Why this priority**: CI cancellation is routine, not exceptional; every
cancellation today costs orphaned workloads, polluted shared trace data, real LLM
spend, and the loss of all results already earned.

**Independent Test**: Start a multi-scenario suite, SIGTERM it during the second
scenario; verify the first scenario's result appears in the report files, the
report is marked interrupted, the exit code is non-zero, and no SUT process
survives.

**Acceptance Scenarios**:

1. **Given** a running suite, **When** SIGINT or SIGTERM arrives, **Then** the
   in-flight scenario is cancelled, the SUT process tree is terminated within a
   bounded grace period, and the process exits non-zero.
2. **Given** completed scenarios before the interrupt, **When** the process exits,
   **Then** every configured report (console summary, JUnit, JSON, HTML) exists and
   contains the completed results plus an explicit "interrupted" marker.
3. **Given** a second interrupt while shutdown is in progress, **When** it arrives,
   **Then** the process exits immediately (standard force-quit escalation).

---

### User Story 3 - Scenario-level timeout reaches everything the scenario does (Priority: P2)

A test author sets a per-scenario or per-target time budget in the configuration.
Today no setting can exist because the scenario's cancellation context is discarded
at the top of every drive/assert step (audit B2) — SUT runs, trace polling, and LLM
judge calls all run unbounded. After this feature, one configured budget bounds the
whole scenario: driving, trace resolution, assertions, and judge calls all stop
promptly when it expires, and replaying a feature through the service interface
honors its caller's cancellation.

**Why this priority**: Depends on US1's process-tree control to be meaningful; on
its own it converts "hang" into "bounded fail" for the non-subprocess work (judge
calls, polling).

**Independent Test**: Set a scenario timeout shorter than the SUT's runtime; the
scenario fails with a timeout error attributing the phase (drive/resolve/judge)
that consumed the budget.

**Acceptance Scenarios**:

1. **Given** a configured scenario timeout, **When** the SUT run exceeds it,
   **Then** the scenario fails naming the phase and elapsed time, and later
   scenarios run normally.
2. **Given** a scenario whose judge call stalls, **When** the timeout expires,
   **Then** the judge call is cancelled and the scenario fails with the timeout
   attributed to the judge phase.
3. **Given** a caller cancelling a service-mode replay, **When** cancellation
   arrives, **Then** the replay stops promptly (no dead context path).

---

### User Story 4 - Doomed batches stop early; registries cannot race (Priority: P3)

A `@runs(N, parallel)` batch hits a structural failure (unknown target, bad
adapter) on one iteration — today the remaining iterations still drive the SUT to
completion before the batch reports the error (audit B4), paying for runs whose
results are discarded. Separately, the framework's internal seam registries are
mutable global state protected only by a "build once before concurrency" comment
that a parallel test has already violated once (audit B5). After this feature,
sibling iterations stop promptly once the batch is doomed, and mutating a registry
after build time either is impossible or fails loudly.

**Why this priority**: Real costs (wasted LLM runs, a latent data race) but narrow
triggers; neither produces wrong verdicts today.

**Independent Test**: A parallel batch with a structurally failing iteration
completes without driving all N iterations; a test that registers a seam after
build time gets a loud failure instead of a race.

**Acceptance Scenarios**:

1. **Given** a parallel `@runs(N)` batch, **When** one iteration fails
   structurally, **Then** iterations not yet started do not drive the SUT and the
   batch returns the structural error promptly.
2. **Given** a fully built engine, **When** code attempts to register a new seam
   implementation afterwards, **Then** the attempt fails loudly (error or panic
   with a descriptive message) instead of mutating shared state.

---

### Edge Cases

- Timeout expiring exactly as the SUT exits normally: the run's real result wins if
  the process already exited; otherwise the timeout failure applies — never both.
- A SUT that ignores the polite termination signal must still die: escalation to
  forceful kill after the grace period.
- Grace periods must be bounded and configurable; the sum (timeout + grace) is the
  worst-case scenario wall time and must be documented.
- Interrupt arriving between scenarios (no in-flight SUT): reports still written,
  marker still set.
- Interrupt during report writing itself: report files must not be left corrupt
  (write-then-rename or equivalent atomicity at the file level).
- Timeout attribution must be phase-accurate even when phases overlap (drive
  finished, resolve in progress).
- Serial `@runs(N)` already stops at the first structural error — must stay that
  way (no regression).
- The bounded-run default must be generous enough not to break slow-but-healthy
  agent runs (documented default, config override per target).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every SUT run MUST be bounded by a timeout resolved from
  configuration (per-target override, suite default, documented built-in default).
  Expiry fails the scenario with an error naming the target and elapsed time. *(B1, B2)*
- **FR-002**: When a run ends (normal exit, timeout, or cancellation), the SUT's
  entire process tree MUST be terminated: polite signal first, forceful kill after
  a bounded grace period. No process from the tree may outlive the run by more
  than the grace period. *(B1)*
- **FR-003**: The runner MUST NOT wait indefinitely on output pipes held by
  surviving descendants; after process exit plus a bounded delay, captured output
  is finalized and the run completes. *(B1)*
- **FR-004**: The scenario's cancellation signal MUST propagate to every operation
  the scenario performs — SUT drive, trace resolution, comparator evaluation, and
  judge calls. No scenario-scoped operation may run on an unbounded background
  context. *(B2)*
- **FR-005**: Service-mode feature replay MUST honor its caller's cancellation
  end-to-end. *(B2)*
- **FR-006**: The CLI MUST handle SIGINT and SIGTERM: cancel in-flight work, reap
  the SUT process tree, write all configured reports containing completed
  scenarios with an explicit interrupted marker, and exit non-zero. A second
  signal MUST force immediate exit. *(B3)*
- **FR-007**: Timeout and interruption failures MUST attribute the phase that was
  in flight (drive / resolve / assert / judge) and the elapsed time. *(B1, B2)*
- **FR-008**: In a parallel multi-run batch, a structural failure MUST promptly
  cancel iterations that have not started driving; the batch returns the
  structural error without driving all remaining iterations. *(B4)*
- **FR-009**: Seam registry mutation after the composition root has finished
  building MUST be impossible or fail loudly with a descriptive message; the
  guarantee MUST be enforced by the code, not by a comment. *(B5)*
- **FR-010**: All new lifecycle behaviours MUST be covered by hermetic tests
  (fake/stub SUT processes, no live trace store) plus at least one live-harness
  test proving a hung SUT scenario fails within its budget with no surviving
  process.

### Key Entities

- **Run budget**: the effective timeout for one SUT run (target override → suite
  default → built-in default) plus the kill grace period.
- **Process tree**: the SUT command and every descendant it spawns; the unit of
  termination.
- **Interrupted report**: a report artifact carrying completed results plus an
  explicit marker that the suite did not run to completion.
- **Phase attribution**: which scenario phase (drive / resolve / assert / judge)
  consumed the budget when a timeout or interrupt fired.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A never-exiting SUT scenario fails within its configured timeout
  plus grace period (measured wall clock), and a post-run process inspection finds
  zero surviving processes from the SUT tree.
- **SC-002**: SIGTERM during a multi-scenario suite yields: all configured report
  files present, completed scenarios' results intact, an interrupted marker, and a
  non-zero exit — verified in an automated test.
- **SC-003**: With a scenario timeout configured, every scenario-scoped operation
  observes cancellation: drive, resolve, and judge phases each demonstrably stop
  within the budget in tests (three phase-specific timeout tests).
- **SC-004**: A parallel batch with a structural failure on iteration 1 drives
  strictly fewer than N iterations (verified by drive-count assertion).
- **SC-005**: Post-build registry mutation fails loudly in a test; the race
  documented in the audit can no longer be reproduced under the race detector.
- **SC-006**: Entire pre-existing suite passes unchanged; e2e wall-clock time does
  not regress by more than 5%.

## Assumptions

- Default run timeout: 5 minutes per SUT run, kill grace 10 seconds — generous for
  agent SUTs, hard-stops runaway CI; both configurable (exact values are
  implementation-tunable, the *existence* of documented defaults is the
  requirement).
- "Unbounded" remains available as an explicit opt-in configuration value, never
  the silent default.
- Process-tree termination is specified for POSIX platforms (the supported dev/CI
  targets today); Windows support is out of scope.
- Report atomicity is file-level (no partial/corrupt files); cross-file atomicity
  (all-or-nothing across formats) is not required.
- Registry immutability applies after the composition root completes; test code
  building multiple engines serially remains supported.
- Findings A*, C*, D*, E*, G1 from the audit are out of scope (separate features).
  A3's deadline hard-error (feature 002) composes with this feature's timeouts:
  resolve stops at whichever bound fires first.
