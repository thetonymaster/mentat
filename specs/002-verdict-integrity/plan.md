# Implementation Plan: Verdict Integrity — Eliminate Silent False Verdicts

**Branch**: `002-verdict-integrity` | **Date**: 2026-07-01 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/002-verdict-integrity/spec.md`

## Summary

Close the eight silent-false-verdict paths found by the 2026-07-01 audit
(`docs/audits/2026-07-01-codebase-audit.md`, findings A1–A8, plus test-hardening
F3/F5): normalize span status/kind to a canonical vocabulary at the store boundary
(A1, A5), make every harness failure loud in single-run scenarios while keeping the
typed-failed-sample model for `@runs(N)` (A2), turn deadline-with-unstable-spans into
a hard error (A3), make trace search complete-or-loud (A4), stop fabricating zero
boundary values for failed samples in aggregates (A6), reject malformed fixture
parent references (A7), and forbid report derivation from flipping verdicts (A8).
All fixes are TDD (red→green per finding) via go-test-writer; one new e2e proves the
error-status path goes red against live Tempo.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: godog (BDD runner), cel-go (aggregate expressions),
`go.uber.org/mock` (seam mocks), Tempo HTTP API (trace store), OTLP JSON encoding

**Storage**: Tempo (live, via `internal/store/tempo.go`) and JSON fixtures
(`internal/store/filestore.go`); no schema migrations — fixture format gains
validation, spans gain a `Kind` field

**Testing**: `go test` (hermetic unit, table-driven, gomock), `//go:build e2e`
against `make harness-up` (deploy/ Tempo stack), L3 meta-features under
`features/meta/`

**Target Platform**: developer workstations + Linux CI

**Project Type**: single Go module — CLI (`cmd/mentat`, `cmd/mentatctl`) over
library packages (`internal/...`)

**Performance Goals**: no regression; A3/A4 touch the resolve path but add no new
polling (deadline handling and one query parameter only)

**Constraints**: no exported-API change outside `internal/`; `core.Evidence` may
gain fields but existing field semantics stay (additive); all error messages name
the concrete failing thing and value (constitution IV)

**Scale/Scope**: 6 packages touched (`store`, `correlate`, `engine`, `comparator`,
`report`, `steps`) + `e2e/` + fixtures; ~8 red→green pairs + 2 hardening tests

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS (no violations).*

- **I. Evidence-Only Comparators**: PASS. All fixes are inside store/correlate/
  engine/steps or consume Evidence; no comparator gains store/driver access.
  Canonical status lives on the `trace.Span` inside Evidence.
- **II. Trace Is a Forest, Tag-First**: PASS — strengthened. A4 removes the silent
  20-trace forest truncation; F3 pins the `test.run.id` query tag in a unit test.
- **III. Seams Are Interfaces, Wired Once**: PASS. No new seams, no DI framework;
  the status/kind normalization is a store-boundary concern implemented inside the
  existing `TraceStore` implementations.
- **IV. No Silent Fallbacks**: PASS — this feature is the enforcement of IV on the
  five audited paths. New errors are wrapped, name run id / span / index / value.
- **V. Test-First & Hermetic**: PASS. One failing test per finding first (SC-001);
  unit tests hermetic (inmem/fixture + gomock); the live-Tempo error-status test is
  `//go:build e2e` (FR-012); coverage floor re-checked per touched package.

## Project Structure

### Documentation (this feature)

```text
specs/002-verdict-integrity/
├── plan.md              # This file
├── research.md          # Phase 0: decisions (vocabulary, truncation, sample semantics)
├── data-model.md        # Phase 1: Evidence/Span/sample field changes
├── quickstart.md        # Phase 1: how to validate the feature end-to-end
├── contracts/
│   └── evidence-vocabulary.md   # canonical status/kind values + failure-mode error contracts
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/
├── trace/          # Span gains Kind; canonical status/kind constants live here
├── store/          # tempo.go: status/kind decode + search limit; filestore.go: parentIndex validation + kind
├── correlate/      # correlate.go: deadline → hard error; F3 unit test pins query tag
├── engine/         # engine.go: driveOnce retains Output on resolve failure; Evidence failure message
├── comparator/     # budgets.go errorCount over canonical status; aggregate_cel.go guarded boundary binding
├── report/         # derive.go: derivation degradation note instead of verdict flip
└── steps/          # steps.go: single-run drive failure surfaces the underlying error

e2e/                # new error-status meta test (SUT emits errored span → assertion goes red)
features/meta/      # new bad-behaviour feature for the error path
tracelab/           # researchbot/orderflow: a scenario mode that emits an errored span
testdata/traces/    # fixtures migrated to canonical status spelling; kind added where asserted
```

**Structure Decision**: existing single-module layout; every change lands in the
package that owns the seam, wired through the existing composition root — no new
packages, no moved files.

## Complexity Tracking

No constitution violations — table intentionally empty.
