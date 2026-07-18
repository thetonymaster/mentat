# Spec 009 prompt — extension-surface integrity

Input for `/speckit-specify`. Feature name suggestion: `009-extension-surface-integrity`.
Evidence gathered 2026-07-18 at commit `b1aabb4` (five-agent audit); re-verify
file:line references during planning — code may have moved.

## Feature description (pass this to speckit-specify)

Make every guarantee of the public extension surface machine-enforced before any
new seam is added. Today the surface gate, the facade type set, and the library
Run path each have a verified hole that lets public-API drift or YAML-vs-code
divergence through silently. Close all five:

**1. Surface gate: struct-field freezing for aliased types.**
`surface_test.go` (008 T028) expands method sets for *interface* aliases but
renders struct/map aliases as bare lines (`type Verdict = core.Verdict`, golden
line 76). Realized drift: feature 008 added `core.Verdict.Qualifiers`
(`internal/core/core.go:57`) and `config.Target.Completeness`
(`internal/config/config.go:115`) with **zero golden churn**, while
`docs/extending/stability.md` claims struct fields are frozen. Extend the golden
renderer to expand exported field sets of aliased structs (same stdlib-AST
technique as T028; no `go/types`/`x/tools`). Acceptance = mutation rehearsal,
documented in the test like `surface_test.go:54-69`: temporarily add an exported
field to `core.Verdict` → `TestPublicSurface` must FAIL; revert → PASS. Regen
golden once via `MENTAT_UPDATE_GOLDEN=1` to capture current fields; restore
stability.md's strong claim (weakened by the Bucket-1 chore PR).

**2. Reachable-but-unnameable types.**
`mentat.Target.Completeness` has type `config.Completeness`
(`internal/config/config.go:115,124-136`) but `mentat.go` aliases only
`CompletenessContract` — an external module cannot write a composite literal for
it (internal imports are compiler-forbidden and Makefile-policed, `Makefile:32`).
Alias `config.Completeness` on the facade, then sweep the whole public surface
for other reachable-unnameable types. Acceptance: a facade-only compile test
(extend `mentat_external_test.go` or `examples/kafkaecho`) that constructs a
composite literal for every exported struct type reachable from `Config`/`Results`
— it must compile using only the `mentat` package.

**3. Code-built Config parity with config.Load.**
`mentat.Run` never calls `config.Load`, so struct-literal Configs silently skip
Load-only resolution. Verified divergence: completeness kind-defaults (2s/5s
settle, `resolveCompleteness`, `internal/config/config.go:275`) never apply —
`engine.completenessContract` reads raw `Mode`/`Settle`
(`internal/engine/engine.go:84-88`), yielding settle=0/mode="" on the library
path. Precedent exists: the engine re-validates judge votes for exactly this
reason (`internal/engine/build.go:60-66`). Decide and implement ONE story:
either (a) engine.Build applies all Load-equivalent resolution/defaulting
(completeness, and audit `ExtractConfig.compiled` — Load-populated at
`config.go:149,155-157`, unusable from code — and `Target.Budget`), or (b) the
facade documents loudly that code-built Configs must go through an exported
resolution function, and calling Run without it on affected fields is a hard,
descriptive error. No silent divergence between the YAML path and the code path
(Constitution IV). Acceptance: table-driven parity test — same logical config via
YAML `config.Load` vs struct literal → identical effective contract, or a loud
error naming the unresolved field.

**4. Seam-addition checklist + taxonomy reconciliation.**
No "new seam" guide exists; adding one touches ~8-10 places (core interface +
mocks regen, registry map+methods, engine options funnel, build wiring, run.go
factory+option, mentat.go aliases, golden regen, `public-surface.md`
justification, CHANGELOG, docs/extending page, tests). Three decisions are
tribal: instance-vs-factory registration (drivers/comparators/matchers are
instances; stores/judges factories), per-engine vs package-global (reporters are
the never-sealed exception, `internal/registry/registry.go:177-183`), and
collision-check-before-construction ordering (`internal/engine/build.go:180-193`).
Also: `registry.go:21-22` and
`specs/007-public-extension-api/contracts/public-surface.md:19` define two
different "six seams" lists, and `AggregateComparator`'s internal-only status is
documented nowhere. Deliverable: `docs/extending/new-seam.md` (checklist with the
three decisions explained), one reconciled taxonomy referenced from both places,
and an explicit sentence documenting AggregateComparator's exclusion.

**5. Nightly L3 lane.**
008 SC-001 requires `MENTAT_L3_RUNS=20` machine-enforced; no workflow sets it —
`.github/workflows/ci.yml` pins 3 runs per PR; the 20-run proof was one manual
run recorded in T032's PR description. Add a scheduled workflow (~10 lines,
nightly cron) that boots the harness and runs the e2e L3 suite with
`MENTAT_L3_RUNS=20`. Acceptance: workflow file exists, triggers on schedule +
manual dispatch, and a dispatched run has gone green once.

## Constraints

- Items 1-3 are behaviour changes → TDD via go-test-writer, one failing test at
  a time. Items 4-5 are docs/CI → go-coder.
- Architecture invariants from ./CLAUDE.md hold (Evidence-only comparators,
  single composition root, no silent fallbacks, tag-first correlation).
- Surface golden changes must be deliberate: every golden diff in this feature is
  itself part of the review surface — call each one out in the PR body.
- Coverage floor 80%/package stays enforced; run the coverage skill before PR.

## Out of scope

Custom-comparator Gherkin invocation (spec 010), mentatctl/CLI UX (spec 011),
any new seam itself — this spec is the precondition for those.
