<!--
SYNC IMPACT REPORT
==================
Version change: (template, unversioned) → 1.0.0
Rationale: Initial ratification. The constitution template was unfilled; this is the
first concrete adoption, so the version starts at 1.0.0 (MAJOR for first ratified set).

Principles (all newly defined from CLAUDE.md architecture invariants + testing rules):
  - I.   Evidence-Only Comparators (Portability Boundary)
  - II.  Trace Is a Forest, Correlation Is Tag-First
  - III. Seams Are Interfaces, Wired Once (Manual DI)
  - IV.  No Silent Fallbacks (Crashes Are Data)
  - V.   Test-First & Hermetic by Default (NON-NEGOTIABLE)

Added sections:
  - Engineering Standards & Tooling (was [SECTION_2_NAME])
  - Development Workflow & Quality Gates (was [SECTION_3_NAME])
  - Governance (filled)

Removed sections: none.

Templates / artifacts reviewed:
  ✅ .specify/templates/plan-template.md — "Constitution Check" gate dynamically references
       this file; no hardcoded change needed, remains aligned.
  ✅ .specify/templates/spec-template.md — no mandatory section added/removed by this
       constitution; no change needed.
  ✅ .specify/templates/tasks-template.md — UPDATED: "Tests are OPTIONAL" note replaced to
       reflect Principle V (Test-First is NON-NEGOTIABLE).
  ✅ .specify/templates/checklist-template.md — generic; no principle-driven change needed.
  ✅ README.md / CLAUDE.md — already describe these invariants; constitution references
       CLAUDE.md as the runtime contributor guide; no change needed.

Deferred / follow-up TODOs: none. Ratification date set to first-adoption date (today).
-->

# Mentat Constitution

## Core Principles

### I. Evidence-Only Comparators (Portability Boundary)

Comparators MUST consume `Evidence` only — the `Trace` forest plus the driver `Output`.
They MUST NOT touch a `TraceStore`, a `Driver`, or any transport. This boundary is what
keeps assertions portable across both AI/LLM agent SUTs and microservice SUTs without
modification.

**Rationale:** The moment a comparator reaches past `Evidence` for data, it couples the
assertion to a specific store or driver and loses portability — the single property that
lets the same behaviour spec grade an agent and a service alike.

### II. Trace Is a Forest, Correlation Is Tag-First

A `Trace` is a forest: a single run MAY span ≥1 root trace (multi-turn, sub-agent,
fan-out). Code MUST NOT assume a single root. Correlation MUST resolve tag-first — inject
`test.run.id` per run (resource attribute via `OTEL_RESOURCE_ATTRIBUTES` for spawned
agents; baggage for HTTP/gRPC) and resolve by querying that tag, then merge the matched
traces.

**Rationale:** Real agent and service behaviour produces disjoint root spans; assuming one
root silently drops evidence. Tag-first correlation is the only reliable join key across
process and turn boundaries.

### III. Seams Are Interfaces, Wired Once (Manual DI)

Every seam (`Driver`, `TraceStore`, `Correlator`, `Comparator`, `Reporter`, `Judge`) MUST
be an interface, kept small and defined by its consumer. The engine MUST depend on
interfaces, never on concrete `Tempo`/`shell`/`Claude` types. All wiring MUST happen at the
single composition root (`engine.Build`) through per-seam registries. Dependency-injection
frameworks (`wire`, `fx`) are PROHIBITED — wiring is manual and explicit.

**Rationale:** A test framework's value is in swapping SUTs and stores freely. One explicit
composition root keeps the dependency graph readable and the seams genuinely substitutable;
DI magic hides exactly the wiring a trace framework most needs to reason about.

### IV. No Silent Fallbacks (Crashes Are Data)

A function that cannot do its job MUST return an `error`, wrapped with `%w` and naming the
concrete thing that failed and the value involved (e.g. `"port: expected int, got %q"`).
It MUST NOT return a zero-value success, a guessed result, or an `or {}` fallback.
Trace-not-found, ambiguous match, and missing required attributes are hard, descriptive
errors. `panic` is forbidden in library code except for true, caller-unreachable invariants.

**Rationale:** Silent fallbacks convert a hard failure into silent corruption of test
verdicts — the worst outcome for a framework whose entire job is to be trusted when it says
PASS or FAIL. A crash is a signal; a swallowed error is a lie.

### V. Test-First & Hermetic by Default (NON-NEGOTIABLE)

All features and bugfixes MUST follow TDD: red → green → refactor, one failing test at a
time. Tests are table-driven by default; interface mocks use uber gomock (`go.uber.org/mock`).
Every package MUST hold ≥80% coverage — a PR dropping a package below the floor is blocked.
Unit tests MUST be hermetic (in-memory / `otlp-file` store + gomock, no network); live-Tempo
tests MUST be gated behind `//go:build e2e`. The L3 meta-test — driving bad scenarios and
asserting Mentat goes RED — is mandatory.

**Rationale:** A behaviour-test framework that is not itself proven to fail on bad behaviour
is unfalsifiable and worthless. Test-first and the L3 red-test are how the framework earns
the trust it asks others to place in it; hermetic defaults keep that proof fast and
deterministic.

## Engineering Standards & Tooling

- **Language/module:** Go (module `github.com/thetonymaster/mentat`), currently on Go 1.25.
- **Formatting & static analysis:** `gofmt -l .` MUST be clean and `go vet ./...` MUST pass
  before any commit; run `golangci-lint run` when a `.golangci.yml` is present. `make ci`
  runs the full gate and is the source of truth.
- **Errors:** wrap with `fmt.Errorf("doing X: %w", err)`; messages name the concrete failing
  thing and the offending value, never `"invalid input"`.
- **Interfaces:** small and consumer-defined; keep files focused on one responsibility and
  split when they grow unwieldy.
- **Mocks:** generate uber gomock mocks for the core interfaces via `go generate ./...`;
  commit generated mocks. Trivial value stubs are acceptable only when no call-count or
  argument verification is needed.
- **Concurrency in tests:** prefer `t.Parallel()` in new table-driven tests that share no
  mutable state (it surfaces data races that matter for trace correlation); it is a soft
  default, not a gate. It is REQUIRED in `//go:build e2e` tests (a real ~7× wall-clock win),
  and MUST be skipped for tests using `t.Setenv`/`t.Chdir`.

## Development Workflow & Quality Gates

- **Routing:** behaviour changes (features/bugfixes) go through **go-test-writer** (owns the
  TDD loop). Scaffolding, config, `deploy/`, dependency bumps, mock regeneration, and
  behaviour-preserving refactors go through **go-coder**. Context gathering before planning
  goes through **go-context-builder**.
- **Review gate:** **go-reviewer** in `gate` mode performs the exhaustive pre-commit audit
  of the staged diff and issues a PASS/BLOCK verdict; `pair` mode gives lightweight mid-task
  scans. It enforces these principles, the testing rules, Conventional Commits, and the
  no-AI-attribution rule.
- **Coverage:** verify the 80% floor with the `/coverage` skill (or `go test ./...
  -coverprofile=cover.out && go tool cover -func=cover.out`) before committing.
- **Git hygiene:** Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`,
  `chore:`). `git add .` is FORBIDDEN — stage files individually and know what you commit.
  No AI attribution in commits or PRs (no "Generated with…", no `Co-Authored-By`).

## Governance

This constitution supersedes other conventions where they conflict; the project's
`CLAUDE.md` is the operational contributor guide and MUST stay consistent with these
principles. Where global and repo guidance overlap, the stricter rule wins, and the global
`~/.claude/CLAUDE.md` epistemics/process rules take precedence on process.

**Amendments:** propose the change in a PR that (a) states the principle or section added,
changed, or removed and the rationale, (b) bumps the version per the policy below, and
(c) updates every dependent artifact listed in the Sync Impact Report. Amendments require
the repository owner's approval before merge.

**Versioning policy (semantic):**
- **MAJOR** — backward-incompatible governance changes: a principle removed or redefined.
- **MINOR** — a new principle or section added, or materially expanded guidance.
- **PATCH** — clarifications, wording, and non-semantic refinements.

**Compliance:** every PR MUST pass `make ci` and the **go-reviewer** `gate` audit, which
verify adherence to these principles. Any deliberate deviation MUST be justified in the PR
description (mirroring the plan template's Complexity Tracking) and approved explicitly;
unjustified violations block the merge.

**Version**: 1.0.0 | **Ratified**: 2026-06-23 | **Last Amended**: 2026-06-23
