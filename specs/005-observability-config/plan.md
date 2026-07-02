# Implementation Plan: Observability & Config Integrity

**Branch**: `005-observability-config` | **Date**: 2026-07-01 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/005-observability-config/spec.md`

## Summary

Give Mentat a voice and a spine (audit cluster D, D1–D6): `log/slog`-based leveled
logging behind `-v`/`-vv` on both binaries, injected through the existing seams so
the engine/correlate/driver narrate the run lifecycle to stderr (D1); enriched
trace-not-found errors carrying endpoint + query + diagnostic checklist (D1);
strict YAML config decoding (`KnownFields`) matching the expectations loader (D2);
adapter validation at `engine.Build` against the driver registry — the registry as
the only truth (D3); telemetry env injection that skips empty values and merges
`OTEL_RESOURCE_ATTRIBUTES` (D4); ordinal parse errors surfaced (D5); and a shared
`BuildCorrelator` so both binaries wire identical poll defaults (D6).

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: `log/slog` (stdlib) with a text handler to stderr;
`gopkg.in/yaml.v3` `Decoder.KnownFields(true)` (already used strictly by
`internal/expectations`); existing registries

**Storage**: none; config format unchanged (strictness only)

**Testing**: pinned-substring log/error tests (`slog` into a `bytes.Buffer`
handler); table test over every config section with a typo'd key; env-inspection
driver tests; golden stdout test for the silent happy path

**Target Platform**: developer workstations + Linux CI

**Performance Goals**: zero overhead when silent (disabled slog handlers are
no-ops); narration adds no polling rounds

**Constraints**: logs to stderr only (stdout is report/consumer territory); env
values echoed only for Mentat-set keys; error-class compatibility with features
002/004 (message enrichment, not new error types)

**Scale/Scope**: 7 packages touched (`config`, `engine`, `correlate`, `driver`,
`steps` ordinal, `registry` adapter check, both `cmd/`); ~9 red→green pairs

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS (no violations).*

- **I. Evidence-Only Comparators**: PASS. Comparators receive no logger in this
  feature (their verdicts already carry reasons); narration lives in engine/
  correlate/driver.
- **II. Trace Is a Forest, Tag-First**: PASS. `test.run.id` keeps winning
  attribute-merge collisions (spec edge case); injection paths otherwise
  untouched.
- **III. Seams Are Interfaces, Wired Once**: PASS — strengthened. D6 gives the
  correlator the same single-construction treatment as every other seam; the
  logger is constructor-injected at the composition root (no package-global
  logger state).
- **IV. No Silent Fallbacks**: PASS — strengthened. D2/D3/D5 convert three silent
  acceptances into hard, named errors.
- **V. Test-First & Hermetic**: PASS. All log/error assertions are hermetic
  buffer tests; one e2e scripts the dead-collector diagnosis walk; coverage floor
  per touched package.

## Project Structure

### Documentation (this feature)

```text
specs/005-observability-config/
├── plan.md              # This file
├── research.md          # Phase 0: logger seam, strictness mechanics, merge policy
├── data-model.md        # Phase 1: log schema, error shapes, config validation states
├── quickstart.md        # Phase 1: validation guide
├── contracts/
│   └── narration-and-errors.md   # log line schema + pinned error substrings
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/
├── config/         # strict KnownFields decode; allowlist reduced to registered adapters
├── engine/         # logger injection via Build options; adapter check vs registry;
│                   #   env-injection policy (skip empty, merge resource attrs)
├── correlate/      # BuildCorrelator + poll defaults single-sourced; per-poll narration;
│                   #   enriched timeout error (endpoint, query, checklist)
├── driver/         # shell/http: invocation narration; resource-attr merge helper
├── steps/          # ordinal Atoi error surfaced with step + value
└── registry/       # expose registered-driver listing for the build-time check

cmd/mentat/         # -v/-vv flags; slog handler to stderr
cmd/mentatctl/      # same flags; drops its copy-pasted correlator defaults
```

**Structure Decision**: logger flows constructor-down from the composition root
(no globals), matching the manual-DI principle; strictness and validation land in
the packages that own those decisions today.

## Complexity Tracking

No constitution violations — table intentionally empty.
