---
name: new-comparator
description: >-
  Scaffold a new Mentat comparator (an assertion over a run's Evidence) the
  right way — Evidence-only, TDD, registered at the single composition root, with
  the mandatory error-path tests and L3 meta-test. Use this whenever the user
  wants to assert a NEW kind of trace/output behaviour: "add a comparator", "new
  comparator", "assert that the agent/service did X", "check tool calls / spans /
  budget / result / shape in a way we don't support yet", or when a .feature needs
  a `Then` phrase no existing comparator backs. Reach for it before hand-writing a
  comparator so the architecture invariants and coverage gate are satisfied by
  construction.
---

# Scaffolding a new comparator

A **comparator** answers one question about a single run: *did the system behave
the way this scenario expects?* It is the most-copied extension point in Mentat,
and the easiest place to accidentally break an architecture invariant. This skill
walks the create loop so the result passes review on the first try.

## The one invariant you cannot break

**Comparators consume `Evidence` only** — the `*trace.Trace` forest plus the driver
`Output`. They never touch a `TraceStore`, a `Driver`, a `Correlator`, or the
`config` layer. That is exactly what makes a comparator portable across an LLM
agent SUT and a microservice SUT. If your comparator needs something that isn't in
`Evidence`, stop: either it belongs upstream (correlation/store) or `Evidence`
needs a new field — surface that as a design question, don't reach around the seam.

Corollary invariants (all enforced by `go-reviewer`):
- **No silent fallbacks.** A missing required attribute, a nil `Trace`, an
  ambiguous match, or a wrong expectation type is a hard `error` naming the
  concrete thing that failed — never a zero-value `Verdict{}` pass. Crashes are data.
- **Multi-run assertions use the sibling interface.** If you need to assert across
  the N runs of an `@runs` scenario, implement `core.AggregateComparator`
  (`Aggregate(ctx, evs []Evidence, e)`), not `Comparator`. They coexist.

## Read these first (single source of truth — do not copy them stale)

Before writing anything, read the current shapes from the code. They evolve; this
skill deliberately does not duplicate their signatures.

| File | Why |
| --- | --- |
| `internal/core/core.go` | `Evidence`, `Verdict`, `Expectation`, the `Comparator` and `AggregateComparator` interfaces, `AggregateDetail`. |
| `internal/comparator/sequence.go` | The cleanest reference comparator — copy its *shape*: expectation struct → type-assert → nil-Trace guard → Evidence read → `Verdict`. |
| `internal/comparator/sequence_test.go` | The table-driven test shape and the fixture builders (`toolTrace`, `svcTrace`). Your test's error rows should mirror these. |
| `internal/engine/build.go` | The ONE composition root where comparators are registered. Your new comparator is wired here and nowhere else. |
| `internal/steps/steps.go` | How a `.feature` reaches a comparator: `w.check("<name>", comparator.FooExpectation{…})`. A new Expectation type needs a new step here (see below). |
| `internal/genai/keys.go` | The `gen_ai.*` span op/attr string constants (`genai.OpExecuteTool`, `genai.ToolName`, …) — use these, don't hardcode attribute strings. |
| `internal/trace/trace.go` | How you read a trace: `trace.Trace.ByOp(op)` selects spans by `gen_ai.operation.name`; `Span.Attr(k)` / `AttrInt` / `AttrFloat` read attributes. |

## This is a feature — it goes through TDD

Creating a comparator is a behaviour change, so **`go-test-writer` owns the
red→green→refactor loop** (`go-coder` refuses feature work). Either invoke
`go-test-writer`, or drive the loop yourself in this order — one failing test at a
time, confirming red before green:

1. **Lay the stub (red-able skeleton).** In `internal/comparator/<name>.go`:
   - a `FooExpectation` struct = the comparator-specific config a scenario supplies;
   - `type foo struct{}` (add fields only if it needs composition-root deps, e.g.
     `pricing core.Pricing` — see `NewBudgets`/`NewCEL`);
   - `func NewFoo(<deps>) core.Comparator { return foo{...} }` and `func (foo) Name() string { return "foo" }`;
   - `Compare` that type-asserts the expectation, then `return core.Verdict{}, fmt.Errorf("foo: not implemented")`.
2. **Write ONE failing table-driven test** in `internal/comparator/<name>_test.go`
   (columns: `name`, `ev`, `exp`, `wantPass`, `wantErr`). Start with the happy-path
   pass case. Run it — **confirm it goes red** for the right reason.
3. **Implement the minimum** to make that row green. Re-run. Confirm green.
4. **Add the next row, repeat.** Grow the table one behaviour at a time.
5. **Refactor** once green (extract helpers like `sequence.go`'s `toolSequence`).
6. **Register** at the composition root: add one line to `internal/engine/build.go`
   (`registry.RegisterComparator("foo", comparator.NewFoo(...))` — or
   `RegisterAggregateComparator` for the aggregate sibling).
7. **Verify coverage** stays ≥80% for the package with the `/coverage` skill.

## Mandatory test rows (the no-silent-fallback contract)

A comparator PR is blocked without these — they are the difference between a real
assertion and one that silently passes on bad data:

- `Name()` returns the registered string.
- **Happy path passes** (`wantPass: true`).
- **Violation fails** (`wantPass: false`) — and assert the failure `Reasons` name
  the concrete thing, since that text is what a user reads in the report.
- **Malformed Evidence errors** — e.g. a required span attribute is missing. If your
  comparator inspects only a *subset* of spans (filters by tool/service/op), a
  required attribute missing on ANY candidate span is still a hard error: you can't
  prove a nameless span isn't one you're counting. Reuse the existing scan
  (`toolSequence`/`serviceSequence`) instead of re-implementing it and silently
  undercounting.
- **`ev.Trace == nil` errors** (unless the comparator legitimately reads only
  `Output`; then test the `Output`-missing path instead).
- **Wrong expectation type errors** — cover `string`, `int`, and `nil` like
  `sequence_test.go` does; this is the `e.(FooExpectation)` guard's contract.

Prefer `t.Parallel()` (top + each `t.Run`) since these share no mutable state —
it surfaces the ordering bugs that matter in a trace framework. Skip it only if a
row uses `t.Setenv`/`t.Chdir`. **Omit the loopvar capture (`tt := tt`)** — the
existing comparator tests still carry it, but it's unnecessary since Go 1.22 and
CLAUDE.md forbids it (this module is on 1.25); copy the table's structure, not that line.

## Wire it into BDD — a new Expectation type always needs a step

Registration (above) makes the comparator reachable by the *engine API*, but a
`.feature` can only invoke it through a step in `internal/steps/steps.go`. Every
`Then` reaches a comparator via `w.check("<name>", comparator.FooExpectation{…})`
(see the `sequence`/`result` steps). A brand-new `FooExpectation` has no phrase
constructing it, so **without a new step your comparator is dead code from the
scenario layer** — registered, unit-tested, and unreachable from any `.feature`.

Add the step: parse the Gherkin row/args into a `FooExpectation` and call
`w.check("foo", exp)`. Keep it thin — it translates words to an `Expectation`; all
judgement lives in `Compare`. (Skip this only if you are deliberately reusing an
existing phrase that already builds your expectation — rare for a new comparator.)

**Naming gotcha:** `.golangci.yml` enables the `predeclared` linter, which fails on
identifiers that shadow Go 1.21+ builtins — including `max` and `min`, the obvious
names for a ceiling/floor comparator's field or step param. Use `n`, `limit`, or
`ceiling` instead (the existing step handlers all use `n`/`ms`/`code`). This bites
in both `Compare` and the `steps.go` handler.

## The L3 meta-test (mandatory for a new assertion)

A test framework must prove it goes **red** on bad behaviour. If this comparator
introduces a new assertion, add both:
- `features/meta/<name>.feature` — a scenario that violates exactly this one
  assertion (see the existing `features/meta/*.feature`);
- a `{feature, reason}` row in the relevant `e2e/*_meta_test.go` table, where
  `reason` is a substring of the failure message `Compare` emits. This is an
  `//go:build e2e` test — see the `new-e2e-scenario` skill for the exec/parallel rules.

This step is **infra-gated**: `//go:build e2e` needs `make harness-up` (live Tempo),
so it can't be closed hermetically in the same pass as the unit-tested comparator.
If the harness isn't available, land the comparator + unit tests + step wiring, and
track the meta-test as an explicit follow-up rather than claiming done.

## Definition of done

- `gofmt -l .` clean, `go vet ./...` / `golangci-lint run` clean.
- New comparator reads `Evidence` only; every not-can-do path returns a wrapped,
  descriptive `error`.
- Table-driven test with all mandatory rows; package coverage ≥80%.
- Registered on exactly one line in `internal/engine/build.go`.
- Wired into `internal/steps/steps.go` so a `.feature` can actually invoke it — a
  new Expectation type ⇒ a new step, or the comparator is unreachable from BDD.
- L3 meta-test proves the assertion trips (if a new assertion was introduced), or
  is tracked as a follow-up when the e2e harness isn't available.
- Conventional Commit (`feat(comparator): …`), files staged individually, no AI
  attribution.
