# Feature Specification: Extension-Surface Integrity

**Feature Branch**: `009-extension-surface-integrity`

**Created**: 2026-07-18

**Status**: Draft

**Input**: User description: "Make every guarantee of the public extension surface machine-enforced before any new seam is added. Today the surface gate, the facade type set, and the library Run path each have a verified hole that lets public-API drift or YAML-vs-code divergence through silently. Close all five." (full prompt: `prompts/spec-009-extension-surface-integrity.md`, evidence gathered 2026-07-18 at commit `b1aabb4`)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Struct-field drift cannot pass the surface gate (Priority: P1)

A maintainer adds, removes, or re-types an exported field on any struct type that is part of the public extension surface. The automated public-surface gate must go red until the change is made deliberate by an explicit golden regeneration, so every field-level API change is visible in review.

**Why this priority**: This hole has already produced realized, silent drift — two public struct fields (`core.Verdict.Qualifiers`, `config.Target.Completeness`) were added in feature 008 with zero golden churn while the stability documentation claimed struct fields were frozen. A gate that does not enforce its documented promise is worse than no gate: it manufactures false confidence. Everything else in this feature builds on a gate that tells the truth.

**Independent Test**: Mutation rehearsal — temporarily add an exported field to a frozen public struct, observe the surface test FAIL naming the drift; revert, observe PASS. Deliverable is complete without any other story.

**Acceptance Scenarios**:

1. **Given** the current committed surface golden, **When** an exported field is temporarily added to an aliased public struct (e.g. `core.Verdict`), **Then** the public-surface test FAILS and the failure output identifies the changed type.
2. **Given** that temporary field is reverted, **When** the surface test runs again, **Then** it PASSES unchanged.
3. **Given** a legitimate field addition, **When** the golden is regenerated via the explicit opt-in mechanism, **Then** the golden diff shows the new field as a reviewable line item.
4. **Given** the strengthened gate, **When** a reader consults the stability documentation, **Then** its struct-field freezing claim matches what the gate actually enforces (the strong claim weakened by the Bucket-1 chore PR is restored).

---

### User Story 2 - Code-built configuration behaves identically to file-loaded configuration (Priority: P2)

A library consumer constructs the run configuration in code (struct literal) and invokes the library entry point. The effective behaviour must be identical to the same logical configuration loaded from a YAML file — or the run must fail immediately with a descriptive error naming the field that cannot be resolved on the code path. Never a silent difference.

**Why this priority**: Verified silent divergence exists today: completeness kind-defaults (settle windows) are applied only on the file-load path, so the same logical configuration yields different contracts depending on how it was built — a direct violation of the project's no-silent-fallbacks principle on the flagship library entry point shipped in 007.

**Independent Test**: A table-driven parity test feeds the same logical configuration through both paths (file load vs code construction) and asserts either identical effective contracts or a loud, named error — testable without any other story.

**Acceptance Scenarios**:

1. **Given** the same logical configuration expressed as a YAML file and as a code-built struct, **When** each is resolved through its path, **Then** the effective contract (including defaulted values such as completeness settle windows) is identical.
2. **Given** a code-built configuration that uses a capability only the file path can resolve (if the chosen story makes such capabilities path-exclusive), **When** the run is invoked, **Then** it fails before driving the system-under-test with an error naming the unresolved field and the required action.
3. **Given** the audit list of Load-only behaviours (completeness defaults, compiled extraction config, target budget), **When** the feature is complete, **Then** each is either resolved identically on both paths or produces the loud error — none silently diverges.

---

### User Story 3 - Every reachable public type is nameable from the facade (Priority: P3)

An extension author working in an external module (where internal imports are compiler-forbidden) can write a composite literal for every exported struct type reachable from the public configuration and results types, using only the public facade package.

**Why this priority**: Verified instance: `mentat.Target.Completeness` has a type the facade does not alias, so external code can read it but cannot construct it — a reachable-but-unnameable dead end. Lower than P2 because the failure is a visible compile error rather than silent wrongness, but it still blocks the extension story 007 promised.

**Independent Test**: A facade-only compile test in an external-module context constructs a composite literal for every exported struct type reachable from the public entry types; the test compiling is the proof.

**Acceptance Scenarios**:

1. **Given** the swept facade, **When** an external module writes composite literals for every exported struct type reachable from the public config/results types importing only the facade package, **Then** the code compiles.
2. **Given** the known gap, **When** the facade is swept, **Then** the completeness configuration type is aliased and any other reachable-unnameable types found by the sweep are aliased in the same pass.

---

### User Story 4 - A contributor can add a new seam without tribal knowledge (Priority: P4)

A contributor adding a new extension seam finds a written checklist covering every touchpoint the change requires, with the three undocumented design decisions explained, and a single reconciled seam taxonomy referenced from both places that currently disagree.

**Why this priority**: Documentation debt, not a live defect — but it is the stated precondition for the next features (010/011), and today the "six seams" are defined differently in two places while one comparator's internal-only status is documented nowhere.

**Independent Test**: The guide exists, enumerates the touchpoints, explains the three decisions (instance-vs-factory registration, per-engine vs package-global, collision-check ordering); both prior taxonomy locations reference the single reconciled list; the internal-only comparator exclusion is stated in one explicit sentence.

**Acceptance Scenarios**:

1. **Given** a contributor planning a new seam, **When** they follow the guide, **Then** every place the change must touch is enumerated with the decision guidance needed to choose correctly.
2. **Given** the two current "six seams" lists, **When** the feature is complete, **Then** both locations agree via one reconciled taxonomy and the aggregate comparator's exclusion from the public seam set is documented explicitly.

---

### User Story 5 - The L3 stability promise is enforced nightly (Priority: P5)

The project's stability requirement that the L3 meta-suite passes 20 consecutive runs is enforced by a scheduled pipeline, not by a one-time manual claim, so regressions in flake-resistance surface within a day instead of never.

**Why this priority**: Closes an honesty gap in CI (008's SC-001 is currently backed by a single manual run recorded in a PR description), but the per-PR 3-run lane already provides baseline protection, so this is last.

**Independent Test**: The scheduled workflow exists, triggers on schedule and manual dispatch, runs the e2e L3 suite at 20 runs, and at least one dispatched run has gone green.

**Acceptance Scenarios**:

1. **Given** the new scheduled workflow, **When** it is dispatched manually, **Then** it boots the harness, runs the L3 suite with the 20-run setting, and completes green.
2. **Given** the schedule, **When** the nightly trigger fires, **Then** the same lane runs without manual intervention.

---

### Edge Cases

- Aliased public structs with embedded fields or unexported fields: the gate must render exported field sets accurately (embedded exported types included by promotion rules) without leaking unexported details into the frozen surface.
- Aliases of non-struct, non-interface types (maps, slices, named basic types): the gate must handle them without crashing and freeze whatever is externally observable about them.
- A struct field whose own type is internal and un-aliased: the nameability sweep must flag it — aliasing the outer struct alone is insufficient.
- Distinguishing "field deliberately zero" from "field unset" on the code-built path (YAML absence vs Go zero value) when deciding whether a default applies — the parity rule must be explicit about this.
- Golden regeneration performed accidentally alongside unrelated work: every golden diff in this feature is itself review surface and must be called out in the PR body.
- The nightly lane failing silently: the workflow must surface failure through the repository's normal CI visibility (failed run on the default branch's Actions page), not require someone to remember to look.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The public-surface gate MUST fail when an exported field is added to, removed from, or re-typed on any struct type exposed through the public facade, including struct types exposed via aliases.
- **FR-002**: The gate's existing guarantees (interface method-set expansion, function signatures, top-level declarations) MUST NOT regress while adding struct-field freezing.
- **FR-003**: Golden regeneration MUST remain an explicit opt-in action; no test run may rewrite the golden by default.
- **FR-004**: The strengthened gate MUST be proven by an in-repo documented mutation rehearsal (add exported field → gate red; revert → green), following the precedent already set in the surface test.
- **FR-005**: The stability documentation MUST state exactly the guarantees the gate enforces, restoring the struct-field freezing claim once it is true.
- **FR-006**: Every exported struct type reachable from the public configuration and results types MUST be nameable (constructible via composite literal) from the facade package alone by an external module.
- **FR-007**: Nameability MUST be enforced by a compile-level test that uses only the facade package, so any future reachable-unnameable type breaks the build.
- **FR-008**: A configuration built in code and passed to the library entry point MUST yield the same effective contract as the identical logical configuration loaded from file, OR fail with a hard, descriptive error naming the unresolved field — silent divergence between the two paths is prohibited. One of these two stories MUST be chosen and applied consistently to all fields (decision recorded during planning).
- **FR-009**: Parity MUST be proven by a table-driven test that runs the same logical configurations through both paths and asserts identical effective contracts or the specified loud error.
- **FR-010**: All currently known Load-only behaviours (completeness kind-defaults, compiled extraction configuration, target budget) MUST each be audited and covered by FR-008's chosen story.
- **FR-011**: A new-seam guide MUST exist documenting the full touchpoint checklist and the three currently tribal decisions: instance-vs-factory registration, per-engine vs package-global registration, and collision-check-before-construction ordering.
- **FR-012**: The two existing divergent seam taxonomies MUST be reconciled into one list referenced from both locations, and the aggregate comparator's internal-only status MUST be documented in an explicit sentence.
- **FR-013**: A scheduled CI workflow MUST run the e2e L3 suite at the 20-run stability setting on a nightly schedule and support manual dispatch.

### Key Entities

- **Public surface golden**: The committed, reviewable rendering of everything the project promises externally — top-level declarations, interface method sets, and (new) exported field sets of public struct types. Drift against it is a test failure.
- **Facade alias set**: The set of type aliases the public package exposes; completeness requires it to cover every type reachable from the public entry types.
- **Effective contract**: The fully resolved configuration a run actually executes with (after defaults and validation), which must be path-independent (file vs code) or fail loudly.
- **Seam taxonomy**: The single authoritative list of public extension seams, including explicit exclusions.
- **L3 stability lane**: The scheduled pipeline enforcing the 20-consecutive-run meta-test requirement.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Mutation rehearsal on a frozen public struct flips the surface gate red and back green (documented in the test file), and the two fields that previously drifted silently (`Verdict.Qualifiers`, `Target.Completeness`) now appear in the golden as frozen lines.
- **SC-002**: 100% of exported struct types reachable from the public entry types can be constructed from the facade alone; the compile-level proof fails the build on any future gap.
- **SC-003**: For every configuration in the parity table, file-loaded and code-built runs produce identical effective contracts or the specified named error; zero silent-divergence cases remain from the audited list.
- **SC-004**: A contributor can enumerate every touchpoint of a hypothetical new seam using only the guide (no source spelunking), and the two prior taxonomy locations no longer disagree.
- **SC-005**: The nightly workflow exists, has both schedule and manual triggers, and at least one dispatched run has completed green at the 20-run setting.
- **SC-006**: All five closures land without adding any new seam and without weakening any existing gate (surface golden churn in this feature is limited to deliberate, PR-called-out diffs).

## Assumptions

- File/line evidence in the input prompt was verified on 2026-07-18 at commit `b1aabb4`; code may have moved since — planning re-verifies every reference before relying on it.
- The choice in FR-008 between (a) engine-applied Load-equivalent resolution and (b) exported resolution function + hard error is explicitly delegated to planning by the feature prompt; the spec requires only that one story is chosen and silent divergence is eliminated.
- Items 1–3 are behaviour changes routed through TDD (go-test-writer); items 4–5 are docs/CI work (go-coder), per the repo constitution's routing rules.
- The per-package 80% coverage floor and all existing CI gates stay in force throughout.
- Out of scope (deferred to later specs): custom-comparator Gherkin invocation (010), CLI/mentatctl UX (011), and adding any new seam — this feature is the precondition for those.
