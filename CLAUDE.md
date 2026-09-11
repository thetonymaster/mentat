# Mentat — repo guide for Claude Code

Read this once at the start of any task in this repo. It anchors the conventions
the `go-*` subagents enforce. The global `~/.claude/CLAUDE.md` (epistemics, batch
size, failure protocol) still applies and takes precedence where they overlap.

## What this is

**Mentat** is a trace-based behaviour test framework: write Gherkin specs, drive a
system-under-test (AI/LLM agents or microservices), fetch its OpenTelemetry trace
from Tempo, and run **comparators** that assert how it behaved and what it produced.

- Module: `github.com/thetonymaster/mentat`
- Design: `docs/superpowers/specs/2026-06-16-trace-behaviour-test-framework-design.md`
  and `…-2026-06-17-mentat-test-harness-design.md`
- Plans: `docs/superpowers/plans/2026-06-17-{tracelab-harness,mentat-framework-core,mentat-integration-cli}-v1.md`
- Architecture diagram: `docs/architecture/mentat-architecture.html`

## Architecture invariants (do not violate)

1. **Comparators consume `Evidence` only** (`Trace` forest + driver `Output`). They
   never touch a `TraceStore` or `Driver`. This is what keeps them portable across
   agents and microservices.
2. **`Trace` is a forest** — a run may span ≥1 root trace (multi-turn / sub-agent).
   Never assume a single root. Correlation resolves by `test.run.id` and merges.
3. **Every seam is an interface, wired at one composition root** (`engine.Build`)
   via per-seam registries. Manual DI — **no `wire`/`fx`**. The engine depends on
   interfaces, never concrete `Tempo`/`shell`/`Claude`.
4. **No silent fallbacks.** A function that cannot do its job returns an `error`
   (wrapped with `%w`), never a zero-value success or a guessed result. Trace
   not-found, ambiguous match, missing required attribute → hard, descriptive error.
   Crashes are data.
5. **Correlation is tag-first.** Inject `test.run.id` per run (resource attribute via
   `OTEL_RESOURCE_ATTRIBUTES` for spawned agents; baggage for http/grpc); resolve by
   querying that tag.

## Go conventions

- **Format & vet:** `gofmt -l .` clean and `go vet ./...` clean before any commit.
  Run `golangci-lint run` if a `.golangci.yml` exists.
- **Errors:** wrap with `fmt.Errorf("doing X: %w", err)`; messages name the concrete
  thing that failed and the value involved (`"port: expected int, got %q"`), not
  `"invalid input"`.
- **Interfaces small, defined by the consumer.** Keep files focused (one clear
  responsibility); split when a file grows unwieldy.
- **No `panic` in library code** except true, caller-unreachable invariants.

## Testing rules (enforced by go-test-writer and gated by go-reviewer)

- **TDD** for all features/bugfixes: red → green → refactor, one failing test at a
  time. (go-test-writer owns this; go-coder refuses feature work.)
- **Table-driven tests** are the default shape:
  ```go
  tests := []struct {
      name string
      // inputs
      // want / wantErr
  }{ ... }
  for _, tt := range tests {
      t.Run(tt.name, func(t *testing.T) { ... })
  }
  ```
  (No `tt := tt` capture — unnecessary since Go 1.22; this module is on 1.25.)
- **`t.Parallel()` — soft default, not a gate.** Prefer `t.Parallel()` in new
  table-driven tests (at the top, and inside each `t.Run`) **when the test shares no
  mutable state**. It surfaces ordering / shared-state / data-race bugs, which matter
  for a trace-correlation framework. For unit tests it is *not* a CI-speed measure
  (execution is seconds; CI cost is compilation) and *not* required. **Exception:**
  `//go:build e2e` tests are I/O-bound (each blocks on Tempo trace-ingestion), so
  there `t.Parallel()` *is* a real ~7× wall-clock win — see the Hermetic bullet.
  **Skip it** for tests using `t.Setenv` / `t.Chdir` — those *panic* under `t.Parallel()`.
- **Mocks: uber gomock** (`go.uber.org/mock/gomock` + `mockgen`). Generate mocks for
  the `core` interfaces (`Driver`, `TraceStore`, `Correlator`, `Comparator`,
  `Reporter`, `Judge`) rather than hand-rolling fakes:
  - Install: `go install go.uber.org/mock/mockgen@latest`
  - Declare next to the interfaces (e.g. in `internal/core/core.go`):
    `//go:generate mockgen -source=core.go -destination=mocks/mock_core.go -package=mocks`
    (mockgen paths are relative to the file's package dir)
  - Regenerate with `go generate ./...`; commit generated mocks.
  - Use `gomock.NewController(t)`, set `.EXPECT()` expectations, assert via the
    controller's automatic `t.Cleanup` finish.
  - Trivial value stubs (a struct returning a fixed trace) are acceptable only when
    no call-count/argument verification is needed; prefer gomock when behavior matters.
- **Coverage floor: 80%** per package. Check with the `coverage` skill
  (`.claude/skills/coverage`) or:
  `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`.
  A PR that drops a package below 80% is blocked.
- **BDD layer:** behaviour specs use `godog`; step defs live in `internal/steps`.
  The L3 meta-test (drive bad scenarios, assert Mentat fails) is mandatory — a test
  framework must prove it goes red on bad behaviour.
- **Hermetic by default:** unit tests use the `inmem`/`otlp-file` store + gomock; no
  network. Live-Tempo tests are `//go:build e2e` and need `make harness-up`. New e2e
  scenarios exec the prebuilt `mentatBin` (built once in `e2e/main_test.go`'s
  `TestMain`), **not** `go run ./cmd/mentat`, and call `t.Parallel()` (top + each
  `t.Run`) so the suite overlaps the per-scenario trace-ingestion waits.

## Git

- Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`, `chore:`).
- `git add .` is forbidden — add files individually.
- **No AI attribution** in commits or PRs (no "Generated with…", no `Co-Authored-By`).

## Routing (which subagent for what)

| Task | Agent |
| --- | --- |
| New feature / bugfix (behaviour change) | **go-test-writer** (TDD) |
| Scaffolding, config, `deploy/`, dep bumps, mocks regen, behavior-preserving refactor | **go-coder** |
| Pre-commit audit or mid-task scan | **go-reviewer** (`gate` / `pair`) |
| Explore + brief before planning | **go-context-builder** |

## Skills

- `/traces` — query the local Tempo (the `deploy/` stack) by `test.run.id`, render
  `gen_ai.*` span forests and tool-call sequences. See `.claude/skills/traces`.
- `/coverage` — run `go test` with coverage and enforce the 80% floor.

## Feature history and standing rules

> These notes are **curated, not generated**. They sit above the `<!-- SPECKIT -->`
> markers deliberately: the agent-context hook REPLACES everything between those
> markers with a three-line pointer to the current plan, so anything kept inside them
> is destroyed the next time that hook runs. Add feature retrospectives here, never
> below the marker.

Features 001–011 are shipped. **011-comparator-gherkin-invocation**
(`specs/011-comparator-gherkin-invocation`, implemented 2026-09-10, all 37 tasks
complete) is **merged to `main`** as `ec4efbc` (#39). Both plan
invariants held: the public-surface golden changed **exactly once, by exactly two
lines**, and no `stepDefs` drift-test assertion was edited anywhere in the branch —
the operational test of D1's claim that this was Option A and not Option B.

What landed: `core.ExpectationParser` (aliased on the facade), one generic `stepDefs`
row in a new **seventh** group `Extend` (39 rows/6 groups → 40/7),
`comparatorSatisfiedByDoc` in `internal/steps/steps.go`, and `Engine.Comparators()`.

Four corrections this feature made to its own artifacts — worth knowing before
trusting the spec text:

- **godog is non-strict by default**, so an UNDEFINED step is reported and the SUITE
  STATUS is still 0. The first reading of this — that a mistyped `Then` step therefore
  passes silently in a real run — was **wrong**, and the correction is worth keeping:
  `mentat.Run` discards the suite status (`_ = suite.Run()`, `run.go`) and derives
  Results from the collector, and godog passes the After hook
  `step is undefined: <text>` as stepErr, so the scenario is recorded as FAILED. The
  collector path is what makes `mentat.Run` correct, not what breaks it.
  The gap WAS real in exactly one place — `ctl.ReplayFeature`, which derived its
  verdict from the suite status directly, so `mentatctl replay` reported success on a
  feature whose assertion never ran. Fixed with `Strict: true` there and covered by
  `TestReplayFeatureFailsOnUndefinedStep`;
  `TestUndefinedStepFailsTheRun` guards the `mentat.Run` side, since that behaviour is
  emergent from godog's hook contract rather than asserted anywhere in mentat.
- **The embedded-quote edge case cannot happen.** The spec and
  `contracts/step-grammar.md` said a name containing a quote is *truncated* at the
  quote. The pattern is anchored at both ends and `([^"]+)` cannot cross a quote, so
  such a line matches nothing at all. Pinned by
  `TestCustomComparatorPatternRejectsEmbeddedQuote`.
- **`Registry.Comparators()` did not sort** while its `Reporters()` sibling did, and
  had zero non-test callers. 011 is its first caller, and it feeds an error message,
  so the sort was added at the source.
- **`tasks.md` T009's red was not achievable as written** (assert `suite.Run() == 0`
  and expect an undefined-step failure — see the non-strict point above), and T015
  tested a guard T010 mandated, so it could never have been observed failing. Both
  were reordered so every test was seen red first.

Read the spec's **Decisions** (D1–D5) first — D1 in particular, which narrows the
feature to one generic step row plus an optional `ExpectationParser` seam and defers
comparator-contributed Gherkin phrases to 012. Three findings shrank this feature
below how 009 framed it, all in `research.md`:

- `type Expectation = any` is **not** the blocker. Six comparators already build
  typed expectations from feature-file text; only the *choice* of concrete type is
  frozen at compile time.
- No new engine plumbing. `Engine.Comparator(name)` exists and `world.eng` is a
  concrete `*engine.Engine`; the sole addition is `Engine.Comparators()`.
- **R6 corrected the spec**: the L3 red-on-bad proof belongs in `internal/steps` as
  an in-process godog suite, *not* in `e2e/` — that suite drives a prebuilt
  `cmd/mentat` binary which cannot contain a Go-registered comparator, and sits
  behind a build tag `make ci` never compiles.

The mutation rehearsals are recorded in the test files themselves
(`surface_test.go` for the nameability probe, `internal/steps/custom_comparator_test.go`
for the four step-level ones), including one that initially failed to go red because
the *mutation* had not applied — "the mutation didn't fire" and "the guard is real"
are indistinguishable from test output alone.

**Roadmap, renumbered by 011's D1:** 012 is comparator-contributed Gherkin phrases
(Option B, a superset of 011 — nothing 011 builds is discarded); CLI/`mentatctl` UX
moves from 012 to 013. The 009 roadmap line
(`specs/009-extension-surface-integrity/spec.md:143`) was corrected on 2026-09-10,
along with its `:65` sibling that cited the range `010–012`.

**Renumbered again 2026-09-11, by 012's convergence:** **013 is built-in step-pattern
disjointness**, and CLI/`mentatctl` UX moves from 013 to **014**. See the roadmap entry
after 012 below for why 013 exists.

**012-comparator-gherkin-phrases** (`specs/012-comparator-gherkin-phrases`, implemented
2026-09-11, all 69 tasks complete) is on branch `012-comparator-gherkin-phrases`.

What landed: two OPTIONAL seams in `internal/core` — `PhraseContributor` (a comparator
declares the Gherkin sentences that invoke it) and `CaptureParser` (its captures become
the comparator's own Expectation) — plus `ContributedPhrase`, `Engine.ContributedPhrases`,
a `reflect.MakeFunc` handler bridge in `internal/steps/phrase.go`, `mentat.Validate`, and
`Strict: true` on `mentat.Run`'s godog options. Convergence added two finding classes:
`step-argument` (Phase 9) and `ambiguous-step` (Phase 10) — see the godog notes below, since
both are consequences of measured godog behaviour rather than design choices.

The headline guarantee is **equivalence**: a contributed phrase and 011's generic step
produce identical pass/fail, identical reason text, identical qualifiers and an identical
expectation value (`custom_phrase_facade_test.go`). 011's path is provably untouched — the
`Extend` row and `comparatorSatisfiedByDoc` are byte-identical, and the only edit to its
test file is one mechanical `mustInit(...)` wrap.

Four corrections this feature made to its own artifacts — the same shape as 011's, and
worth reading before trusting the spec text:

- **R10: the ambiguity defect is LATENT, not live.** R1's first draft claimed it was
  "reachable between two built-in patterns today". Measured false — the 40 built-in
  patterns are **pairwise disjoint** across 1530 generated sentences covering every
  alternation branch, and registration is single-pathed. Contributed phrases are what
  make it reachable, so `Strict` is a prerequisite of 012 rather than a separable bugfix.
  Pinned by `TestBuiltinStepPatternsArePairwiseDisjoint`, which nothing asserted before.
- **R11: the nameability sweep does NOT demand the new aliases.** R2 claimed 010 would
  pay for itself here. Measured false — `TestFacadeNameabilitySweep` is SEEDED from the
  aliases that already exist, so removing one removes its seed and nothing fires; and both
  new seams are optional, so no published type references them. SC-008 still holds, via
  the public-surface golden plus the external-package compile-time witnesses — the same
  pair 011 used. **A gate's coverage is a property to measure, not to infer from its name.**
- **`StepBindingFindings` had ONE non-test caller, not two** (`cmd/mentat/validate.go`).
  Which is also why its `sync.Once` never bit: its only consumer was a single-shot CLI
  process.
- **Both stdout goldens captured godog's step-definition SOURCE LINE**
  (`# metadata.go:75 -> *world`), so documenting `registerSteps` churned them with no
  change to rendered output. Both normalizers now collapse the line number and keep the
  filename. The e2e one was caught only by running `go test -tags e2e` — `make ci` does
  not compile that lane.

Three design decisions worth knowing before extending this:

- `registerSteps` takes its phrases as a **parameter**, not from `w.eng`. The drift test
  drives it with a zero `world` whose `eng` is nil; nil-guarding would be the silent
  fallback that makes a drift test assert over an empty set while believing otherwise.
- The drift gate became a **partition** (every registered pattern is either a `stepDefs`
  row or a member of the supplied contributed set, counts adding up) rather than being
  relaxed into an unchecked superset.
- Validation lives in `internal/steps`, not `engine.Build`: `internal/engine` cannot import
  `internal/steps`, and V4 (collision with a built-in) is a statement about `stepDefs`.
  It runs at composition, so no scenario executes when a phrase is malformed.

`docs/extending/phrases.md` is the authoring guide. `mentat steps` and `docs/steps.md`
remain **built-in only** — a compiled binary cannot see a consumer's registrations, which
is structural; use `mentat.Validate` and the engine-scoped renderer from your own test
binary.

Read `research.md` before touching anything godog-related. Everything below was
**measured** against the pinned `godog v0.15.1`, and every one of these corrected a claim
someone had previously asserted without testing:

- **godog reports an ambiguous match only under `Strict`** (`suite.go:547-553`), and
  `mentat.Run` did not set it (`run.go:411-419`). Measured: two patterns matching one
  step resolve **silently to the first-registered one and the scenario PASSES** — the
  After hook receives a nil `stepErr`. 011's D1 asserted the opposite. Enabling `Strict`
  surfaces it through the existing After-hook path as a FAILED scenario naming every
  matching expression — no new mechanism needed. The branch was **latent, not live**, at
  `0f9dcea` (see R10 above); built-ins register first, so the phrase a contributed
  collision would silently swallow is always the contributed one.
- **godog SILENTLY DISCARDS any step argument the handler did not declare** — the
  conversion loop runs `i < numIn`, so a step carrying a docstring **or a data table**,
  matched by a phrase that takes none, runs on its captures alone and the scenario
  reports **PASSED** with the argument never read. **Now closed for BOTH sources** by
  `StepArguments` (`internal/steps/stepargs.go`) at scenario init, in `mentat.Validate`
  and in the `mentat validate` binary, with an unrecognised argument kind rejected by
  default so the next argument type godog adds is refused rather than silently joining
  the list of things that vanish.

  It took **three** attempts, each aimed at an instance rather than the mechanism, and
  review found the same hole one step over every time: docstrings on phrases → data
  tables on phrases (one struct field away) → **all 40 built-in steps** (no comparator
  needed to reach it at all). The built-in half is the one worth remembering: it was
  pre-existing on `main`, reachable by anyone who types a docstring under the wrong step,
  and it survived two rounds of fixing *this exact defect* because each fix was scoped to
  where the defect had been found rather than to what caused it.

  The expected argument is **derived by reflection from each `stepDefs` row's handler
  signature**, never listed beside it — a hand-kept list would be a second source of truth
  for the thing `stepDefs` exists to be the only source of, and it would drift silently
  back into the same unearned green. Pinned end-to-end by
  `TestBuiltinStepWithSurplusArgumentMakesTheRunRed`, rehearsed against the pre-fix state
  (`stepArgs := StepArguments{}` → suite status 0, "1 scenarios (1 passed)").

  Two review rounds on the FIX found the same defect class inside it, twice: the comment
  claiming godog delivers an argument only into `*godog.DocString`/`*godog.Table`
  (**false** — a plain `string` parameter receives it too, so a drifting handler would
  false-RED every scenario using its step; now an arity invariant, `checkBuiltinArity`),
  and a message promising a silent pass in cases that actually fail loudly. Chasing the
  second exposed a real panic: `toolsInOrder`/`servicesInOrder` dereferenced a typed-nil
  `*godog.Table`, unlike all nine docstring handlers. Both now nil-guard.
- **Enabling `Strict` churns zero goldens**, measured on both surfaces (`go test ./...`
  and `go test -tags e2e` with the harness up). `make ci` does not compile the e2e lane,
  so it is not evidence for this on its own.
- godog accepts no `[]string` or variadic step handler (`internal/models/stepdef.go:222-233`)
  and **silently discards surplus captures** (`stepdef.go:58`), so contributed phrases need
  a `reflect.MakeFunc` bridge with arity derived from the pattern's `NumSubexp()`.
- **godog's ambiguity check reduces to a per-sentence multi-match, so the static check is the
  SAME predicate and not an approximation** (R15). `matchStepTextAndType`
  (`suite.go:511-556`) returns `ErrAmbiguous` under `Strict` when more than one registered
  expression matches, and its `keywordMatches` filter (`suite.go:558-560`) is **inert for every
  Mentat step** — `ScenarioContext.Step` registers with `formatters.None`
  (`test_context.go:255-257`) and `registerSteps` uses `reg.Step` for both halves
  (`metadata.go:107,110`). Had any step registered via `Given`/`When`/`Then`, a static check
  ignoring the keyword would false-RED valid files; a repo-wide grep confirms none does.

  This closed the last Validate/Run asymmetry: `mentat.Validate` used to report **CLEAN** on a
  step `Run` refuses as ambiguous, i.e. a validator certifying a suite the runner rejects —
  the drift D7 exists to remove. `StepBindingFindings` (`internal/steps/precheck.go`) now
  classifies every step by how many patterns match: `0` → `unbound-step`, `1` → clean, `>1` →
  **`ambiguous-step`** naming every match in registration order. One function answers all three
  because it is one question; a separate ambiguity check is how the two would drift apart again.

  Reachable because pattern validation rejects only **identical** patterns, so two
  distinct-but-overlapping contributed phrases coexist legally. It reaches suites through
  `mentat.Validate`; from the binary it is unreachable **as measured, not structurally** — the
  disjointness test substitutes nine fixed fillers, so it is evidence, not proof.

  **Neither Mentat nor godog computes regex overlap.** Both classify per sentence, so a
  collision on a sentence the corpus does not contain is reported by nobody.

All five are properties of the **pinned** `godog v0.15.1`; a bump re-opens them.

**Roadmap after 012:**

- **013 — built-in step-pattern disjointness: prove it, or stop relying on it.** 012's
  `ambiguous-step` class is unreachable from the `mentat validate` binary, and that is now
  written down in three places — `contracts/validate-surface.md` §4, `CHANGELOG.md`, and the
  doc comment on `StepBindingFindings` — as **measured, not structural**. The measurement is
  `TestBuiltinStepPatternsArePairwiseDisjoint` (`internal/steps/metadata_test.go:286`), which
  generates sentences from each pattern's own syntax tree, expanding every alternation branch
  — but substitutes just **nine fixed fillers** (`"x"`, `""`, `"a b"`, `"1"`, `"2nd"`,
  `"true"`, `"0.5"`, `"tool-name"`, `"a/b.c"`) into capture groups and character classes. Two
  built-ins colliding only on a string no filler produces would pass it.

  So "the 40 built-in patterns are pairwise disjoint" is **evidence, not proof**, and four
  things rest on it: V4's rationale (rejecting a contributed pattern identical to a built-in's
  is only meaningful if the built-in set is itself unambiguous); R10's "latent, not live"
  finding; the binary's inability to report `ambiguous-step`; and `StepArguments.matchBuiltin`
  (`internal/steps/stepargs.go:305`) returning the FIRST match as though it were the only one.

  The overclaim was caught in review during 012's Phase 10: a draft of `validate-surface.md`
  said "structurally unreachable" while its own next clause conceded the test "proves only over
  generated sentences". It was corrected to "unreachable **as measured**", which is honest and
  leaves the gap open — hence this feature.

  Three routes, to weigh rather than assume: decide regex intersection for the built-in set
  (RE2 makes emptiness-of-intersection decidable in principle; Go's stdlib exposes nothing for
  it); widen the generator (more fillers is more evidence, never proof); or **stop relying on
  the property** — have `matchBuiltin` report every match and route it through the
  `ambiguous-step` path that already exists. Only the third removes the assumption instead of
  strengthening it.

- **014 — CLI/`mentatctl` UX.** Renumbered from 013 on 2026-09-11 by the entry above.

Two standing rules 010 established — read these before touching the facade:

- **Only terminal types may be facade-declared.** Root imports `internal/report`,
  `internal/engine` and `internal/registry`, so any type those packages *consume*
  must live beneath them and be aliased on the facade, never declared there. This
  is why the result types (`Results`, `ScenarioResult`, `RunRecord`, `Reporter`)
  live in the leaf package `internal/result`. See `specs/010-.../spec.md` D5.
- **The reachable set includes seam signatures.** `TestFacadeNameabilitySweep`
  (`surface_test.go`) walks parameter and result types of every published seam, not
  just fields reachable from `Config`/`Results`. `docs/extending/stability.md`
  boundary 4 is closed; boundaries 1–3 remain accepted gaps.

For additional context about technologies used, project structure, shell commands,
and other important information, read `specs/` as history — each feature dir carries
its `spec.md`, `plan.md`, `tasks.md`, and `contracts/`. When work is in flight, the
current plan is the `plan.md` of the highest-numbered spec dir whose `tasks.md`
still has unchecked tasks.

<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan
at specs/013-builtin-pattern-disjointness/plan.md
<!-- SPECKIT END -->
