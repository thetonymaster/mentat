# Feature Specification: Public Extension API — Extensible Without Forking

**Feature Branch**: `007-public-extension-api`

**Created**: 2026-07-01

**Status**: Draft

**Input**: User description: "Public extension API — make Mentat extensible without forking, as the design promised. Audit finding G1: every seam interface, the evidence types, and the registries live under internal/, so the design's 'implement the interface + register a factory' pitch is unreachable and library-mode embedding is impossible. Deliver a deliberately minimal public surface — seam interfaces + their data types, registration hooks composing with the composition root, and a library-mode run entry point — plus a CI-compiled example extension, an API stability policy with an API-diff gate, and per-seam implementation docs. One-way door: everything not exported stays internal; the surface is the minimum viable set."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Implement and register a custom adapter without forking (Priority: P1)

A platform team wants Mentat to drive their SUT over a transport Mentat doesn't
ship (message queue, custom RPC). Today the seam interfaces and registries are
module-private (audit G1), so their only options are forking or upstreaming.
After this feature, they implement the published driver interface in their own
module, register it through a public hook, and their adapter name works in
config and feature files exactly like the built-ins. The same path exists for
custom trace stores, comparators, and judges.

**Why this priority**: Extensibility is the design's core pitch and the largest
promise-vs-reality gap in the audit; without it, every integration request
becomes a fork or an upstream PR.

**Independent Test**: The in-repo example extension (a toy driver in a separate
module) compiles against the public surface only, registers itself, and a
feature file drives it green — built and run in CI.

**Acceptance Scenarios**:

1. **Given** a third-party module implementing the public driver interface,
   **When** it registers under an adapter name and config references that name,
   **Then** scenarios drive it exactly like a built-in (evidence, verdicts,
   reports all work).
2. **Given** the example extension's source, **When** its imports are inspected,
   **Then** it imports only the public package(s) — zero module-private imports
   (enforced by a CI check).
3. **Given** registration of a duplicate adapter name or a registration after
   the composition root is sealed, **When** it happens, **Then** it fails
   loudly (the sealing rules from the run-lifecycle feature apply to public
   registration too).

---

### User Story 2 - Embed Mentat as a library in an existing test suite (Priority: P1)

A team runs standard Go test suites in CI and wants behaviour specs to run
inside their own test binary — no separate CLI process, results consumed
programmatically. Today only the CLI exists. After this feature, a library
entry point accepts configuration, feature paths, and optional extension
registrations, runs the suite, and returns structured results (per-scenario
verdicts, failure reasons, report data) plus an error/exit status equivalent to
the CLI's.

**Why this priority**: Embedding is the second half of the unreachable-API
problem; it also unlocks the custom-registration story for teams who want
everything in one binary.

**Independent Test**: An external-style test (in-repo, but importing only the
public surface) runs a feature file through the entry point against the file
store and asserts on the returned verdicts.

**Acceptance Scenarios**:

1. **Given** a library caller with config + feature paths, **When** the suite
   runs, **Then** the returned results contain each scenario's name, verdict,
   and failure reasons, and the overall status matches what the CLI would
   report.
2. **Given** extension registrations passed to the entry point, **When** the
   suite runs, **Then** registered adapters are usable and the registrations do
   not leak into subsequent runs (each run composes its own root).
3. **Given** a caller-provided cancellation, **When** it fires mid-suite,
   **Then** the run stops per the run-lifecycle feature's semantics and the
   results mark the interruption.

---

### User Story 3 - The public surface is governed, documented, and hard to break by accident (Priority: P2)

A maintainer edits an exported type. Today nothing distinguishes "public by
design" from "public by accident" — the module has no external consumers, so
every export is de facto private. After this feature, the public surface is an
explicit, documented manifest; CI runs an API-diff check that fails on any
unacknowledged change to it; a stability policy states pre-1.0 expectations
(breaking changes allowed but deliberate, changelogged, and never silent); and
per-seam implementation guides document contracts an implementer must honor
(error conventions, forest semantics, evidence-only boundaries).

**Why this priority**: The one-way-door risk management — without the gate and
manifest, the minimal surface erodes into an accidental one.

**Independent Test**: A PR changing a public signature without updating the
acknowledged manifest fails CI; the seam guides exist and the example extension
follows them.

**Acceptance Scenarios**:

1. **Given** an unacknowledged public-signature change, **When** CI runs,
   **Then** the API-diff gate fails naming the changed symbol.
2. **Given** an intentional change with the manifest updated and changelog
   entry, **When** CI runs, **Then** the gate passes.
3. **Given** a new implementer reading a seam guide, **When** they follow it,
   **Then** the documented contract covers error conventions, the trace-forest
   rule, and the evidence-only boundary for that seam (guides reviewed against
   the constitution's principles).

---

### Edge Cases

- The public surface must not expose the registries themselves — only
  registration functions — so sealing and thread-safety invariants stay
  enforceable internally.
- Public registration composes with registry sealing (run-lifecycle feature):
  library-mode registrations happen during composition, never after; the
  failure mode is loud.
- Two extensions registering the same name: deterministic loud conflict error
  naming both registrants (no last-wins).
- The library entry point must be safe to call multiple times in one process
  (sequential runs; concurrent runs explicitly documented as unsupported or
  safe — decided in plan, tested either way).
- Everything the public types transitively reference must itself be public and
  minimal — no internal type may leak through a public signature (compile-time
  reality, but the manifest review must catch unintended additions).
- The CLI must be a consumer of the same public composition path (one
  composition story, not two), without any CLI behaviour change.
- Documentation examples must be compiled (the example extension is the living
  doc); prose-only API examples rot.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A public package (or minimal set) MUST expose the six seam
  interfaces (driver, trace store, correlator, comparator, reporter, judge) and
  exactly the data types their contracts require (evidence, trace forest, span,
  verdict, expectation, run spec, output, and their constituent types) —
  nothing else. *(G1)*
- **FR-002**: Public registration hooks MUST exist for drivers, stores,
  comparators, and judges, composing with the single composition root and its
  sealing rules; duplicate names and post-seal registration fail loudly naming
  the conflict. *(G1)*
- **FR-003**: A library-mode entry point MUST run feature files with a given
  configuration and optional registrations, honoring caller cancellation, and
  return structured results (per-scenario verdict + reasons + report data +
  overall status) equivalent to CLI outcomes.
- **FR-004**: An example extension (toy driver in its own module) MUST build
  and run in CI against the public surface only; a CI check MUST fail if it
  imports anything module-private.
- **FR-005**: The public surface MUST be recorded in a reviewed manifest; CI
  MUST fail on any public-surface change not acknowledged in the manifest.
- **FR-006**: A written stability policy MUST state pre-1.0 semver
  expectations: breaking changes allowed, always deliberate, changelogged, and
  gate-acknowledged; the policy ships with the docs.
- **FR-007**: Per-seam implementation guides MUST document the contracts an
  implementer must honor: error conventions (no silent fallbacks), trace-forest
  semantics, evidence-only comparator boundary, correlation tag rules.
- **FR-008**: The CLI MUST be rebuilt on top of the same public composition
  path with zero behaviour change (byte-identical output on the golden green
  run).

### Key Entities

- **Public surface**: the exported API set — seam interfaces, evidence types,
  registration hooks, run entry point; everything else stays internal.
- **Surface manifest**: the reviewed, versioned record of the public surface
  that the CI gate diffs against.
- **Example extension**: the separate-module toy driver proving the surface
  suffices; CI-built.
- **Run result**: the structured per-scenario outcome returned by the library
  entry point.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The example extension compiles and its feature file runs green in
  CI with zero module-private imports (automated import check).
- **SC-002**: An external-style test drives a feature file through the library
  entry point and asserts on returned verdicts — no CLI process involved.
- **SC-003**: A deliberately-broken public signature in a test PR fails the
  API-diff gate naming the symbol; acknowledging it in the manifest makes the
  same change pass.
- **SC-004**: The CLI golden green run is byte-identical before and after the
  re-composition (FR-008).
- **SC-005**: 100% of the six seams have implementation guides; the example
  extension follows the driver guide (review checklist at PR).
- **SC-006**: Public surface size stays minimal: every exported symbol is
  either a seam interface, a type transitively required by one, a registration
  hook, or the entry point — verified by a manifest review that lists and
  justifies each symbol (no "misc" exports).

## Assumptions

- Pre-1.0 module semantics: the module stays v0; the stability promise is
  process (deliberate, gated, changelogged), not immutability. Tagging v1 is an
  explicit future decision outside this feature.
- The public package layout (single facade vs small set like core+mentat) is an
  implementation decision recorded in the plan — the requirement is the
  minimal-surface property, not a package count.
- Feature 003 (registry sealing) lands first; public registration builds on its
  sealing semantics. Feature 006's file store is the natural hermetic vehicle
  for the example extension's run and the library-mode test.
- The design docs' extension pitch (§2/§7.1) is the scope ceiling: drivers,
  stores, comparators, judges. Reporters and correlators are exposed as
  interfaces (needed by the types) but custom registration for them is out of
  scope until a real use case exists (three-examples rule).
- The API-diff mechanism is implementation-defined (standard ecosystem tooling
  or a golden-manifest test); the requirement is the failing gate, not the tool.
- Findings A*–E* are out of scope (separate features).
