# Research: Extension-Surface Integrity (009)

**Date**: 2026-07-18 | **Input evidence**: `prompts/spec-009-extension-surface-integrity.md`
(gathered at `b1aabb4`), re-verified at HEAD `2f4073d` by go-context-builder sweep.

Every file:line below was re-verified at `2f4073d`. The only commit in
`b1aabb4..HEAD` is `2f4073d` (doc-comment rewrites, stability.md interim-gap
section, this feature's prompt) — no code-semantics drift in any referenced file.

## R0 — Evidence re-verification summary

| # | Prompt claim | Status at 2f4073d |
|---|--------------|-------------------|
| 1 | Golden renders struct aliases bare; `Verdict.Qualifiers` / `Target.Completeness` absent | VERIFIED — `surface_test.go:265-276` expands only `*ast.InterfaceType` (`lookupInterface` :296-309 returns nil otherwise); golden line 76 is bare `type Verdict = core.Verdict`; grep confirms no `Qualifiers` line; fields at `internal/core/core.go:57`, `internal/config/config.go:115` |
| 2 | `config.Completeness` reachable-unnameable | VERIFIED — `config.go:115,124-136`; absent from `mentat.go`'s alias list (full list re-catalogued, 30+ aliases) |
| 3 | `mentat.Run` bypasses Load resolution | VERIFIED — `run.go` never calls `config.Load`; `engine.go:76-89` reads raw `Mode`/`Settle`; comment `engine.go:73-74` ("kind-defaulted at config load") is false on the code path |
| 4 | Two divergent six-seams lists; reporters never-sealed; AggregateComparator undocumented | VERIFIED — `internal/registry/registry.go:21-22` (drivers, comparators, aggregate comparators, matchers, judges, stores) vs `specs/007-public-extension-api/contracts/public-surface.md:19` (driver, store, comparator, judge + correlator/reporter types-only); reporters `registry.go:177-201`; `AggregateComparator` at `core.go:110`, zero mentions in `docs/extending/` |
| 5 | No workflow enforces `MENTAT_L3_RUNS=20` | VERIFIED, sharper than claimed — **no workflow sets the env at all**; the per-PR 3 is the code default (`e2e/l3runs.go:11`); `l3runs.go:8-10` documents a nightly lane that does not exist; `ci.yml` is the only workflow (push-main + PR); no e2e make target — CI runs `go test -tags e2e ./e2e/ -v -parallel 16` directly (`ci.yml:116`) |
| 6 | stability.md weakened | VERIFIED — interim-gap section `stability.md:53-84` explicitly defers the strong claim to "spec 009" |

New facts surfaced by the sweep (not in the prompt):

- **Three composition calls, not one**: `mentat.Run` calls `engine.BuildCorrelator`
  (`run.go:293`), `engine.BuildStore` (`run.go:310`), then `engine.Build`
  (`run.go:349`). Load-equivalent resolution placed inside `engine.Build` alone would
  leave the first two reading unresolved `Poll`/`Tempo`/`Store` config.
- **Idempotency trap**: `resolveCompleteness` (`config.go:380-405`) keys off
  `SettleRaw == ""` — re-running it on a config where the user set `Settle`
  directly (code-path idiom) would overwrite `Settle` with the kind default.
- **Live consumer on the divergent path**: `examples/kafkaecho/main_test.go:23`
  builds `mentat.Config{…}` in code and calls `mentat.Run` — any "hard error on
  code-built configs" story would break the shipped example.
- **Two additional divergence suspects** beyond the prompt's three:
  zero `Target.Budget` semantics in `Drive` on the code path, and `validateJudge`'s
  Load-only temperature/votes-pairing + `MaxCostUSD` rules (`config.go:312-324`) —
  `engine.Build` re-checks votes only (`build.go:83-104`).

## R1 — Surface gate: struct-field freezing for aliased types

**Decision**: Extend the existing stdlib-AST renderer in `surface_test.go` with a
struct index parallel to the interface index: `indexStructs` beside
`indexInterfaces` (`surface_test.go:313-346`), a `lookupStruct` beside
`lookupInterface`, and a `renderStructFields` beside `renderInterfaceMethods`
(`surface_test.go:356-380`). For every alias whose RHS resolves to an
`*ast.StructType`, render the exported fields one per line (name + type as written
in the aliased source, `go/printer` mode 0), skipping unexported fields
(e.g. `ExtractConfig.compiled`). Embedded fields render as written (the embedded
type name is itself a frozen line). Aliases of maps, funcs, and `any`
(`Pricing`, `Expectation`, the `run.go` factory func types) keep their current
single-line rendering — their full shape is already the declaration text.
Regenerate the golden exactly once via `MENTAT_UPDATE_GOLDEN=1`
(`mentat_golden_test.go:28`, `surface_test.go:107`); the ~25-struct churn is the
deliberate, PR-called-out review surface. Acceptance is a documented mutation
rehearsal following the T014/T028 precedent (`surface_test.go:42-52`, `54-70`):
add an exported field to `core.Verdict` → FAIL naming the type; revert → PASS.
Restore the strong claim in `docs/extending/stability.md` (drop the interim-gap
section :53-84) in the same change that makes it true.

**Rationale**: The dir-resolution and caching machinery (`surfaceCtx`
`surface_test.go:152-163`, `addImports` :231-249, module-path resolution :214-227)
is fully reusable — the delta is one index + one renderer, staying inside the
established stdlib-only constraint. Field-type text "as written" is stable against
formatting noise (printer-normalized) and catches adds, removals, and re-types,
which is exactly FR-001. Expanding struct fields also closes the "arg-type alias
blindness" case stability.md names (:76-78): a struct passed as a func-type arg
drifts via its own expanded alias entry.

**Alternatives considered**:
- `go/types`-based export-data walk (`golang.org/x/tools/go/packages`) — precise,
  but violates the repo's deliberate stdlib-only constraint for this test and adds
  a heavyweight dependency for information the AST already carries.
- Freezing via `go doc` / `gopls` output diffing — output format not a stable
  contract, breaks on toolchain upgrades.
- Rendering unexported fields too — rejected: they are not part of the public
  surface promise and would freeze internals (e.g. `ExtractConfig.compiled`),
  causing false churn.

## R2 — FR-008 story choice: (a) path-independent resolution in `mentat.Run`

**Decision**: Story **(a)** — extract the post-unmarshal resolution/validation
half of `config.Load` (`config.go:198-298`) into an exported-to-the-module
`config.Resolve(*Config) error` such that `Load = read file + strict decode +
Resolve`, and call `Resolve` at the top of `mentat.Run` **before**
`BuildCorrelator`/`BuildStore`/`Build`. `Resolve` must be idempotent
(Load-then-Run double-resolution is the CLI path: `cmd/mentat/main.go:74`,
`cmd/mentatctl/main.go:310`) with **explicit-value-wins** per-field semantics —
a default applies only when the field is at its zero value; a raw-string field
(`SettleRaw`) wins over its resolved twin (`Settle`) only when the raw is
non-empty. Full per-field rules in [contracts/config-resolve.md](./contracts/config-resolve.md);
the audited inventory `Resolve` must cover (everything Load does that a literal
never gets, `config.go:198-298`):

| Load behaviour | Lines | Kind |
|---|---|---|
| Store default `"tempo"` | 212-214 | default |
| file-store storePath required | 218-220 | hard error |
| Expectations default | 221-223 | default |
| `Poll.SearchLimit` default 100 | 226-228 | default |
| kill-grace + suite timeout → `c.Budget` | 233-241 | default |
| per-target concurrency defaults | 243-252 | default |
| http url/method required + trimmed | 253-264 | error + normalize |
| `t.Budget` resolution | 265-269 | default |
| extract validate + regexp compile (`compiled`) | 270-274 | error + compile |
| completeness kind-defaults (2s/5s settle) | 275-279, 380-405, 193-196 | default |
| `validatePricing` | 282 | hard error |
| judge backend/model/votes defaults | 285-293 | default |
| `validateJudge` (temperature pairing, `MaxCostUSD`) | 294, 312-324 | hard error |

**Rationale**: (a) over (b) on four grounds. (1) Precedent — the engine already
re-applies Load-only validation for exactly this reason (judge votes,
`build.go:58-65,83-104`; `MaxConcurrency` re-default `build.go:141-144`); (a)
completes that precedent instead of contradicting it. (2) A live shipped consumer
(`examples/kafkaecho`) already builds Config in code — story (b)'s hard error or
mandatory extra call is a breaking change to a published example for zero user
benefit. (3) Constitution IV asks that the *same* rules apply loudly everywhere,
not that one path be forbidden: `Resolve` carries Load's existing hard errors
(storePath, http url, pricing, judge) to the code path, which (b) would merely
re-implement behind a new API. (4) One resolution call in `mentat.Run` covers all
three composition calls; (b) would need the same coverage plus new public surface.
Placement in `mentat.Run` (not `engine.Build`) because `BuildCorrelator`/`BuildStore`
read config first — and the CLI paths stay correct because resolution is idempotent.

**Alternatives considered**:
- **(b) exported resolution func + hard error on unresolved fields** — rejected:
  breaks `examples/kafkaecho`, adds public API this spec is trying not to grow,
  and turns an invisible divergence into a mandatory ceremony rather than removing it.
- **Resolution inside `engine.Build` only** — rejected: `BuildCorrelator`/`BuildStore`
  (`run.go:293,310`) would still read unresolved `Poll`/`Store`/`Tempo` values.
- **Silent lazy defaulting at each read site** (e.g. `completenessContract`
  defaulting inline) — rejected: that is a scattered silent fallback, the exact
  anti-pattern Constitution IV names; it also leaves validation (hard errors)
  unapplied on the code path.

## R3 — Nameability: facade alias + compile-level sweep

**Decision**: Alias `config.Completeness` on the facade
(`type Completeness = config.Completeness` in `mentat.go`), then sweep every
exported struct type reachable from `mentat.Config` and `mentat.Results` (walk
exported field types transitively) and alias any other reachable-unnameable type
found. Enforce with a compile-level test extending `mentat_external_test.go`
(facade-only imports, precedent :63-68): one composite literal per reachable
exported struct, each populating at least one field, so any future
reachable-unnameable type (or field whose *own type* is un-aliased internal —
edge case from the spec) fails compilation. `examples/kafkaecho` stays the
external-module proof that the facade is sufficient (its `go.mod` `replace`
makes internal imports compiler-forbidden; `Makefile:32` polices `examples/`).

**Rationale**: The compile test is the cheapest machine-enforcement that matches
the failure mode exactly — "cannot write the literal" is precisely what breaks
external authors. Doing the sweep once manually during implementation is
acceptable because the test then freezes the property forever (any new reachable
struct must appear in the literal test to be constructible, and the surface golden
(R1) makes the reachable set reviewable).

**Alternatives considered**:
- Reflection-based runtime walk asserting every reachable type has a facade name —
  rejected: reflection cannot see aliases (they are compile-time only), so it
  cannot distinguish `mentat.Completeness` from `config.Completeness`.
- A generator emitting aliases automatically — rejected: premature abstraction;
  one realized gap does not justify codegen, and auto-aliasing would silently
  grow the public surface (the golden should force each addition to be deliberate).

## R4 — Seam-addition guide + canonical taxonomy

**Decision**: Create `docs/extending/new-seam.md` as the **single canonical
taxonomy** plus the seam-addition checklist. Taxonomy content: the six seams
with, per seam, registration style (instance: drivers, comparators, aggregate
comparators, matchers, reporters — `registry.go:74,102,126,139,189`; factory:
judges, stores — `registry.go:152,165`), sealing behaviour (reporters as the
never-sealed package-global exception, `registry.go:177-201`), and public hooks
(driver/store/comparator/judge have `With*` options; correlator/reporter
types-only until three real demands exist — the 007 rule). One explicit sentence
documents `AggregateComparator` (`core.go:110`, built-in `"aggregate-cel"`
registered at `build.go:51`) as internal-only, excluded from the public seam set.
Checklist: the ~10 touchpoints (core interface + mocks regen, registry map +
methods + seal handling, engine options funnel, build wiring incl.
collision-check-before-construction ordering (`build.go:179-216`), `run.go`
factory type + `With*` option, `mentat.go` alias(es), golden regen,
`public-surface.md` justification, CHANGELOG, docs/extending page, tests incl.
mutation rehearsal). The three tribal decisions get a short "how to choose"
paragraph each. Both existing sites then *reference* the canonical list:
`registry.go:21-22` doc comment and `public-surface.md:19`.

**Rationale**: The two current lists disagree because each describes a different
axis (what the registry owns vs what has public hooks) — the reconciliation is to
name both axes in one table rather than pick a winner, which is why the canonical
home must be a doc page (code comments can't hold a table usefully) with both old
sites pointing at it.

**Alternatives considered**:
- Making `public-surface.md` the canonical home — rejected: it is a 007 contract
  artifact (historical record of that feature's decisions); an evergreen
  contributor guide belongs in `docs/extending/` next to `stability.md`.
- Generating the taxonomy from registry method signatures — rejected: premature
  abstraction, and the interesting content (the three decisions, the exclusion
  sentence) is judgement, not signature data.

## R5 — Nightly L3 lane

**Decision**: New workflow `.github/workflows/nightly-l3.yml`: triggers
`schedule` (nightly cron, e.g. `0 3 * * *`) + `workflow_dispatch`; one job that
checks out, sets up Go, builds the harness images (`make labs`), boots the stack
(`docker compose -f deploy/docker-compose.yml up -d --wait` — same steps as
`ci.yml`'s e2e job :96-98), then runs `go test -tags e2e ./e2e/ -v -parallel 16`
with `MENTAT_L3_RUNS: "20"` in the job env. Mirror `ci.yml`'s teardown/log-dump
steps. This makes the `e2e/l3runs.go:8-10` comment ("the release/nightly lane
sets MENTAT_L3_RUNS=20") true instead of aspirational. Acceptance: file exists
with both triggers, and one manually dispatched run has gone green.

**Rationale**: Reusing `ci.yml`'s proven e2e job steps verbatim minimizes new
failure modes; env-var injection is exactly the parameterization `l3runs.go`
(:19-30) was built for. Nightly-on-default-branch means failures land on the
Actions page where CI failures are already watched (spec edge case: no silent
failure channel).

**Alternatives considered**:
- Raising the per-PR default to 20 — rejected: ~7× e2e wall-clock on every PR for
  a stability property that only needs daily proof.
- A `make nightly-e2e` target wrapping the run — deferred: there is currently no
  e2e make target at all (CI invokes `go test` directly); introducing one only for
  the nightly lane would create a second divergent invocation path. Follow the
  existing CI idiom instead.
- Reusable workflow / matrix over runs — rejected: ~10 lines of YAML is the
  budget; a matrix of 20 jobs would multiply harness boots, not runs.

## Resolved unknowns

- **FR-008 story** → (a), per R2 (the spec explicitly delegated this to planning).
- **Additional divergence suspects** (zero `Target.Budget` in `Drive`; full
  `validateJudge` rules) → in scope of the R2 inventory table; the parity test
  table (FR-009) must include cases for both, so implementation confirms or
  refutes them with evidence rather than assumption.
- **Non-struct alias rendering policy** (maps/funcs/`any`) → unchanged single-line
  rendering, per R1 (their declaration text is already their full shape).
- **Golden churn size** → ~25 aliased structs; accepted as one deliberate regen,
  itemized in the PR body per the repo's golden-change protocol (stability.md
  three acts).

No NEEDS CLARIFICATION markers remain anywhere in spec or plan.
