# Phase 1 Data Model: Public Extension API

The public surface inventory — this table seeds the manifest
([contracts/public-surface.md](./contracts/public-surface.md)). Everything not
listed stays internal. References research.md R1–R7.

## Facade package `mentat` (repo root)

### Seam interfaces (aliases to internal/core)

| Symbol | Kind | Justification |
|--------|------|---------------|
| `Driver` | alias | implementable seam (registration hook exists) |
| `TraceStore` | alias | implementable seam (registration hook exists) |
| `Comparator` | alias | implementable seam (registration hook exists) |
| `Judge` | alias | implementable seam (registration hook exists) |
| `Correlator` | alias | referenced by contracts; **no hook** (three-examples rule) |
| `Reporter` | alias | referenced by contracts; **no hook** |

### Evidence & contract types (aliases)

`Evidence`, `Trace`, `Span`, `Verdict`, `Expectation`, `RunSpec`, `Output`,
`TraceQuery`, `TraceRef`, `FailureKind` constants, canonical `Status*`/kind
constants (feature 002 vocabulary). Each exists because a seam-interface method
signature or documented contract references it — no convenience exports.

### Registration & run

| Symbol | Signature (shape) | Rules |
|--------|-------------------|-------|
| `Config` | struct / `LoadConfig(path)` | mirrors mentat.yaml; constructible in code |
| `Option` | opaque functional option | |
| `WithDriver(name, DriverFactory)` | Option | duplicate name → `Run` error naming both registrants |
| `WithStore(name, StoreFactory)` | Option | same |
| `WithComparator(name, ComparatorFactory)` | Option | same |
| `WithJudge(name, JudgeFactory)` | Option | same |
| `Run(ctx, Config, ...Option) (Results, error)` | func | fresh sealed composition root per call; reentrant; honors ctx cancellation (feature 003 semantics) |

### Results

| Symbol | Fields | Rules |
|--------|--------|-------|
| `Results` | `Scenarios []ScenarioResult`, `Passed/Failed int`, `Interrupted bool`, judge/report aggregates as published by feature 006 | status equivalent to CLI exit semantics |
| `ScenarioResult` | name, feature file, verdict, reasons, run ids, derivation note | mirrors report collector entries |

## Not exported (explicit)

- Registries and sealing machinery (`internal/registry`) — hooks only.
- Engine, steps, correlate/poll internals, config parsing internals.
- Tempo/shell/http/claude concrete implementations (reachable via config
  names, not types).
- godog wiring (feature-file execution is behind `Run`).

## Example module (examples/kafkaecho)

| Artifact | Purpose |
|----------|---------|
| `go.mod` (separate module) | proof the facade suffices externally |
| toy `Driver` impl + factory | follows docs/extending/driver.md |
| feature file + file-store fixture | hermetic green run via `mentat.Run` |
| CI job | build + run + `grep mentat/internal` = empty |

## Surface gate

| Artifact | Rule |
|----------|------|
| `contracts/public-surface.golden` | canonical rendering of exported names/signatures; `go test` diff-fails naming changed symbols; updating it in-PR = acknowledgment |
| `contracts/public-surface.md` | human manifest: every symbol justified (this table, maintained) |
