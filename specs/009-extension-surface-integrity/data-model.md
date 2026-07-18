# Data Model: Extension-Surface Integrity (009)

No new runtime data structures are introduced — this feature hardens existing
artifacts. The entities below are the contractual objects the feature freezes,
resolves, or creates. Decisions referenced: [research.md](./research.md) R1–R5.

## 1. Public Surface Golden

The committed, reviewable rendering of every promise the facade makes
(`specs/007-public-extension-api/contracts/public-surface.golden`).

| Aspect | Rule |
|---|---|
| Rendered kinds (existing) | top-level funcs, consts, vars, type decls; interface aliases expanded to method sets (008 T028) |
| Rendered kinds (**new**) | aliases resolving to struct types expanded to their **exported field sets** (name + type as written in aliased source, printer-normalized); unexported fields omitted; embedded fields rendered as written |
| Unchanged kinds | aliases of maps, funcs, `any` stay single-line (declaration text is the full shape) |
| Source of truth | stdlib AST of facade files + aliased internal package dirs (no `go/types`) |

**States**: `in-sync` (test PASS) → `drifted` (any surface change; test FAIL naming
the drift) → `deliberately-regenerated` (only via `MENTAT_UPDATE_GOLDEN=1`; diff is
PR review surface, itemized in the PR body).

**Validation**: mutation rehearsal documented in `surface_test.go` (add exported
field to `core.Verdict` → FAIL; revert → PASS), following the T014/T028 precedent.

## 2. Facade Alias Set

The set of `type X = internal.Y` aliases in `mentat.go` (+ facade-owned types in
`run.go`).

- **Completeness invariant (new)**: every exported struct type in the transitive
  closure of exported field types reachable from `mentat.Config` and
  `mentat.Results` has a facade name.
- **Known addition**: `Completeness = config.Completeness` (`internal/config/config.go:124-136`);
  the implementation sweep may find more — each addition is a golden line and a
  composite literal in the compile test.
- **Relationships**: every member is frozen by Entity 1; constructibility is proven
  by the facade-only compile test (Entity 6 in [contracts/facade-nameability.md](./contracts/facade-nameability.md)).

## 3. Effective Contract (Resolved Config)

The fully resolved `config.Config` a run actually executes with — now defined to
be **path-independent** (YAML `config.Load` vs code-built struct literal).

- **Transition**: `raw` → (`config.Resolve`) → `resolved | error`. `Load` becomes
  `read + strict decode + Resolve`; `mentat.Run` applies `Resolve` before
  `BuildCorrelator`/`BuildStore`/`Build`.
- **Idempotency law**: `Resolve(resolved) = resolved` (the CLI paths re-enter
  `Resolve` via `mentat.Run` with an already-Load-resolved config).
- **Per-field semantics**: explicit-value-wins; full field inventory and rules in
  [contracts/config-resolve.md](./contracts/config-resolve.md) (13 Load behaviours:
  8 defaults, 4 hard errors, 2 normalize/compile steps).
- **Validation**: table-driven parity test — same logical config through both
  paths → identical effective contract, or identical descriptive error.

## 4. Seam Taxonomy

The single canonical list of extension seams, hosted in
`docs/extending/new-seam.md`, referenced from `internal/registry/registry.go:21-22`
and `specs/007-public-extension-api/contracts/public-surface.md:19` (the two
currently divergent sites).

Per-seam attributes (full table in [contracts/seam-taxonomy.md](./contracts/seam-taxonomy.md)):

| Attribute | Values |
|---|---|
| Registration style | instance (drivers, comparators, aggregate comparators, matchers, reporters) / factory (judges, stores) |
| Sealing | sealed at first engine build / never-sealed package-global (reporters only) |
| Public hook | `With*` option (driver, store, comparator, judge) / types-only (correlator, reporter) / **none — internal-only (aggregate comparators)** |

**Explicit exclusion**: `AggregateComparator` (`internal/core/core.go:110`) is
internal-only; its absence from the public seam set is documented in one explicit
sentence (currently documented nowhere — verified gap).

## 5. L3 Stability Lane

The scheduled pipeline enforcing 008 SC-001's 20-consecutive-run requirement.

| Field | Value |
|---|---|
| Workflow | `.github/workflows/nightly-l3.yml` (new; today `ci.yml` is the only workflow) |
| Triggers | `schedule` (nightly cron) + `workflow_dispatch` |
| Parameterization | job env `MENTAT_L3_RUNS: "20"` (consumed by `e2e/l3runs.go:19-30` → `e2e/completeness_meta_test.go:31`; unset default is 3, `l3runs.go:11`) |
| Steps | mirror `ci.yml` e2e job (`make labs`, compose up, `go test -tags e2e ./e2e/ -v -parallel 16`, teardown/logs) |
| Pass condition | suite green at 20 runs; at least one dispatched run green before the feature closes |
