# Research: Custom-Comparator Gherkin Invocation (011)

**Date**: 2026-09-10 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

All findings verified against the tree at `1206a56` (011 branch point). Every claim below
cites the file and line it was read from; nothing here is inferred.

---

## R1 — The step pattern, and whether it can collide

**Decision**: `^the "([^"]+)" comparator is satisfied by:$`

**Finding**: There is **no collision risk**. All 39 registered patterns are fully anchored
(`^…$`), and every existing pattern beginning with `the ` requires a literal keyword
immediately after it:

| Existing prefix | Keyword after `the ` |
|---|---|
| `^the (?:agent\|service) target "…"$` | `agent` / `service` |
| `^the agent calls tools in order:$` | `agent` |
| `^the tool "…" is never called$` | `tool` |
| `^the services are called in order:$` | `services` |
| `^the service "…" is never called$` | `service` |
| `^the result …` (7 patterns) | `result` |
| `^the response …` (3 patterns) | `response` |
| `^the run satisfies…` / `^the runs satisfy…` / `^the run matches shape …` | `run` / `runs` |

The proposed pattern places a **quote** immediately after `the `, which no existing pattern
accepts. Godog reports ambiguous matches as errors, so this is worth stating rather than
assuming — but the analysis is exhaustive over the full pattern list, not a spot check.

**Rationale for the phrasing**: deliberately plain. Option B (012) is where natural syntax
gets solved; this phrase exists to be unambiguous and to read as obviously generic, so nobody
mistakes it for the final ergonomics.

**Alternatives considered**: `^comparator "…" is satisfied by:$` (reads worse in a `Then`
chain and breaks the "steps start with an article or pronoun" habit of the existing grammar);
`^the "…" comparator passes:$` ("passes" collides conceptually with the verdict vocabulary,
where pass/fail is the *outcome* rather than the invocation).

---

## R2 — Handler signature and docstring binding

**Decision**: `func (w *world) comparatorSatisfiedByDoc(name string, doc *godog.DocString) error`

**Finding**: Six handlers already take a docstring, and the established convention is that
regex captures come first and the docstring last —
`func (w *world) resultMeansDoc(doc *godog.DocString) error` (`steps.go:510`),
`func (w *world) resultToolDoc(slot, tool, verb string, doc *godog.DocString) error`
(`steps.go:433`), `runSatisfiesDoc` (`:558`), `runsSatisfiesDoc` (`:569`),
`resultAttrDoc` (`:474`), `sendRequestBodyDoc` (`:168`).

The docstring body is read as `doc.Content`, unmodified — `steps.go:562` passes it straight
into `comparator.CELExpectation{Expr: doc.Content}`. This feature does the same and **must
not** trim or normalize, since whitespace may be significant to a comparator's private
format (spec Edge Cases).

---

## R3 — Where the new row goes in `stepDefs`

**Decision**: a new group, `Extend`, appended after the existing five.

**Finding**: `stepDefs` currently holds 35 rows across five groups — `Drive` (5),
`Sequence` (5), `Budgets` (4), `Result` (12), `Shape` (9). `docs_test.go` enforces that rows
sharing a group are **contiguous**, and `metadata_test.go` enforces that no group, summary or
example is blank.

**Rationale**: the new step belongs to none of the five — it is not about driving, sequence,
budgets, results or shape; it is about reaching *outside* the built-in grammar. A sixth group
keeps the contiguity invariant trivially satisfied (one row, appended at the end) and gives
`mentat steps` and `docs/steps.md` a section that reads as what it is: the extension escape
hatch. Folding it into `Result` would misfile it and force a decision about where inside a
12-row block it lands.

---

## R4 — `Engine.Comparators()` accessor

**Decision**: add `func (e *Engine) Comparators() []string { return e.reg.Comparators() }`

**Finding**: `Registry.Comparators()` already exists (`registry.go:125`) and returns the
sorted registered names. `Engine` exposes the reporter equivalent —
`func (e *Engine) Reporters() []string { return e.reg.Reporters() }` (`engine.go:218`) — but
not the comparator one. The addition is three lines mirroring an existing accessor exactly,
and exists solely to satisfy FR-007's "list the registered names" obligation.

**Not needed**: `Engine.Comparator(name) (core.Comparator, bool)` already exists
(`engine.go:200`), and `world.eng` is a concrete `*engine.Engine` (`steps.go:34`). The handler
can resolve the instance directly. **This feature adds no other engine plumbing.**

---

## R5 — Registering a custom comparator inside a test

**Decision**: `engine.WithExtraComparator(name, factory)`.

**Finding**: `engine.Build(cfg, st, cor, opts ...Option)` (`build.go:30`) takes variadic
options, and `WithExtraComparator(name string, f func(config.Config) (core.Comparator, error))`
already exists (`options.go:91`) alongside the driver, reporter, judge, store and matcher
equivalents. So an in-process test can build a real engine carrying a test-only comparator
with no new machinery.

This is what makes R6 possible.

---

## R6 — Where the red-on-bad proof belongs *(revises the spec's FR-015 placement)*

**Decision**: the L3 red proof lands in **`internal/steps`, as an in-process godog suite** —
not in `e2e/`.

**Finding — the spec's assumption was wrong in a way that matters.** FR-015 as written
assumed the proof goes in the `e2e/` L3 suite. That suite cannot host it:
`e2e/main_test.go:29` builds `mentatBin` with `go build -o … ./cmd/mentat`, and
`e2e/meta_test.go` drives that prebuilt binary with feature files. A comparator registered in
Go via `WithComparator` **is not in that binary**, so the CLI-level meta-suite structurally
cannot reach one. Proving red there would require building a second, test-only binary.

**The better vehicle already exists.** `internal/steps/steps_test.go:103`,
`TestFeatureGoesRedOnBadScenario`, runs a full godog suite **in process**: it builds a real
engine via `engine.Build` against a gomock `TraceStore`, passes `Initializer(eng)` as the
scenario initializer, supplies the feature as inline `FeatureContents` (no file on disk), and
asserts `suite.Run() != 0` plus a substring of the failure reason. Its own comment names it
"the hermetic unit-level complement to the binary-level L3 meta-test".
`semantic_meta_test.go`, `qualifier_test.go`, `judge_ledger_test.go` and
`filestore_replay_test.go` use the same shape — five precedents, not one.

**Why this is an improvement rather than a compromise**:

1. It is **hermetic** — gomock store, no network, no Tempo (Constitution V).
2. It runs under `make ci`, so the proof actually gates. The `e2e/` placement would have put
   this feature's red-on-bad proof behind `//go:build e2e`, which `make ci` never compiles —
   the exact hole that left the e2e package unbuildable for six commits during 010.
3. Combined with R5, the test can register its own failing comparator in three lines.

**Consequence for the spec**: FR-015's *obligation* is unchanged and still mandatory — prove
red on a failing comparator and on a parser error. Only its *location* moves, from `e2e/` to
`internal/steps`. SC-010's `go vet -tags e2e ./...` requirement stays, because the e2e package
must still compile even though it gains no new test here.

---

## R7 — Public-surface golden effect

**Decision**: exactly **two** lines added.

**Finding**: the golden renders an aliased seam interface as an alias line plus one
`method (…)` line per method, with parameter types written in **facade** names:

```text
method (Comparator) Compare(ctx context.Context, ev Evidence, e Expectation) (Verdict, error)
type Comparator = core.Comparator
```

(`specs/007-public-extension-api/contracts/public-surface.golden:173,193`)

So `ExpectationParser` adds precisely:

```text
method (ExpectationParser) ParseExpectation(text string) (Expectation, error)
type ExpectationParser = core.ExpectationParser
```

A predictable two-line diff makes the golden update reviewable at a glance, and makes SC-002's
"exactly one step row" claim checkable by the same discipline.

**Sweep consequence**: `Expectation` appears in a published seam's method set, so 010's
widened reachable-set rule requires it to be nameable. It already is —
`type Expectation = core.Expectation` is on the facade. No second alias is pulled in, because
`Expectation = any` is a terminal.

---

## R8 — Why `ExpectationParser` cannot be declared at the facade

**Decision**: declare in `internal/core`, alias as `mentat.ExpectationParser`.

**Finding**: 010's D5 rule — only *terminal* types may be facade-declared. `internal/steps`
consumes `ExpectationParser` (the handler type-asserts against it), and root imports
`internal/steps` (`run.go`). Declaring the interface in package `mentat` would make
`internal/steps` import root, which is the import cycle D5 exists to prevent.

`internal/core` is the correct home: it already declares `Comparator` and `Expectation`, the
two types this interface sits beside, and `core` imports nothing that would cycle.

---

## Summary of what this research changed

| Spec assumption | Research finding | Effect |
|---|---|---|
| L3 proof goes in `e2e/` (FR-015) | `e2e/` execs a prebuilt `cmd/mentat` binary that cannot contain a Go-registered comparator; `internal/steps` has five in-process precedents | **Placement moved**; obligation unchanged, and the proof now runs under `make ci` |
| Engine plumbing might be needed | `Engine.Comparator(name)` exists; `world.eng` is concrete | Only `Engine.Comparators()` is added, mirroring `Reporters()` |
| Group placement unstated | Five groups exist, contiguity enforced | New `Extend` group, appended |
| Golden effect unstated | Interface aliases render as alias + method lines in facade names | Exactly 2 lines, predictable |

No `NEEDS CLARIFICATION` items remain.
