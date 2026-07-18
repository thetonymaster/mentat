# Feature Specification: Trace Completeness Contract — Flush Barrier for Sound Absence Assertions

**Feature Branch**: `008-trace-completeness`

**Created**: 2026-07-03

**Status**: Draft

**Input**: User description: "Trace-completeness contract — flush barrier for sound absence assertions. Mentat's absence and aggregate assertions (\"the tool X is never called\", \"exactly N\", budget sums over tokens/cost, error-span counts) are only as sound as the claim that the resolved Evidence forest is COMPLETE. Today completeness is inferred from span-count stability polling alone (stable for K rounds), which is a heuristic: an SUT (or a sub-agent it spawned) using a batching exporter with a multi-second flush interval can export spans AFTER the stability window closes, so the assertion passes against a partial forest — a silent false green, the same defect class feature 002 (verdict integrity) exists to eliminate, one level deeper. Nothing today can detect a partial forest because nothing knows how many spans should exist. This feature establishes an explicit completeness contract per driver adapter: (1) for spawned-process adapters, process exit plus a bounded post-exit export barrier is the primary completeness signal; (2) for request-scoped adapters, absence and aggregate assertions carry explicitly weaker, documented semantics plus a configurable settle window; (3) an opt-in strict mode where the SUT declares its total span count in-trace and resolution hard-fails on any mismatch. Constraints: feature 002's stability-gate guarantees preserved exactly; comparators remain Evidence-only and unchanged. The tracelab harness gains a deliberately late-exporting bad SUT and the L3 meta-test proves absence assertions never pass green against a partial forest."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Spawned-Process Runs Judge Only Complete Evidence (Priority: P1)

A test author drives a spawned agent (shell target) whose telemetry uses a batching
exporter. Part of the run's spans export immediately; the rest flush seconds later, at
or near process exit. The author's scenario contains absence assertions ("the tool X is
never called", "exactly N spans") and aggregate assertions (token/cost budget sums,
error-span counts). Mentat must not conclude trace resolution for that run until the
SUT process has exited and a bounded post-exit settle window has elapsed — so every
verdict is judged against the complete span forest, or the run fails loudly.

**Why this priority**: This closes the primary silent-false-green vector on the primary
adapter. An absence assertion that can pass against a partial forest is worse than no
assertion at all — it manufactures false confidence. It is the same defect class feature
002 (verdict integrity) exists to eliminate, one layer deeper: 002 makes the stability
gate honest; this story makes the thing the gate observes trustworthy.

**Independent Test**: Drive the harness's deliberately late-flushing SUT (an early decoy
export, then a delayed final batch containing a call to a forbidden tool) with an
absence assertion on that tool. The scenario must go RED on every repetition — never
green against the partial forest.

**Acceptance Scenarios**:

1. **Given** a spawned SUT that exports a first batch of spans immediately and a final
   batch (containing a call to tool "X") only at process exit, **When** the scenario
   asserts tool "X" is never called, **Then** the verdict is FAIL, and it is FAIL on
   every repetition — resolution never concludes against the earlier partial forest.
2. **Given** a spawned SUT process that has not yet exited, **When** trace resolution
   observes a span count that would otherwise qualify as stable, **Then** resolution
   does not conclude; completeness observation for the run begins only after the
   process has exited.
3. **Given** a well-behaved spawned SUT that flushes its telemetry on shutdown and
   exits promptly, **When** the scenario runs, **Then** the barrier's added wall-clock
   cost does not exceed the configured post-exit settle window.
4. **Given** a spawned SUT that produces zero spans within the resolution timeout,
   **When** resolution times out, **Then** the existing zero-trace hard error is
   preserved and the error states which completeness barrier it was still waiting on.

---

### User Story 2 - Request-Scoped Runs State Their Weaker Guarantee Honestly (Priority: P2)

A test author targets a long-running service through a request-scoped adapter
(http/grpc). The service cannot signal "all spans for this run have been exported", so
absence and aggregate verdicts on such targets are inherently bounded by the ingestion
window. Mentat must say so explicitly — in the report output for those verdicts and in
the documentation — and must let the author widen the post-response settle window per
target when their ingestion pipeline is slow.

**Why this priority**: The weakness cannot be fixed structurally without SUT
cooperation (that is User Story 3). What Mentat can and must do is refuse to overstate
its guarantee: an honestly-qualified verdict preserves trust; an unqualified one is a
quiet lie about certainty.

**Independent Test**: Run an absence assertion against a request-scoped target and
inspect the report: the verdict carries an explicit ingestion-window-bounded qualifier.
Raise the per-target settle window and observe that resolution waits correspondingly
longer before concluding.

**Acceptance Scenarios**:

1. **Given** a scenario with an absence or aggregate assertion against a request-scoped
   target without strict mode, **When** the report is produced, **Then** that verdict —
   pass or fail — is annotated as bounded by the ingestion window.
2. **Given** a per-target settle window configured to a larger value, **When** the
   scenario runs, **Then** resolution keeps observing for late spans for at least that
   window after the response before the stability gate may conclude.
3. **Given** no explicit settle-window configuration, **When** the scenario runs,
   **Then** a documented non-zero default applies — never a silent zero.

---

### User Story 3 - Strict Mode: The Run Declares Its Own Span Count (Priority: P3)

An author who controls the SUT opts into strict completeness per target: the SUT emits,
inside its own trace, exactly one machine-readable declaration of the total number of
spans the run produced (the span-count sentinel). Resolution then refuses to produce
any verdict until the resolved forest reaches exactly the declared count, and fails
hard with a descriptive error if it cannot within the timeout. No verdict over partial
evidence, ever.

**Why this priority**: This is the strongest guarantee — exact completeness — but it
requires SUT cooperation, so it is opt-in and lands after the zero-cooperation
improvements of Stories 1 and 2.

**Independent Test**: The harness SUT emits a sentinel declaring N spans while a
delayed batch withholds some of them past the timeout: the run must end in a hard error
naming the run id, declared count, and observed count — no verdict. With full arrival,
verdicts proceed normally.

**Acceptance Scenarios**:

1. **Given** strict mode enabled and a sentinel declaring N spans, **When** the
   resolved forest reaches exactly N spans, **Then** resolution concludes and
   comparators judge the complete evidence normally.
2. **Given** strict mode enabled and a sentinel declaring N spans, **When** only M < N
   spans arrive before the resolution timeout, **Then** the run fails hard with an
   error naming the run id, the declared count N, the observed count M, and the
   elapsed time — no verdict is produced.
3. **Given** strict mode enabled, **When** no sentinel has arrived by the resolution
   timeout, **Then** the run fails hard with an error stating that strict mode expected
   a span-count declaration and none was found.
4. **Given** more than one sentinel for the same run, **When** resolution encounters
   them, **Then** the run fails hard with an error naming the ambiguity.
5. **Given** strict mode enabled and a sentinel declaring N spans, **When** more than N
   spans arrive, **Then** the run fails hard — the declaration is violated and the
   evidence is ambiguous.
6. **Given** strict mode enabled on a request-scoped target, **When** the report is
   produced, **Then** its verdicts do not carry the ingestion-window-bounded qualifier
   — the declared count makes completeness exact.

---

### Edge Cases

- **Grandchild processes that outlive the SUT**: a spawned SUT may itself spawn
  children (wrapper scripts, sub-agents) that keep exporting after the parent exits.
  The process-exit barrier covers the whole process group only once feature 003 (run
  lifecycle) delivers process-group semantics; until then "exit" means the direct
  child, and the settle window is the only mitigation. This limit must be documented.
- **Pipeline batching after SUT exit**: spans can legitimately reach the trace store
  seconds after process exit (collector/back-end batching). The post-exit settle window
  plus the preserved stability gate exist precisely to absorb this.
- **Sentinel in the late batch**: strict mode must keep polling for the sentinel itself
  until the timeout — it must not conclude "no sentinel" from an early partial forest.
- **Settle window configured to zero**: permitted, but documented as the weakest
  configuration (barrier reduces to process exit plus the stability gate).
- **Multi-root forests**: the declared span count applies to the whole run — all roots
  merged — not to any single trace within it.
- **SUT exits with a failure code**: the completeness barrier is unchanged; drive-failure
  semantics belong to features 002/003 and must not be weakened by this feature.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: For spawned-process targets, the system MUST NOT conclude trace
  resolution while the SUT process is still running; completeness observation for the
  run begins only after process exit.
- **FR-002**: After SUT process exit, the system MUST keep observing for late-arriving
  spans for a bounded, per-target-configurable post-exit settle window before the
  stability gate may conclude; a documented non-zero default applies when unconfigured.
- **FR-003**: The documented contract for spawned-process targets MUST require the SUT
  to flush and shut down telemetry export before exiting, and MUST state the
  consequence of violating it (late spans may be missed unless strict mode is used).
- **FR-004**: Absence and aggregate verdicts for request-scoped targets without strict
  mode MUST carry an explicit human-readable qualifier in report output stating the
  verdict is bounded by the ingestion window; the documentation MUST define exactly
  what that bound means.
- **FR-005**: Authors MUST be able to configure a per-target post-response settle
  window for request-scoped targets; a documented non-zero default applies when
  unconfigured.
- **FR-006**: Strict mode MUST be opt-in per target. When enabled, the run's trace MUST
  contain exactly one machine-readable declaration of the run's total span count.
- **FR-007**: In strict mode, the system MUST NOT produce any verdict until the
  resolved forest's span count equals the declared count; if equality is not reached
  within the resolution timeout, the run MUST fail hard with an error naming the run
  id, the declared count, the observed count, and the elapsed time.
- **FR-008**: In strict mode, a missing sentinel at timeout, multiple sentinels for one
  run, and an observed count exceeding the declared count MUST each produce a distinct,
  descriptive hard error — never a verdict over the ambiguous evidence.
- **FR-009**: Strict-mode verdicts MUST NOT carry the ingestion-window-bounded
  qualifier, including on request-scoped targets — the declared count makes
  completeness exact.
- **FR-010**: The stability-gate guarantees delivered by feature 002 MUST be preserved
  exactly; completeness barriers are additive conditions evaluated in addition to the
  gate and MUST NOT weaken, bypass, or replace it.
- **FR-011**: Comparators MUST remain Evidence-only and unchanged by this feature;
  completeness enforcement lives entirely in the drive and resolution layers
  (Constitution Principle I).
- **FR-012**: The test harness MUST gain a deliberately late-exporting SUT scenario (an
  early decoy export followed by a delayed final batch containing behaviour that
  violates an absence assertion), and the L3 meta-test suite MUST prove that such a run
  can never produce a green verdict from a partial forest (Constitution Principle V).
- **FR-013**: Every timeout or failure raised by a completeness barrier MUST name which
  barrier was unsatisfied (process exit, settle window, sentinel arrival, or count
  match) and the concrete values involved (Constitution Principle IV).

### Key Entities

- **Completeness barrier**: a per-adapter-kind condition that must hold before the
  stability gate may conclude — process exit (spawned), settle window elapsed (both
  kinds), or declared span count reached (strict mode).
- **Settle window**: the bounded post-exit (spawned) or post-response (request-scoped)
  period during which late spans are still awaited; per-target configurable with a
  documented default.
- **Span-count sentinel**: a machine-readable, in-trace declaration of the run's total
  span count; at most one per run; the contract of strict mode.
- **Completeness qualifier**: the report annotation marking a verdict as bounded by the
  ingestion window rather than exact.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The late-flush meta-scenario never passes: across 20 consecutive L3 runs
  against the late-exporting SUT, absence assertions produce zero green verdicts — every
  run either fails on complete evidence or ends in a loud, descriptive error. Enforced by
  the e2e meta-test's CI-tunable repeat count (`MENTAT_L3_RUNS`: default 3 for PR CI, set
  to 20 in the release/nightly lane) — the criterion's threshold and its gate are the
  same test, so 20 is machine-checked, not only a manual proof.
- **SC-002**: Zero verdicts are produced for spawned-process runs while the SUT is
  still running, demonstrated by a harness scenario that holds the process alive past
  the point where the span count would otherwise appear stable.
- **SC-003**: 100% of absence and aggregate verdicts on request-scoped targets without
  strict mode carry the ingestion-window-bounded qualifier in every emitted report
  format that shows verdict reasons — the json, html, and junit reporters — on pass
  and fail alike.
- **SC-004**: In strict mode, 100% of count mismatches, missing sentinels, and
  duplicate sentinels end in a hard error naming the expected and observed values;
  zero verdicts are ever computed from partial evidence.
- **SC-005**: For a well-behaved SUT (flushes on exit), the completeness barrier adds
  no more wall-clock time per run than the configured settle-window default, and all
  pre-existing unit, e2e, and meta suites still pass unchanged apart from the new
  scenarios.

## Assumptions

- Standard telemetry SDK shutdown flushes pending spans on process exit; SUTs that
  cannot guarantee a flush-on-exit are served either by strict mode or by the
  documented weaker semantics — the framework never silently guesses.
- "Process exit" means the direct child process today; full process-group coverage
  (grandchildren, wrapper scripts) arrives with feature 003 (run lifecycle). This
  feature does not block on 003, but its guarantee widens automatically when 003 lands.
- Feature 002 (verdict integrity) defines the stability gate this feature builds on;
  002 must land first — the barriers here are additive to an honest gate, not a
  substitute for one.
- The sentinel's concrete representation (attribute name, span shape) is decided at
  planning time; this spec requires only that it be machine-readable, carried in the
  run's own trace, and unique per run.
- Default settle-window durations (post-exit and post-response) are decided at
  planning time; both must be configurable per target and documented.
- The barrier applies uniformly to the whole run — presence-only scenarios wait the
  same barriers as absence scenarios. No per-assertion bypass exists in v1; uniform
  resolution keeps verdict semantics simple and predictable.
