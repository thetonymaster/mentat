# Implementation Plan: Public Extension API — Extensible Without Forking

**Branch**: `007-public-extension-api` | **Date**: 2026-07-01 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/007-public-extension-api/spec.md`

## Summary

Publish the minimum viable extension surface (audit G1): a root `mentat` facade
package exposing the six seam interfaces and evidence types via type aliases to
the (unmoved) internal packages, `With*` registration options and a `mentat.Run(ctx, cfg,
opts...) (Results, error)` entry point that compose with `engine.Build` and its
sealing (feature 003); an `examples/kafkaecho` toy-driver module compiled and
run in CI against the facade only; an API manifest + `go doc`-golden diff gate;
per-seam implementation guides; and the CLI re-composed over the same public
path with byte-identical output.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`, stays
v0)

**Primary Dependencies**: type aliases (Go 1.9+) for zero-copy re-export;
`golang.org/x/exp/apidiff` or golden-`go doc` test for the surface gate
(decision: golden test — stdlib-only, see research R4); examples module via
`go.work`/separate go.mod built in CI

**Storage**: none; file store (feature 006) used as the hermetic vehicle for
example + library-mode tests

**Testing**: external-style tests (`package mentat_test` importing only the
facade), example-module CI build + import-lint (grep for `/internal/`), golden
CLI output comparison, manifest-diff golden test

**Target Platform**: developer workstations + Linux CI

**Project Type**: single Go module + one example module

**Performance Goals**: none (API surface feature); zero runtime overhead from
aliasing

**Constraints**: one-way door — every exported symbol individually justified in
the manifest; no internal type may appear in a public signature; CLI output
byte-identical (SC-004)

**Scale/Scope**: 1 new facade package (~200 lines of aliases/hooks/Run), 1
example module, 6 seam guides, CI wiring; internal packages unmoved

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS (no violations).*

- **I. Evidence-Only Comparators**: PASS — now *published*: the facade exposes
  Evidence but no store/driver access to comparator implementers; the comparator
  seam guide documents the boundary as a contract for third parties.
- **II. Trace Is a Forest, Tag-First**: PASS — the store/driver seam guides make
  forest semantics and tag injection explicit implementer obligations.
- **III. Seams Are Interfaces, Wired Once**: PASS — strengthened. Public
  registration funnels into the same registries at the same composition root;
  registries themselves stay unexported; `mentat.Run` builds a fresh root per
  call.
- **IV. No Silent Fallbacks**: PASS. Duplicate-name registration fails loudly;
  post-seal registration is unrepresentable on the public surface (options only
  exist at `Run`) and stays loud internally (feature 003); `Run` returns errors,
  never partial silent results.
- **V. Test-First & Hermetic**: PASS. External-style tests + example module run
  hermetically on the file store; every acceptance scenario lands red→green.

## Project Structure

### Documentation (this feature)

```text
specs/007-public-extension-api/
├── plan.md              # This file
├── research.md          # Phase 0: facade vs move, alias mechanics, gate tooling
├── data-model.md        # Phase 1: public surface inventory (the manifest seed)
├── quickstart.md        # Phase 1: validation guide
├── contracts/
│   └── public-surface.md     # the API manifest: every exported symbol, justified
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
mentat.go (root package)   # facade: aliases, With* options, Run, Results
internal/…                 # unmoved; unexported to the outside as before
examples/kafkaecho/        # separate module: toy driver + feature file + go.mod
docs/extending/            # per-seam implementation guides (driver, store,
                           #   comparator, judge, + evidence-types primer)
cmd/mentat/                # re-composed over mentat.Run path (golden-checked)
.github/ or Makefile ci    # example build + import lint + surface golden test
```

**Structure Decision**: root facade package with type aliases; internal
packages stay put (smallest diff, no import churn in 20+ packages, surface
controlled symbol-by-symbol). Recorded as the reversible half of the one-way
door: aliases can be re-pointed if internals reorganize; the *exported names*
are the commitment.

## Complexity Tracking

No constitution violations — table intentionally empty.
