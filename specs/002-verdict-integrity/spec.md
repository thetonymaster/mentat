# Feature Specification: Verdict Integrity — Eliminate Silent False Verdicts

**Feature Branch**: `002-verdict-integrity`

**Created**: 2026-07-01

**Status**: Draft

**Input**: User description: "Verdict integrity — eliminate silent false verdicts. Mentat's product is trustworthy pass/fail verdicts over SUT traces; the 2026-07-01 audit (docs/audits/2026-07-01-codebase-audit.md, cluster A, findings A1–A8, plus F3/F5) found ways verdicts can silently lie: error-status assertions never fire on live traces, harness failures are swallowed into green, partial trace ingestion returns as success, wide trace searches truncate silently, the span-kind selector is dead, failed runs feed fabricated zeros into aggregates, malformed fixtures load silently, and report derivation can flip a passing verdict."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Error and kind assertions reflect the real trace (Priority: P1)

A test author writes `no span has status "ERROR"` (or a `MaxErrors` budget, a CEL
expression over `errors`, or a `span.status=Error` / `span.kind=...` shape selector)
against a live SUT run. Today these assertions evaluate a status vocabulary the live
trace store never produces (audit A1) and a span-kind field that is never populated
(audit A5), so error assertions are permanently green and kind assertions are
permanently green-or-red regardless of SUT behaviour. After this feature, span status
and span kind observed by every assertion faithfully represent what the SUT emitted,
in one canonical vocabulary, whether evidence comes from the live store or a fixture.

**Why this priority**: This is the worst class of defect a test framework can have — a
user's error-detection suite passes forever while the SUT errors freely. It directly
falsifies Mentat's core promise.

**Independent Test**: Drive a SUT that emits one errored span through the live harness;
assert `no span has status "ERROR"` fails and names the span. Repeat with a
kind-based selector. Deliverable value: error assertions become trustworthy.

**Acceptance Scenarios**:

1. **Given** a live SUT run containing one span with an error status on the wire,
   **When** the scenario asserts `no span has status "ERROR"`, **Then** the scenario
   fails and the reason names the errored span.
2. **Given** the same run, **When** a `MaxErrors: 0` budget or a CEL expression using
   `errors` evaluates, **Then** the errored span is counted (count ≥ 1).
3. **Given** a live SUT run with a server-kind root span, **When** a shape assertion
   uses `span.kind`, **Then** the selector matches that span (an `exists` assertion
   passes; an `absent` assertion fails).
4. **Given** an existing fixture using the legacy status spelling, **When** the fixture
   is loaded, **Then** its spans evaluate under the same canonical vocabulary as live
   traces (no fixture silently means something different from the wire).

---

### User Story 2 - A failed harness run can never pass (Priority: P1)

A test author's SUT fails to launch (bad command, missing binary), or the run's trace
never arrives. Today the underlying error is discarded (audit A2): a scenario with no
assertions passes green, and a scenario with assertions fails with fabricated reasons
(`status: want 201, got 0`) while the real cause is unrecoverable. Additionally, when
trace resolution fails, the driver's real output is thrown away, so multi-run
aggregates compute over fabricated zero values (audit A6). After this feature, a
scenario whose drive or resolution failed fails with the real underlying error, and no
aggregate ever consumes fabricated values from a failed sample.

**Why this priority**: Equal-worst class: "the SUT never ran" reported as PASS is a
false verdict, and fabricated aggregate inputs are silently corrupted statistics.

**Independent Test**: Point a target at a nonexistent command; run a scenario with no
assertions; it must fail with the driver error. Value: harness failures are always
loud.

**Acceptance Scenarios**:

1. **Given** a target whose command cannot be executed, **When** a scenario drives it
   (with or without assertions), **Then** the scenario fails and the failure text
   contains the driver's underlying error.
2. **Given** a run whose trace never becomes available, **When** the scenario drives
   it, **Then** the scenario fails and the failure text contains the resolution error
   (run id, waited duration).
3. **Given** a `@runs(N)` batch where one run's trace resolution failed but its driver
   produced real output, **When** an aggregate expression reads boundary fields
   (`r.status`, `r.answer`), **Then** the aggregate either uses that run's real output
   values or the step fails with a descriptive error — it never computes from
   fabricated zeros.

---

### User Story 3 - Incomplete trace evidence is never silent success (Priority: P2)

A test author runs a scenario whose trace ingestion is slower than the poll timeout,
or whose run spans more root traces than the store's search page size. Today the
resolver returns a partial, still-growing forest as plain success at the deadline
(audit A3), and the trace search silently truncates at the store's default result
limit (audit A4) — absence assertions (`the tool "X" is never called`) can pass
because the incriminating spans were not in the evidence. After this feature,
evidence handed to assertions is either demonstrably stable and complete, or the
scenario fails with a hard, descriptive error.

**Why this priority**: Also a false-verdict source, but it requires slower ingestion
or unusually wide runs, so it bites less often than US1/US2.

**Independent Test**: Configure a poll timeout shorter than a SUT's ingestion lag;
the scenario must fail with an instability error rather than pass on partial
evidence.

**Acceptance Scenarios**:

1. **Given** trace ingestion still in progress when the poll deadline expires,
   **When** the resolver gives up, **Then** the scenario fails with an error naming
   the run id, the observed (unstable) span count, and the timeout — it never
   evaluates assertions against the partial forest.
2. **Given** a run whose root traces exceed the store's search page size, **When**
   the run is resolved, **Then** either every matching trace is retrieved or the
   scenario fails with an error stating the result set was truncated.

---

### User Story 4 - Test inputs and reporting cannot corrupt a verdict (Priority: P3)

A test author hand-writes a trace fixture with a typo'd parent reference, or runs a
scenario whose SUT emits spans without a service name. Today the fixture loader
silently turns malformed parent references into extra roots or self-parenting spans
(audit A7), and the post-scenario report derivation can flip an all-assertions-passed
scenario to failed (audit A8). After this feature, malformed fixtures are rejected
loudly at load time, and report derivation can never change a scenario's verdict.

**Why this priority**: Real defects, but they corrupt developer workflows (fixtures,
reports) rather than production verdict paths, and no live-path false-green depends
on them.

**Independent Test**: Load a fixture with an out-of-range parent index; loading must
fail naming the span and index. Run a passing scenario whose spans lack a service
name; it must stay passed and the report must note the degraded derivation.

**Acceptance Scenarios**:

1. **Given** a fixture span whose parent index is out of range or self-referencing,
   **When** the fixture loads, **Then** loading fails with an error naming the span
   and the offending index; only the documented root marker means "root".
2. **Given** a scenario whose assertions all passed but whose spans cannot yield a
   service/tool sequence, **When** the report is derived, **Then** the scenario is
   reported passed and the report visibly notes the missing derivation detail.

---

### Edge Cases

- Spans with unset/omitted status (the healthy default on the wire) must not count as
  errors — only genuinely errored spans do.
- Wire values, fixture spellings, and any legacy spelling already in the repo must
  converge on one canonical vocabulary; a fixture must never assert something subtly
  different from what a live trace would.
- A run that is genuinely slow but completes within the timeout must still pass —
  the stability gate, not the deadline, decides success (raising the poll timeout
  remains the user's remedy for slow ingestion).
- A resolve hard-failure (US3) in a `@runs(N)` batch must become a failed sample with
  the real error retained (consistent with US2), not abort sibling runs' evidence.
- Assertion-free scenarios are legitimate (smoke runs); they pass only when the drive
  and resolution both succeeded.
- The truncation guard must not break runs with exactly the page-size number of
  traces (boundary case: N == limit with no further pages).
- Report derivation degradation must be visible in the report itself (not silently
  omitted detail), per the no-silent-fallback principle — it just must not alter the
  verdict.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Evidence MUST present span status in one canonical vocabulary,
  regardless of source (live store or fixture); the store boundary MUST translate
  wire status values, and fixture loading MUST accept and normalize the documented
  spellings. Unset/omitted status is canonical "unset", never an error. *(A1)*
- **FR-002**: All error-sensitive assertions — error-count budgets, span-status
  steps, CEL error variables, and status-based selectors — MUST detect errored spans
  in live-store evidence. *(A1)*
- **FR-003**: Evidence MUST populate span kind from both the live store and fixtures
  in a canonical, documented vocabulary, and kind-based selectors MUST evaluate it.
  *(A5)*
- **FR-004**: When a single-run drive fails (driver error or trace-resolution
  error), the scenario MUST fail and the failure text MUST contain the underlying
  wrapped error (root cause), not a fabricated comparison result. *(A2)*
- **FR-005**: A scenario containing only a drive step (no assertions) MUST fail when
  its drive or resolution failed. *(A2)*
- **FR-006**: When the resolution deadline expires before the evidence is stable,
  resolution MUST return a hard error naming the run id, observed span count, and
  timeout. Partial forests MUST never be handed to comparators as success. *(A3)*
- **FR-007**: Trace search MUST retrieve every trace matching the run tag or fail
  with an error stating the result set was truncated; silent truncation at a store
  default limit is prohibited. *(A4)*
- **FR-008**: Evidence from a run whose driver succeeded but whose resolution failed
  MUST retain the driver's real output; multi-run aggregates MUST NOT consume
  zero-values fabricated by evidence construction. Where no real value exists for a
  sample, the aggregate step MUST fail descriptively rather than compute. *(A6)*
- **FR-009**: The fixture loader MUST accept only the documented root marker as
  "root"; any other out-of-range or self-referencing parent index MUST fail loading
  with an error naming the span and index. *(A7)*
- **FR-010**: Report derivation MUST never change a scenario's verdict. When
  derivation cannot compute a detail (e.g. no service/tool sequence), the report
  MUST carry a visible note of the missing detail and the scenario's verdict MUST
  remain as decided by its assertions. *(A8)*
- **FR-011**: A hermetic unit test MUST pin the correlation query to the
  `test.run.id` tag and the run's id (guarding the tag-first invariant against
  silent rename). *(F3)*
- **FR-012**: An end-to-end test against the live harness MUST exercise the
  error-status path (SUT emits an errored span; an error assertion must go red) —
  the test class that would have caught A1. *(F5)*

### Key Entities

- **Evidence**: the trace forest plus driver output handed to assertions; gains the
  guarantee "faithful and complete, or absent with a hard error".
- **Span status / span kind**: per-span fields with a single canonical vocabulary
  across live and fixture sources.
- **Failed sample**: a run in a multi-run batch whose harness failed; carries its
  real driver output (when available) and its real error; never fabricated values.
- **Verdict**: a scenario's pass/fail decision; owned exclusively by drive success +
  assertion results, never by reporting.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every false-verdict scenario demonstrated in the audit (A1–A8) is
  reproduced as a failing test before its fix and passes after — eight red→green
  pairs, verifiable in the feature's test evidence.
- **SC-002**: A live-harness scenario asserting no-errors against a SUT that emits
  one errored span fails, and its failure text names the errored span — where today
  it passes.
- **SC-003**: A scenario whose SUT never ran, or whose trace never arrived, fails
  with the root cause present in the failure text — zero green outcomes across
  drive-failure modes, with or without assertions.
- **SC-004**: The entire pre-existing green test suite (unit + e2e meta-suite)
  still passes with unchanged verdicts on healthy traces — no regression in either
  direction.
- **SC-005**: Every touched package remains at or above the 80% coverage floor.

## Assumptions

- Canonical status vocabulary is three-valued (unset / ok / error), mirroring the
  OpenTelemetry status model; canonical kind vocabulary mirrors the OpenTelemetry
  span-kind model. Exact spellings are an implementation decision documented with
  the feature.
- Per the constitution's No-Silent-Fallbacks principle, deadline-with-unstable-spans
  (A3) is resolved as a hard error, not as flagged-but-usable evidence; users with
  slow ingestion raise the poll timeout.
- Existing repo fixtures may be updated to the canonical vocabulary as part of the
  feature (fixtures are test inputs, not a public API).
- The store's search page size / retrieval strategy is implementation-defined as
  long as FR-007's "complete or loud" guarantee holds.
- The live-harness e2e test (FR-012) is gated behind the existing e2e build tag and
  harness tooling, consistent with the hermetic-by-default principle.
- Findings B*, C*, D*, E*, G1 from the same audit are explicitly out of scope here
  (separate features); this feature only touches verdict-integrity paths.
