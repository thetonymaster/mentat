# Phase 0 Research: Public Extension API

## R1. Facade vs package move

**Decision**: Root-level `mentat` facade package using type aliases
(`type Driver = core.Driver`, `type Evidence = core.Evidence`, …) plus thin
registration/run functions. `internal/` packages do not move.

**Rationale**: Aliases give identical type identity (a third-party
implementation of the alias satisfies the internal interface — no adapters),
zero runtime cost, and symbol-by-symbol control over the surface. Moving
`internal/core` out wholesale would export every current and future symbol of
that package by default — the opposite of the manifest discipline. Import churn
across ~20 packages is also avoided.

**Alternatives considered**: (a) move `internal/core`+`registry` to `pkg/` —
surface becomes package-shaped, not symbol-shaped; accidental exports become
the default; (b) duplicate types with conversion layers — drift machine,
rejected outright.

**Verified premise**: exported aliases to internal types are usable by external
importers (the alias resides in a public package; Go's internal rule applies to
import paths, not type identity). The example module is the CI-enforced proof
of this premise — if it ever breaks (e.g. a future Go change), CI says so.

## R2. Public surface inventory (the minimality rule)

**Decision**: Exactly four groups (mirrors spec FR-001):
1. Seam interfaces: `Driver`, `TraceStore`, `Correlator`, `Comparator`,
   `Reporter`, `Judge`.
2. Types transitively required by their contracts: `Evidence`, `Trace`, `Span`,
   `Verdict`, `Expectation`, `RunSpec`, `Output`, `TraceQuery`, `TraceRef`,
   `RunRecord`/report data types needed by `Results`, plus the canonical
   status/kind constants (feature 002).
3. Registration hooks: `RegisterDriver(name, factory)`, `RegisterStore`,
   `RegisterComparator`, `RegisterJudge` — as `Run` options (see R3), not
   package-level mutable state.
4. Entry point: `Run(ctx, Config, ...Option) (Results, error)` + `Results` /
   `ScenarioResult` types; `Config` loaded from YAML or constructed.

Correlator/Reporter are exposed as *types* (contracts reference them) but get
no registration hook yet (three-examples rule; audit found zero demand).

## R3. Registration mechanics vs sealing (feature 003)

**Decision**: Registrations are `Option` values consumed by `Run`/`Build`
inside the composition root, applied before sealing:
`mentat.Run(ctx, cfg, mentat.WithDriver("kafka", newKafkaDriver))`. The CLI
uses the same path. Package-level `Register*` functions are NOT provided —
option-passing makes "register after seal" unrepresentable for library users
and keeps `Run` reentrant (each call builds a fresh sealed root; spec's
multiple-runs edge case: sequential safe, concurrent safe because no shared
mutable registration state — decided: supported and tested).

**Rationale**: options-at-composition beats init()-style global registration on
every axis the constitution cares about (explicit wiring, no hidden ordering,
sealing enforced by construction). Duplicate names: `Run` errors naming the
adapter and both sources.

## R4. API-diff gate tooling

**Decision**: golden-surface test: a unit test renders the facade's exported
surface via `go/packages` + `go/types` (names, signatures, field sets) into a
canonical text file `contracts/public-surface.golden`; any diff fails with the
symbol named; updating the golden file in the same PR is the acknowledgment
act. `apidiff` considered as CI extra later.

**Rationale**: stdlib-only, runs in `go test`, the acknowledgment artifact
lives in the repo and shows up in PR review — satisfying FR-005 with no new CI
infrastructure. The manifest doc (contracts/public-surface.md) explains each
symbol; the golden file enforces it.

## R5. Example extension shape

**Decision**: `examples/kafkaecho/` — its own go.mod requiring the mentat
module via a `replace` to the repo root (CI) — implements a toy driver that
"drives" a stub SUT and emits a fixture trace to the file store, plus one
feature file and a `go test` that runs it through `mentat.Run`. CI job: build,
run, and `grep -r "mentat/internal"` must return nothing.

**Rationale**: separate module is the only honest proof the surface suffices —
in-module code can accidentally use internal imports. Pairing with the file
store keeps it hermetic (no docker in the example CI job).

## R6. Seam guides

**Decision**: `docs/extending/{driver,store,comparator,judge}.md` + an
`evidence.md` primer. Each guide: the interface contract, the constitution
obligations that bind implementers (error wrapping, forest semantics, tag-first
injection for drivers, evidence-only boundary for comparators, capability
allowlist pattern for judges), and a walkthrough of the example driver.
Compiled example = living documentation; prose kept short.

## R7. CLI re-composition

**Decision**: `cmd/mentat` main becomes a thin caller of the same public
composition path (`mentat.Run` with options derived from flags), keeping flag
parsing and exit-code mapping. Golden test: green-run stdout byte-identical
pre/post (SC-004). If any CLI-only capability can't be expressed through the
public path, that's a surface gap to fix, not a second path to keep — the CLI
is consumer zero.
