# Implementation Plan: Extension-Surface Integrity

**Branch**: `009-extension-surface-integrity` | **Date**: 2026-07-18 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/009-extension-surface-integrity/spec.md`

**Note**: This template is filled in by the `/speckit-plan` command. See `.specify/templates/plan-template.md` for the execution workflow.

## Summary

Make every guarantee of the public extension surface machine-enforced before any new
seam is added, by closing five verified holes: (1) the surface gate ignores exported
struct fields of aliased types (realized drift: `Verdict.Qualifiers`,
`Target.Completeness` landed with zero golden churn); (2) `config.Completeness` is
reachable from `mentat.Target` but not nameable from the facade; (3) `mentat.Run`
never applies `config.Load`'s resolution, so code-built Configs silently diverge from
YAML-loaded ones (settle defaults, budget, compiled extraction, judge validation);
(4) seam-addition knowledge is tribal and the two "six seams" taxonomies disagree;
(5) the 20-run L3 stability requirement has no enforcing workflow.

Approach (decisions recorded in [research.md](./research.md)): extend the stdlib-AST
golden renderer with a struct index + exported-field renderer and regenerate the
golden once, deliberately; extract `config.Resolve` from `config.Load` with
explicit-value-wins idempotent semantics and call it at the top of `mentat.Run`
before all three composition calls (story (a) of FR-008); alias `Completeness` (and
anything else the sweep finds) on the facade and freeze nameability with a
facade-only composite-literal compile test; write `docs/extending/new-seam.md` as
the canonical taxonomy + checklist referenced from both divergent locations; add a
`nightly-l3.yml` workflow (cron + manual dispatch) running the e2e suite with
`MENTAT_L3_RUNS=20`.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: stdlib only for the surface gate (`go/parser`, `go/printer`, `go/ast` — no `go/types`/`x/tools`, existing constraint); `godog` (BDD layer, untouched); `go.uber.org/mock` (parity-test mocks where needed); GitHub Actions + `deploy/` docker-compose harness (Tempo/Collector) for the nightly e2e lane

**Storage**: N/A (artifacts are the committed golden `specs/007-public-extension-api/contracts/public-surface.golden` and YAML fixture files for parity tests)

**Testing**: `go test` — table-driven, hermetic by default; documented mutation rehearsals for the surface gate (precedent: `surface_test.go:42-52`, `54-70`); facade-only compile test (`mentat_external_test.go` + `examples/kafkaecho` external module); `//go:build e2e` for the L3 lane (`e2e/completeness_meta_test.go:31` consumes `MENTAT_L3_RUNS`, parser `parseL3Runs` in `e2e/l3runs.go`)

**Target Platform**: any Go 1.25 platform for the library; ubuntu GitHub Actions runners for CI/nightly

**Project Type**: single Go module — library facade + two CLIs. Mostly dev-infra hardening of the public surface, with one deliberate RUNTIME change: `mentat.Run` now applies `config.Resolve` (US2), so library callers get the same defaults, twin resolution and hard errors the YAML path has always had. That is behaviour, not tooling — it is why US2 goes through go-test-writer and carries its own regression tests.

**Performance Goals**: surface test stays sub-seconds (stdlib parsing of ~4 internal dirs, cached per run); nightly L3 lane completes within existing e2e wall-clock (suite already `t.Parallel`, ~7× overlap of trace-ingestion waits; 20 sequential meta-runs is the long pole, budget ≤ ~30 min on a cold runner)

**Constraints**: renderer stays stdlib-AST only; no new public seams (explicit spec out-of-scope); golden regeneration only via `MENTAT_UPDATE_GOLDEN=1` and every golden diff called out in the PR body; per-package coverage floor ≥80%; resolution added to `mentat.Run` must be idempotent (CLI paths `cmd/mentat/main.go:74`, `cmd/mentatctl/main.go:310` feed already-Load-resolved configs through the same code)

**Scale/Scope**: ~25 aliased struct types gain frozen field sets in the golden (one deliberate mass churn); 3 audited Load-only behaviours (completeness defaults, `ExtractConfig.compiled`, `Target.Budget`) + 2 suspected (zero-budget semantics in `Drive`, `validateJudge` temperature/`MaxCostUSD` rules) to cover; 1 new doc, 1 new workflow, 2 taxonomy sites reconciled

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Assessment | Verdict |
|-----------|------------|---------|
| I. Evidence-Only Comparators | Untouched. The completeness contract keeps riding the resolve seam (`internal/engine/engine.go:74-75`); no comparator gains access to anything beyond `Evidence`. | PASS |
| II. Trace Is a Forest, Correlation Is Tag-First | Untouched. No correlation or trace-shape code changes. | PASS |
| III. Seams Are Interfaces, Wired Once (Manual DI) | No new seams (spec out-of-scope). `config.Resolve` is called from `mentat.Run` ahead of the existing composition calls (`run.go:293/310/349`) — wiring topology unchanged; registries untouched except a doc comment referencing the canonical taxonomy. | PASS |
| IV. No Silent Fallbacks | This feature *enforces* the principle: it eliminates the known silent YAML-vs-code divergences. `config.Resolve` returns hard, descriptive errors naming field and value (same rules Load applies today); no new defaults are invented — Load's existing defaulting becomes path-independent. | PASS |
| V. Test-First & Hermetic by Default | Items 1–3 are behaviour changes routed through go-test-writer TDD (red→green mutation rehearsals, table-driven parity tests, facade-only compile test — all hermetic). Items 4–5 are docs/CI via go-coder. The nightly lane *strengthens* the L3 requirement. Coverage floor enforced via `/coverage` before PR. | PASS |

**Initial gate: PASS (no violations, Complexity Tracking empty).**

**Post-design re-check (after Phase 1)**: PASS — the contracts introduce no new seams, no DI framework, no comparator reach-through, and no silent defaults; `config.Resolve`'s explicit-value-wins rules are loud (documented error catalogue in [contracts/config-resolve.md](./contracts/config-resolve.md)).

## Project Structure

### Documentation (this feature)

```text
specs/009-extension-surface-integrity/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
│   ├── surface-golden-v2.md
│   ├── config-resolve.md
│   ├── facade-nameability.md
│   ├── seam-taxonomy.md
│   └── nightly-l3.md
├── checklists/
│   └── requirements.md  # spec quality checklist (complete)
└── tasks.md             # Phase 2 output (/speckit-tasks command - NOT created by /speckit-plan)
```

### Source Code (repository root)

```text
mentat.go                        # facade aliases — add `Completeness = config.Completeness` + sweep results
run.go                           # mentat.Run — call config.Resolve before BuildCorrelator/BuildStore/Build
surface_test.go                  # golden renderer — struct index + exported-field renderer; new mutation rehearsal doc block
mentat_external_test.go          # facade-only composite-literal compile test (extends existing alias-identity assertions)
internal/config/config.go        # extract Resolve() from Load; per-field idempotent semantics; descriptive errors
internal/config/config_test.go   # parity table: YAML-Load vs struct-literal → identical effective contract
internal/engine/engine.go        # completenessContract comment corrected (defaulting now guaranteed on both paths)
internal/registry/registry.go    # seams doc comment (registry.go:21-22) points at canonical taxonomy
internal/core/core.go            # unchanged — its exported fields become frozen golden lines
docs/extending/stability.md      # restore the strong struct-field-freezing claim (drop the interim-gap section)
docs/extending/new-seam.md       # NEW — seam-addition checklist, three tribal decisions, canonical taxonomy
specs/007-public-extension-api/contracts/public-surface.md      # taxonomy site #2 → references canonical list; AggregateComparator exclusion sentence
specs/007-public-extension-api/contracts/public-surface.golden  # regenerated PER STORY via MENTAT_UPDATE_GOLDEN=1, each regen isolated and PR-called-out: US1 struct-field expansion (+102), US3 Completeness alias (+4), then the review-driven field ordinal (105 lines re-annotated, no field added or lost)
.github/workflows/nightly-l3.yml # NEW — cron + workflow_dispatch; harness up; go test -tags e2e with MENTAT_L3_RUNS=20
e2e/l3runs.go                    # defaultL3Runs doc comment becomes true (nightly lane exists)
examples/kafkaecho/              # live external-module consumer; benefits from parity fix (main_test.go:23 builds Config in code)
```

**Structure Decision**: Single Go module, existing layout. The feature touches the
facade root (aliases, gate, compile test), `internal/config` (resolution seam),
docs (`docs/extending/`), the 007 contracts dir (golden + taxonomy site), and CI
(`.github/workflows/`). No new packages; the only new files are `new-seam.md`,
`nightly-l3.yml`, and test fixtures.

## Complexity Tracking

No constitution violations — table intentionally empty.
