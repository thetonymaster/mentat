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

The T003 skeleton's minimal transitive closure forced four more seam
argument/return types not named in the prose list above; each is added here to
keep the manifest honest (SC-006):

| Symbol | Kind | Justification |
|--------|------|---------------|
| `RunResult` | alias | return type of `Driver.Run` (the driver seam's output) |
| `StoreCaps` | alias | return type of `TraceStore.Caps` (store capability descriptor) |
| `JudgeRequest` | alias | argument type of `Judge.Judge` (the matter to judge) |
| `JudgeVerdict` | alias | return type of `Judge.Judge` (the semantic verdict) |

T008/T009 (library-mode run) forced one more evidence-adjacent alias, transitively
required by the run results below (SC-006):

| Symbol | Kind | Justification |
|--------|------|---------------|
| `JudgeUsage` | alias | judge-token ledger carried by `Results.JudgeTotal` and `ScenarioResult.Judge` (the report data FR-003 returns; nil ⇒ no judge call, no fabricated zeros) |

### Config surface (aliases to internal/config) — T008/T009

`type Config = config.Config` (research R2: alias, no duplicate/conversion type)
satisfies FR-003 "constructible in code" + `LoadConfig`. An alias to `Config` alone,
however, does NOT let an external package (`package mentat_test`, the whole point of
the surface) **name** the nested field types to build a `Config` literal (e.g. a
`Targets` map). So the nested "mentat.yaml surface" types are aliased explicitly —
this is the concrete realization of decision R2's "transitively exposes the nested
config types", forced by the external-only in-code-construction test (SC-002):

| Symbol | Kind | Justification |
|--------|------|---------------|
| `Config` | alias | whole mentat.yaml config; `Run`'s second arg; `LoadConfig` result |
| `Target` | alias | element type of `Config.Targets` — nameable to build a target literal |
| `HTTP` | alias | `Target.HTTP` (http-adapter request config) |
| `ExtractConfig` | alias | `Target.Extract` (answer-extraction policy) |
| `Endpoint` | alias | `Config.Tempo` |
| `PollSpec` | alias | `Config.Poll` (trace stability-poll config) |
| `Pricing` | alias | `Config.Pricing` (model→rate map) |
| `ModelRate` | alias | a `Pricing` value (input/output per-MTok rate) |
| `JudgeConfig` | alias | `Config.Judge` (semantic-matcher config) |
| `RunBudget` | alias | `Config.Budget` / `Target.Budget` (resolved lifecycle bound) |

### Registration & run

| Symbol | Signature (shape) | Rules |
|--------|-------------------|-------|
| `Config` | struct / `LoadConfig(path)` | mirrors mentat.yaml; constructible in code |
| `LoadConfig(path) (Config, error)` | func | `os.ReadFile` + `config.Load`; every error names the path (Constitution IV) — **implemented T008/T009** |
| `Option` | opaque functional option (`func(*runOptions)`, unexported state) — **implemented T008/T009** | |
| `WithFeatures(paths ...string)` | Option | **necessary addition beyond the prose list** — `config.Config` has no feature-paths field (the CLI passes them as args), yet FR-003 requires running feature files. Additive across calls; **no** implicit `features` default (godog silently defaults empty `Paths` to `./features` — refused loudly, Constitution IV). **implemented T008/T009** |
| `WithDriver(name, DriverFactory)` | Option | duplicate name → `Run` error naming the seam+conflict (built-in or earlier registration); **implemented T004/T005** |
| `WithStore(name, StoreFactory)` | Option | same; registers a `TraceStore` (the seam type — the `Store` short form is used only for the option/factory names); **implemented T004/T005** |
| `WithComparator(name, ComparatorFactory)` | Option | same; **implemented T004/T005** |
| `WithJudge(name, JudgeFactory)` | Option | same; the custom judge is USED only when `cfg.Judge.Backend == name`, but the collision check runs unconditionally; **implemented T004/T005** |
| `Run(ctx, Config, ...Option) (Results, error)` | func | fresh sealed composition root per call; reentrant; honors ctx cancellation (feature 003 semantics) |

### Factory types (registration) — T004/T005

The `With*` hooks take a factory, not an instance, so a registered adapter is
constructed from the resolved `Config` at the composition root (uniform with the
built-in `tempo`/`file`/`claude` factory shape). Each is a zero-cost func-type
alias over the facade seam/config aliases, so an external module names them without
importing anything internal (SC-006).

| Symbol | Kind | Justification |
|--------|------|---------------|
| `DriverFactory` | alias `func(Config) (Driver, error)` | argument type of `WithDriver`; builds a custom driver from config |
| `StoreFactory` | alias `func(Config) (TraceStore, error)` | argument type of `WithStore`; builds a custom trace store from config |
| `ComparatorFactory` | alias `func(Config) (Comparator, error)` | argument type of `WithComparator`; builds a custom comparator from config |
| `JudgeFactory` | alias `func(Config) (Judge, error)` | argument type of `WithJudge`; builds a custom judge from config |

### Results

| Symbol | Fields | Rules |
|--------|--------|-------|
| `Results` | `Scenarios []ScenarioResult`, `Passed/Failed int`, `Interrupted bool`, plus the suite report aggregates mirroring `core.RunReport`: `TotalCost float64` and `JudgeTotal *JudgeUsage` (suite-wide judge ledger; nil when no scenario made a judge call — no fabricated zeros) | status equivalent to CLI exit semantics; aggregates asserted in T008 — **implemented T008/T009** |
| `ScenarioResult` | `Name string`, `FeatureFile string`, `Pass bool`, `Reasons []string`, `Cost float64`, `RunIDs []string`, `DerivationNote string`, `Judge *JudgeUsage` — facade-OWNED struct (not an alias) so `core.RunRecord` etc. never leak | mirrors report collector entries — **implemented T008/T009** |

`FeatureFile` is populated from Godog's `scenario.Uri` threaded through
`steps.go` → `report.Derive` → `core.ScenarioResult` → the facade, so a consumer
running several feature files can tell scenarios apart by origin, not just by
`Name` (which may collide across files).

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
