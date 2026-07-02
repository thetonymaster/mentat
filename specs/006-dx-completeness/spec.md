# Feature Specification: DX & Product Completeness — Deliver What the Design Promised

**Feature Branch**: `006-dx-completeness`

**Created**: 2026-07-01

**Status**: Draft

**Input**: User description: "DX and product completeness — close the gaps between what Mentat's design promises and what users can actually do. The 2026-07-01 audit (docs/audits/2026-07-01-codebase-audit.md, cluster E, findings E1–E9): undocumented step vocabulary, no dry-run validation, JUnit-or-console (not both), no HTTP request body, fixtures write-only, invisible judge spend and wasteful judge defaults, thin mentatctl summary, hardcoded answer extraction, go-run SUT overhead and two convention-violating e2e tests."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Author a feature file without reading framework source (Priority: P1)

A new user wants to write their first behaviour spec. Today the ~30-step Gherkin
vocabulary — the entire user-facing language — exists only as regexes in framework
source (audit E1). After this feature, a complete step reference exists: every
step pattern, what it asserts, its argument grammar (span selectors, quantifiers,
ordinals), the CEL variables available in expressions, and one example each —
available as a docs page and as a CLI subcommand, both generated from the same
registration source so they cannot drift from the code.

**Why this priority**: Documentation of the language *is* the product's front
door; every other DX item assumes users can write feature files at all.

**Independent Test**: A user (or reviewer role-playing one) writes a working
feature file using only the reference; the generated reference lists every
registered step (count-verified against the registration table).

**Acceptance Scenarios**:

1. **Given** the CLI, **When** the user runs the steps subcommand, **Then** every
   registered step prints with pattern, description, and example, including the
   selector/quantifier grammar and CEL variable list.
2. **Given** a step added to the framework, **When** the reference regenerates,
   **Then** the new step appears (single source of truth; a drift test fails if a
   registered step lacks reference metadata).

---

### User Story 2 - Validate features and config instantly, without driving anything (Priority: P1)

A user edits feature files or config and wants to know they're well-formed before
paying for a live SUT run. All the prechecks exist (step binding, CEL precompile,
shape-pattern resolution, target-name checks) but only fire at drive time (audit
E2). After this feature, a validate subcommand runs every precheck against the
feature files + config and reports all findings (not just the first) in under a
second, with no SUT driven and no trace store required — usable as a CI lint.

**Why this priority**: Turns the authoring loop from minutes (live run) to
sub-second, and prevents whole classes of mid-suite failures.

**Independent Test**: A feature set seeded with an unbound step, a bad CEL
expression, an unknown target, and an unresolvable shape pattern yields all four
findings in one validate run, exit non-zero; a clean set exits zero.

**Acceptance Scenarios**:

1. **Given** feature files with the four seeded defect classes, **When** validate
   runs, **Then** all four are reported with file/line and the run exits non-zero
   without driving any SUT.
2. **Given** clean features + config, **When** validate runs, **Then** exit zero,
   silent (or summary line), under one second.

---

### User Story 3 - CI gets machine and human output in the same run (Priority: P2)

A CI pipeline needs JUnit XML for its test-report UI and console output for
humans reading the log. Today requesting JUnit silences the console entirely
(audit E3). After this feature, both are emitted in one run, alongside the
existing JSON/HTML reports.

**Acceptance Scenarios**:

1. **Given** a run with the JUnit flag, **When** it completes, **Then** the JUnit
   file is written AND the console shows the normal progress/failure output.

---

### User Story 4 - Microservice targets can send request bodies (Priority: P2)

A user tests a real microservice endpoint that expects a JSON body. Today the
HTTP driver always sends an empty body — the input channel simply has no writer
(audit E4), though the design promised a body step. After this feature, steps
exist to send an inline body or a body loaded from a fixture file, and the body
is visible in verbose narration.

**Acceptance Scenarios**:

1. **Given** a step providing an inline body (doc-string), **When** the request
   is driven, **Then** the SUT receives exactly that body.
2. **Given** a step naming a body fixture file, **When** the file exists it is
   sent verbatim; when missing, the scenario fails naming the path.

---

### User Story 5 - Saved runs replay with no infrastructure (Priority: P2)

A user saved a run's trace to disk. Today it can never be served back — only the
live store is wired (audit E5), so replay and CI smoke tests require the full
docker stack. After this feature, a file-backed store is selectable in config,
serving saved fixtures; replay of a saved run and a docker-free smoke suite both
work offline, with absent fixtures failing loudly.

**Acceptance Scenarios**:

1. **Given** a saved run and file-store config, **When** the run is replayed,
   **Then** verdicts render with no network and no docker.
2. **Given** a fixture id that doesn't exist on disk, **When** resolution runs,
   **Then** it fails naming the path/id (complete-or-loud, as live).

---

### User Story 6 - Judge spend is visible, bounded, and sanely defaulted (Priority: P1)

A team uses semantic judge assertions. Today every judge call spends real money
invisibly: no report shows judge tokens/cost, no budget exists, the default model
is the most expensive tier for a binary verdict, and best-of-N voting at
temperature zero pays N times for near-identical answers (audit E6). After this
feature: per-scenario judge token/cost appears in JSON and HTML reports and in
the suite summary; an optional suite-level judge budget aborts the suite when
exceeded (hard stop, descriptive error); the default model is a fast, cheap tier
with the accuracy-upgrade knob documented; and vote diversity is honest — votes>1
either pairs with non-zero temperature by default or requires explicit
configuration acknowledging the cost.

**Why this priority**: Invisible spend is the kind of surprise that gets a tool
banned from CI; the fix is bookkeeping plus defaults.

**Acceptance Scenarios**:

1. **Given** a suite with semantic steps, **When** reports render, **Then** each
   affected scenario shows judge calls, tokens, and cost, and the suite summary
   totals them.
2. **Given** a configured judge budget, **When** cumulative judge cost exceeds
   it, **Then** the suite stops with an error naming the budget and the spend.
3. **Given** default config, **When** a semantic step runs, **Then** the
   fast-tier default model is used, and votes>1 with zero temperature triggers a
   loud configuration error or documented auto-diversification.

---

### User Story 7 - The interactive agent runner matches its design (Priority: P3)

An operator uses the service CLI to poke an agent. Today the run summary omits
tokens, cost, latency, and trace id (all promised, all already derivable), and
input must be inline text only (audit E7). After this feature the summary
includes them, prompts can come from a file or stdin, output can be written to a
file, and per-invocation timeout flags exist.

**Acceptance Scenarios**:

1. **Given** an agent run via the service CLI, **When** it completes, **Then**
   the summary shows answer, tools, span count, tokens, cost, latency, and trace
   id(s).
2. **Given** `--prompt-file`/stdin input and `-o`, **When** used, **Then**
   prompt is read from the source and the answer written to the file.

---

### User Story 8 - Result assertions survive chatty agents (Priority: P2)

An agent logs progress to stdout around its final answer. Today the "answer" is
the whole trimmed stdout, so result assertions see log noise (audit E8; the
design promised configurable extraction). After this feature, answer extraction
is configurable per target: whole-output (default, unchanged), a marker
convention (text after the last occurrence of a configured marker), or a
regex-capture pattern; extraction failures are descriptive.

**Acceptance Scenarios**:

1. **Given** no extraction config, **When** a run completes, **Then** behaviour
   is exactly today's (whole trimmed output).
2. **Given** a marker config and an output containing the marker, **Then** the
   answer is the text after its last occurrence; **Given** the marker is absent,
   **Then** the scenario fails naming the marker (no silent whole-output
   fallback).

---

### User Story 9 - The lab and e2e suites practice what the repo preaches (Priority: P3)

Every scenario drive currently pays toolchain compile/startup overhead because
lab SUTs run via the toolchain runner, and two e2e tests violate the repo's own
prebuilt-binary + parallel conventions (audit E9). After this feature, lab SUT
binaries are prebuilt by the harness and referenced by path, and the two tests
follow the conventions.

**Acceptance Scenarios**:

1. **Given** the harness setup, **When** scenarios drive lab SUTs, **Then** they
   execute prebuilt binaries (no per-scenario toolchain invocation).
2. **Given** the two report-meta e2e tests, **When** the suite runs, **Then**
   they use the prebuilt framework binary and run in parallel like their peers.

---

### Edge Cases

- Step reference: steps registered by extensions/future features must fail the
  drift test if undocumented — the reference is generated, never hand-listed.
- Validate must not require a live store or judge credentials; judge-dependent
  steps validate structurally (config shape), not by calling the judge.
- Validate on a directory with zero feature files: non-zero with "nothing to
  validate" (not silent success).
- Dual output: JUnit file write failure must still fail the run (existing
  reporter-error contract) even though console succeeded.
- Body fixture paths resolve relative to the feature file's directory
  (documented); absolute paths allowed.
- File store + `@runs(N)`: replays are deterministic — multi-run over a single
  fixture either errors or is documented as N identical samples (decided in
  plan; no silent surprise).
- Judge budget counts spend from completed calls; in-flight calls at the
  threshold complete but no new calls start (documented semantics).
- Marker extraction with multiple markers in output: last occurrence wins
  (documented); regex extraction with no match fails naming the pattern.
- Prebuilt lab binaries must rebuild when sources change (build-system
  dependency, not manual).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A step reference MUST be generated from the step-registration
  source of truth, covering every registered step (pattern, purpose, argument
  grammar, example), the selector/quantifier/ordinal grammar, and CEL variables;
  exposed as both a docs artifact and a CLI subcommand; a drift test MUST fail
  when a registered step lacks reference metadata. *(E1)*
- **FR-002**: A validate subcommand MUST run all existing authoring prechecks
  (step binding, CEL compile, shape-pattern resolution, expectations load,
  target-name and config checks) across given feature files + config, report
  every finding with file/line, exit non-zero on any finding, and complete
  without driving SUTs or contacting stores/judges. *(E2)*
- **FR-003**: JUnit output MUST coexist with console output in a single run; the
  reporter-failure exit-code contract applies to both. *(E3)*
- **FR-004**: Steps MUST exist to send an HTTP request body inline (doc-string)
  or from a fixture file; missing fixture files fail the scenario naming the
  path. *(E4)*
- **FR-005**: A file-backed trace store MUST be selectable in config, serving
  saved fixtures with the same complete-or-loud error contract as the live
  store; saved runs MUST replay offline end-to-end. *(E5)*
- **FR-006**: Reports (JSON, HTML, suite summary) MUST include per-scenario and
  suite-total judge call count, tokens, and cost. *(E6)*
- **FR-007**: An optional suite-level judge budget MUST hard-stop the suite with
  a descriptive error when exceeded; semantics (completed-call accounting)
  documented. *(E6)*
- **FR-008**: The default judge model MUST be a fast/cheap tier appropriate for
  binary verdicts, with the accuracy-upgrade knob documented; votes>1 combined
  with zero temperature MUST NOT silently send identical calls (loud config
  error or documented auto-diversification). *(E6)*
- **FR-009**: The service CLI's run summary MUST include tokens, cost, latency,
  and trace id(s); prompt input from file/stdin, output-to-file, and
  per-invocation timeout flags MUST exist. *(E7)*
- **FR-010**: Answer extraction MUST be configurable per target: whole-output
  (default), marker, or pattern capture; extraction failure is a descriptive
  scenario failure, never a silent fallback to whole output. *(E8)*
- **FR-011**: Lab SUT binaries MUST be prebuilt by the harness/build system and
  referenced by path in configs; the two convention-violating e2e tests MUST be
  brought to the prebuilt-binary + parallel conventions. *(E9)*

### Key Entities

- **Step reference**: the generated vocabulary document/command output; single
  source: the registration table.
- **Validation finding**: file/line + defect class + message; the unit of
  validate output.
- **Judge ledger**: per-scenario and suite-total judge call/token/cost record
  carried into reports and checked against the optional budget.
- **Answer extractor**: per-target extraction policy (whole | marker | pattern).
- **File store**: the fixture-serving store implementation selectable in config.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user authors a working feature file using only the step
  reference — verified once by review walkthrough; the drift test enforces 100%
  step coverage continuously.
- **SC-002**: Validate catches 100% of the four seeded defect classes in one
  run, in under 1 second, with zero SUT/store/judge contact (asserted
  hermetically).
- **SC-003**: One CI run produces both JUnit XML and console output (file
  exists + console non-empty).
- **SC-004**: A saved run replays to identical verdicts with the network
  disabled and docker stopped.
- **SC-005**: Judge tokens/cost appear for every semantic-step scenario in JSON
  and HTML reports; a 1-cent budget on a multi-call suite stops it with the
  budget error.
- **SC-006**: With defaults, per-semantic-step judge cost drops by ≥80% versus
  the current default model's pricing (fast-tier vs top-tier price sheet), with
  the L3 semantic meta-tests still passing.
- **SC-007**: Lab-SUT scenario drives shed the per-run toolchain overhead
  (measured: scenario setup time drops by the previously-measured 100–300ms per
  drive on warm cache); the two e2e tests run in parallel with the suite.
- **SC-008**: Zero verdict changes on existing green suites (except where a
  previously-silent misconfiguration now fails loudly by design).

## Assumptions

- The step reference ships as a generated markdown doc in the repo plus a CLI
  subcommand reading the same metadata; regeneration is wired into the build/CI
  so drift fails fast.
- Validate reuses the existing precheck implementations (they already exist at
  scenario-init); the subcommand is orchestration, not new validation logic.
- Dual JUnit+console uses the BDD runner's native multi-formatter support.
- The file store serves the existing saved-fixture format; it is a test/replay
  vehicle, not a new archival format (OTLP-file store remains future work).
- Judge cost derives from the judge's own usage reporting and the existing
  pricing table mechanism; unknown-model pricing follows the existing
  ambiguous-model hard-error rules.
- Fast-tier default model choice follows the same capability-allowlist
  hardening the judge already has; exact model id is an implementation decision
  recorded in the plan.
- `@runs(N)` over a single fixture in file-store mode: decided at plan time
  (error vs N identical samples), with a test either way.
- Features 002–005 land first or independently; only 002's canonical fixture
  vocabulary interacts (file store must speak it) — coordination noted in plan.
- Findings A*–D*, G1 are out of scope (separate features).
