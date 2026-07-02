# Implementation Plan: DX & Product Completeness

**Branch**: `006-dx-completeness` | **Date**: 2026-07-01 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/006-dx-completeness/spec.md`

## Summary

Close the audited design-vs-implementation gaps (cluster E, E1–E9) as nine
independently shippable slices: a generated step reference (metadata on the
existing registration table → docs page + `mentat steps`), a `mentat validate`
dry-run reusing the scenario-init prechecks, godog multi-formatter for
JUnit+console, HTTP body steps writing the dormant `RunSpec.Input`, a `file`
store factory over the existing fixture format, a judge cost ledger + budget +
sane defaults (fast-tier model, vote diversity), the designed `mentatctl agent
run` summary and input/output flags, configurable answer extraction, and
prebuilt lab SUT binaries + the two convention-fix e2e tests.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: godog multi-formatter (`Format: "pretty,junit:file"`
comma syntax), existing registries and precheck functions, Anthropic SDK usage
metadata (judge ledger), existing pricing table (`internal/comparator` cost
rules), Makefile harness targets

**Storage**: `store: file` config value + fixture directory; no new formats
(serves `ctl.WriteFixture` output)

**Testing**: hermetic throughout except one offline-replay e2e (docker stopped)
and the harness prebuild; drift test for step reference; golden tests for
summaries

**Target Platform**: developer workstations + Linux CI

**Performance Goals**: validate < 1s on the repo's feature corpus; lab drives
shed 100–300ms/run toolchain overhead; judge default-cost ≥80% cheaper

**Constraints**: zero verdict changes on green suites (SC-008); no silent
fallback anywhere (extraction, fixtures, budget); story-level independence
preserved for tasking

**Scale/Scope**: 9 slices; packages: `steps` (metadata, body/extraction steps),
`cmd/mentat` (steps/validate subcommands, formatter), `store` (+factory),
`engine/store.go` (registry entry), `judge`+`comparator`+`report` (ledger,
budget), `ctl`+`cmd/mentatctl` (summary/flags), `core` (extractor policy),
`config` (extraction, judge budget, store), Makefile/tracelab/e2e (E9)

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS with one
justified note (see Complexity Tracking).*

- **I. Evidence-Only Comparators**: PASS. The judge ledger is collected at the
  judge seam and carried through Verdict/report metadata — comparators still
  see only Evidence; cost bookkeeping rides the existing Verdict path.
- **II. Trace Is a Forest, Tag-First**: PASS. The file store returns saved
  forests; correlation contract unchanged.
- **III. Seams Are Interfaces, Wired Once**: PASS — strengthened. The file
  store becomes the second registered `TraceStore` factory (registry finally
  exercised by production config); extractor policy is config-driven, applied
  in `core`/driver layer, no new globals.
- **IV. No Silent Fallbacks**: PASS. Marker/pattern extraction failures are
  scenario failures; missing fixtures are hard errors; budget stop is a hard
  descriptive error; votes>1@temp0 is a loud config error.
- **V. Test-First & Hermetic**: PASS. Every slice red→green; validate and steps
  subcommands fully hermetic; the offline-replay proof runs with the network
  disabled by construction.

## Project Structure

### Documentation (this feature)

```text
specs/006-dx-completeness/
├── plan.md              # This file
├── research.md          # Phase 0: per-slice decisions (formatter, metadata, ledger, extractor)
├── data-model.md        # Phase 1: step metadata, ledger, config additions
├── quickstart.md        # Phase 1: validation guide
├── contracts/
│   ├── cli-surface.md        # steps/validate subcommands, mentatctl flags, dual output
│   └── judge-ledger.md       # report fields, budget semantics, default-model policy
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/steps/       # step metadata table (pattern+doc+example), body + extraction steps
cmd/mentat/           # `steps` + `validate` subcommands; multi-formatter wiring
internal/store/       # file store factory (serves WriteFixture format)
internal/engine/      # store registry entry "file"; extractor policy plumb-through
internal/judge/       # usage capture per call (tokens in/out, model)
internal/comparator/  # semantic matcher: ledger emission, votes/temperature guard
internal/report/      # judge ledger fields in JSON/HTML + suite totals; budget check
internal/config/      # judge.budget_usd, judge default model, target.extract, store: file
internal/core/        # Answer extractor policy types (whole|marker|pattern)
internal/ctl/         # run summary enrichment (tokens/cost/latency/trace ids)
cmd/mentatctl/        # --prompt-file/stdin, -o, --timeout flags
Makefile + tracelab/  # prebuilt lab SUT binaries; mentat.yaml points at binaries
e2e/                  # report_meta tests → mentatBin + t.Parallel; offline replay test
docs/steps.md         # generated step reference (committed artifact + drift test)
```

**Structure Decision**: nine story-scoped slices over the existing layout; the
only cross-slice coupling is config parsing (shared file) and the report schema
(ledger fields) — flagged for /speckit-tasks ordering.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| One feature carries 9 slices (breadth, not a principle violation) | Q explicitly chose "spec everything in E" as one feature | Nine separate features — 9× SpecKit overhead for items averaging ~1 day each; story-level independence is preserved instead, so slices still ship separately |
