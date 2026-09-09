# Contract: The `Reporter` seam — one interface, per-run registration

**Consumers**: external extension authors; the three built-in reporters
(`internal/report`); `mentat.Run`'s emission path.
**Fulfils**: FR-003, FR-004, FR-011, FR-012, FR-014, FR-015; SC-001, SC-003, SC-008, SC-010.
**Decisions**: spec D1–D4. **Research**: [research.md R2/R3](../research.md).

## The seam

Declared in `internal/result` and aliased as `mentat.Reporter` — **not** declared at the
facade. Root imports `internal/report`, `internal/engine` and `internal/registry`, so a type
those packages consume cannot live in package `mentat` without an import cycle (D5,
[research R8](../research.md)).

```go
// package result

// Reporter renders a completed run. Implementations receive the same Results a
// library caller receives from Run — there is no richer internal input.
type Reporter interface {
	Report(res Results, w io.Writer) error
}
```

```go
// package mentat
type Reporter = result.Reporter
```

**One interface.** The built-in json/html/junit reporters implement *this* type, not a
privileged internal variant (FR-012). Under D5 that holds **by construction**: `Results` is
the collapsed former `RunReport`, so there is only one type to render and no second shape a
built-in could be given.

**Breaking change.** The previous signature took `core.RunReport`, a name that ceases to
exist. It carries all three acts: regenerated golden, `CHANGELOG` entry, migration note
(FR-009).

## Input completeness

`Results` MUST carry everything any built-in renders (FR-011). At `f2afdda` the facade's
`Results` did not — eight data points were reachable by built-ins and not by an external
author.

**D5 satisfies this by construction rather than by parity.** `Results` *is* the type the
built-ins already render; there is no mirror to keep in step and no field that can be present
on one side and missing on the other. The invariant to test is therefore not a field
inventory:

> The three built-in reporters compile against the published seam, unmodified in what they
> read.

If a built-in needs a datum the published `Results` does not carry, it will not compile. That
is the check (SC-008). Rendering equivalence, not field counting.

## Registration

```go
type ReporterFactory func(Config) (Reporter, error)
func WithReporter(name string, f ReporterFactory) Option
```

Mirrors `WithComparator` (`run.go:201`) exactly — same factory shape, same per-`Run`
scoping, same collision discipline.

**Selection is unchanged.** `WithReports(map[string]string)` still maps a reporter *name*
to an output path. A custom reporter is chosen the way a built-in one is: by naming it
there. No second selection mechanism is introduced.

```go
mentat.Run(ctx, cfg,
	mentat.WithReporter("dashboard", newDashboardReporter),
	mentat.WithReports(map[string]string{"dashboard": "out/dash.json"}),
)
```

### Collision

A name already registered — built-in or custom — fails **loudly at build time**, naming the
conflicting name. Never last-wins, never silent replacement (Constitution IV). This matches
`WithDriver`'s existing behaviour; it is stated because reporters previously had *no*
collision check at all: `RegisterReporter` (`registry.go:196`) overwrote silently.

### Scope — the reentrancy requirement

Registration MUST be per-`Run` and MUST NOT mutate package-global state (FR-014).

Reporters move onto the per-engine `*Registry` alongside the other five seams, sealed by
`engine.Build` like the rest. The package-global map (`registry.go:190-193`) and its
rationale comment are **deleted** — the comment justifies the global by a call path
(`cmd/mentat` emitting after `Run` returns) that has not existed since the 007 recompose;
emission is at `run.go:457`, inside `Run` (FR-015).

**`EmitReports` must be given the registry.** It resolves reporters through the package-global
today (`emit.go:36`), which the move deletes. Its replacement takes the resolved reporter set
(or the `*Registry`) alongside the `Results`; the call site at `run.go:457` holds both, so no
new plumbing is needed — but the signature MUST be stated in the implementation, not left for
the implementer to invent. This is the same seam as the registry move and is settled with it.

**Test obligation**: two concurrent `Run`s registering *different* reporters under the
*same* name both succeed, each using its own (SC-010). A `t.Parallel()` table test is not
sufficient on its own — this needs genuinely concurrent `Run` calls, run under `-race`.

## Error behaviour

| Situation | Required behaviour |
|---|---|
| Reporter returns an error while rendering | Surfaced with the reporter **named**; the suite's own `Results` are still returned. A rendering failure is not a suite failure. |
| Two registrations share a name | Loud build error naming the collision. |
| `WithReports` names an unregistered reporter | Loud error naming the unknown name and, ideally, the registered ones. Never a silently skipped file. |
| Reporter panics | Out of scope — library code does not defend against caller panics, per the constitution's stance on `panic`. |

The first row preserves today's behaviour at `run.go:455-459`, where an emit failure is
captured rather than early-returned so it cannot mask a simultaneous budget trip. That
`errors.Join` composition MUST survive the re-shape.

## What this contract does not do

- It does not add a `Correlator` registration hook. `Correlator` is fully nameable and
  therefore implementable; it has no nameability defect, and its "no concrete demand"
  rationale stands untouched (spec Out of Scope).
- It does not change what the built-in reporters emit. See
  [report-format-golden.md](./report-format-golden.md) — that is a hard constraint, not an
  aspiration.
- It does not publish `AggregateComparator`, the main consumer of `AggregateDetail`. That
  is a missing-symbol gap, a different defect class.
