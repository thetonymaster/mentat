# Feature Specification: Observability & Config Integrity — Mentat Explains Itself and Rejects Bad Config

**Feature Branch**: `005-observability-config`

**Created**: 2026-07-01

**Status**: Draft

**Input**: User description: "Observability and config integrity — Mentat must explain itself and reject bad config. The 2026-07-01 audit (docs/audits/2026-07-01-codebase-audit.md, cluster D, findings D1–D6): zero logging anywhere, silent 30s correlation waits with context-free timeout errors, non-strict config parsing where typos silently change verdict semantics, adapter validation that accepts adapters no driver implements, env injection that clobbers ambient OTel settings with empty values, dropped ordinal parse errors, and correlator wiring copy-pasted across both binaries."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Correlation failures are diagnosable from the output alone (Priority: P1)

A first-time user wires their SUT wrong — collector down, wrong endpoint, agent not
exporting. Today they get 30 seconds of total silence, then an error that says only
that no trace arrived for a run id (audit D1). They cannot tell whether the SUT ran,
where Mentat looked, or what was injected. After this feature: with verbosity on,
Mentat narrates the run lifecycle (driver invocation, run id, injected environment,
store endpoint, resolution query, per-poll progress); and even with verbosity off,
the trace-not-found error itself names the store endpoint, the exact query, the
wait duration, and a short diagnostic checklist of the common causes.

**Why this priority**: This is the single highest-friction moment of the product —
first-run failure with zero signal. Every new user and every broken CI hits it.

**Independent Test**: Run a scenario against a dead collector; the failure message
alone (no source reading) is sufficient to identify where Mentat queried and what
to check; rerunning with `-v` shows the lifecycle narration.

**Acceptance Scenarios**:

1. **Given** an SUT that exports nowhere, **When** resolution times out, **Then**
   the error names the store endpoint, the query (tag and run id), the elapsed
   wait, and a checklist of likely causes (collector down / wrong endpoint / SUT
   not instrumented).
2. **Given** `-v`/`--verbose`, **When** any scenario runs, **Then** the log
   narrates the drive/resolve lifecycle: target and command driven, run id, store
   endpoint, and the final resolution outcome. **And given** `-vv`,
   **Then** the log additionally narrates the injected SUT env (names+values,
   Mentat-set keys only) and each poll round's detail (round, spans seen, stable
   streak).
   (Reconciled 2026-07-18: this scenario originally placed the injected-env values
   and per-poll span counts under `-v`. The authoritative contract
   [`contracts/narration-and-errors.md`](contracts/narration-and-errors.md) and the
   shipped code both gate those two under `-vv`; contract and code win, and the
   text above now matches them. See the resolved note in `tasks.md`.)
3. **Given** default verbosity, **When** a healthy suite runs, **Then** output is
   exactly as today (no new noise on the happy path).

---

### User Story 2 - Bad configuration fails at load, precisely (Priority: P1)

A user typos a config key (`poll.timout`, `judge.vote`) or configures an adapter
that isn't implemented. Today the typo silently falls back to defaults — changing
verdict semantics like vote count with no warning (audit D2) — and the phantom
adapter passes validation only to kill the suite mid-run (audit D3). After this
feature, unknown config keys are load-time errors naming the offending key, and
adapter validation happens against the set of drivers that actually exist, at
startup, naming the valid options.

**Why this priority**: Silent verdict-semantics drift from a typo is a
config-shaped false-verdict source — same severity class as cluster A, and
trivially preventable.

**Independent Test**: A config with a misspelled key fails to load naming the key;
a config with `adapter: grpc` fails at startup listing registered adapters.

**Acceptance Scenarios**:

1. **Given** a config containing an unknown key at any nesting level, **When** it
   loads, **Then** loading fails naming the unknown key (and its path), matching
   the strictness the expectations sidecar already has.
2. **Given** a target whose adapter has no registered driver, **When** the engine
   builds, **Then** startup fails naming the adapter and the registered
   alternatives — before any scenario runs.
3. **Given** a valid config, **When** it loads, **Then** behaviour is unchanged.

---

### User Story 3 - Mentat never sabotages the SUT's own telemetry config (Priority: P2)

A developer's SUT already has a working OTel setup via ambient environment
variables. Today, running it under Mentat with no `otlpEndpoint` configured
injects an *empty* endpoint value that overrides the working one — the SUT
exports nowhere and the run dies as an opaque timeout (audit D4); the injected
resource attributes likewise replace any ambient ones. After this feature, Mentat
only injects an endpoint when it has one to inject, merges its resource
attributes with ambient values instead of replacing them, and (with US1) the
narration shows exactly what was injected.

**Why this priority**: It converts a working developer setup into the US1 failure
mode — nasty because the user's config was *correct*.

**Independent Test**: With ambient endpoint set and no config endpoint, the SUT
subprocess receives the ambient value unchanged; with both set, config wins and
the narration says so.

**Acceptance Scenarios**:

1. **Given** no configured endpoint and an ambient one, **When** a run is driven,
   **Then** the SUT's environment keeps the ambient endpoint (nothing empty is
   injected).
2. **Given** ambient resource attributes on the SUT environment, **When** Mentat
   injects its correlation attributes, **Then** the result contains both (merge),
   with Mentat's keys winning only on key collision.
3. **Given** both configured and ambient endpoints, **When** a run is driven,
   **Then** the configured value wins (explicit config beats ambient).

---

### User Story 4 - Small honesty fixes: ordinals and single-source correlator defaults (Priority: P3)

A feature author writes an absurd span ordinal; today the parse error is dropped
and a misleading downstream error appears (audit D5). Separately, the correlator
is the only seam wired by copy-paste in both binaries, so poll defaults can drift
between `mentat` and `mentatctl` (audit D6). After this feature, unparseable
ordinals fail at step-parse time naming the value, and correlator construction +
defaults live in one place used by both binaries.

**Why this priority**: Low blast radius, but both are cheap and close audited
gaps.

**Independent Test**: A step with an overflow ordinal fails naming the ordinal;
grep-level check shows one correlator construction site consumed by both
binaries with identical defaults.

**Acceptance Scenarios**:

1. **Given** a step referencing an ordinal that cannot parse, **When** the feature
   parses, **Then** the error names the ordinal text and the step.
2. **Given** both binaries, **When** each builds its engine, **Then** the
   correlator and its poll defaults come from the same shared construction path
   (drift is structurally impossible).

---

### Edge Cases

- Verbose narration must redact nothing it doesn't have to but must not dump
  secrets: injected env values are shown by name; values echoed only for the
  OTel/correlation keys Mentat itself sets (never arbitrary inherited env).
- Log output goes to stderr so it never corrupts stdout consumers (JUnit path,
  scripts parsing run output).
- Strict config must still accept documented-but-unset optional keys (absence ≠
  unknown key).
- Strictness applies to the main config only in this feature; any other YAML
  surfaces adopt it opportunistically (expectations sidecar is already strict).
- Adapter validation must use the post-build registry state (single source of
  truth), not a hardcoded list that can drift again — the drift *was* the bug.
- Merge semantics for resource attributes must preserve correlation integrity:
  Mentat's `test.run.id` always wins collisions (correlation is the product).
- Verbosity flag must exist on both binaries and default off in CI unless set.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Both CLIs MUST accept a verbosity flag enabling structured, leveled
  logging to stderr; default remains silent on the happy path. *(D1)*
- **FR-002**: With verbosity on, the run lifecycle MUST be narrated: target +
  command, run id, injected environment (names always; values only for
  Mentat-set keys), store endpoint, resolution query, per-poll observation
  progress, and final outcome. *(D1)*
- **FR-003**: The trace-not-found/timeout error MUST name the store endpoint, the
  query (tag + run id), the elapsed wait, and include a short diagnostic
  checklist — independent of verbosity. *(D1)*
- **FR-004**: Main config parsing MUST reject unknown keys at any nesting level,
  naming the key and its path. *(D2)*
- **FR-005**: Adapter validity MUST be checked at engine build time against the
  actually-registered drivers; failure names the bad adapter and the registered
  set. The load-time allowlist MUST NOT be a second, driftable source of truth.
  *(D3)*
- **FR-006**: The engine MUST NOT inject empty-valued telemetry endpoint
  variables; configured values win over ambient; injected resource attributes
  MUST merge with ambient ones, Mentat's correlation keys winning collisions.
  *(D4)*
- **FR-007**: Span-ordinal parsing MUST surface parse failures naming the ordinal
  text and step. *(D5)*
- **FR-008**: Correlator construction and poll defaults MUST have a single
  construction path shared by both binaries (same pattern as the other seams).
  *(D6)*
- **FR-009**: All new log lines and error texts MUST be covered by tests pinning
  the load-bearing substrings (endpoint, query, key names), keeping them stable
  for users who script against them.

### Key Entities

- **Run narration**: the leveled log stream describing one run's lifecycle.
- **Diagnostic checklist**: the fixed cause-list embedded in correlation timeout
  errors.
- **Strict config**: the main configuration whose unknown keys are load errors.
- **Injection policy**: the rules for what telemetry env Mentat sets, merges, or
  leaves untouched.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a usability check against a dead collector, the failure message
  alone identifies where Mentat queried and the top causes — no source reading;
  the same run under `-v` shows every lifecycle stage (verified by pinned-log
  tests plus one scripted e2e).
- **SC-002**: 100% of misspelled top-level and nested config keys used in the
  test corpus fail loading with the key named (table test across every config
  section).
- **SC-003**: A phantom-adapter config fails before any scenario executes, and
  the error lists the registered adapters (startup-time test).
- **SC-004**: With ambient telemetry env and empty Mentat config, the SUT
  receives its ambient values unchanged (env-inspection test); `test.run.id`
  injection still correlates every run (no correlation regression in e2e).
- **SC-005**: Healthy-suite output at default verbosity is byte-identical to
  pre-feature output (golden test on a green run's stdout).
- **SC-006**: Zero verdict changes across the full suite.

## Assumptions

- Structured logging uses the language ecosystem's standard leveled logger;
  levels: silent (default), verbose (`-v`), debug (`-vv`, poll-round detail).
  Exact flag spelling is implementation-defined; existence and default-off are
  the requirements.
- The diagnostic checklist is static text (collector down / wrong endpoint / SUT
  not instrumented / attributes not applied) — curated, not computed.
- Strict parsing may require replacing the YAML decode call; the config file
  format itself does not change (only unknown keys become errors — an
  intentional breaking change for configs that were already silently wrong).
- `mcp`/`grpc` stay out of the load-time allowlist until real drivers register
  them (the registry becomes the only truth; the hardcoded concurrency-default
  list is reduced to registered adapters).
- Feature 002's resolve error contract is extended, not replaced, by FR-003's
  enrichment (message gains fields; error class unchanged). Ordering with 002/004
  is flexible; merge conflicts in correlate messages are expected and trivial.
- Findings A*, B*, C*, E*, G1 are out of scope (separate features).
