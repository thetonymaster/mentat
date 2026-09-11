# Adding a new seam

A **seam** is an interface Mentat composes at its one composition root
(`engine.Build`). Drivers, stores, comparators and judges are seams; so are the
internal matcher and aggregate-comparator strategies. This page is the *canonical*
description of which seams exist, how each is registered, and what a contributor
must touch to add one.

It is canonical in a literal sense: two earlier lists (the `internal/registry`
doc comment and the 007 public-surface manifest) described *different* sets of
"six seams" because they were measuring different axes, and neither said so. Both
now point here. If you are about to write a seam list somewhere else, link this
page instead.

## The two axes

Every seam sits on two independent axes, and conflating them is what produced the
divergence:

- **Registry ownership** — is the seam stored in the per-engine
  `*registry.Registry`, resolvable by name at run time? Six are.
- **Public hook** — is the seam reachable from the `github.com/thetonymaster/mentat`
  facade, either as an aliased type or as a `With*` registration option? Four have
  registration options; two more are aliased as types only.

A seam can be registry-owned and invisible to the public surface (matchers), or
publicly aliased and absent from the registry (correlator). Neither list is a
subset of the other.

## The canonical taxonomy

| Seam | Registry ownership | Registration style | Sealing | Public hook |
|------|--------------------|--------------------|---------|-------------|
| Driver | yes — `Registry.RegisterDriver` ([`registry.go:74`](../../internal/registry/registry.go)) | instance | sealed at first build | `WithDriver` (factory-typed option) |
| Store | yes — `Registry.RegisterStore` (`registry.go:165`) | **factory** (stateful) | sealed | `WithStore` |
| Comparator | yes — `Registry.RegisterComparator` (`registry.go:102`) | instance | sealed | `WithComparator` |
| Judge | yes — `Registry.RegisterJudge` (`registry.go:152`) | **factory** (stateful) | sealed | `WithJudge` |
| Matcher | yes — `Registry.RegisterMatcher` (`registry.go:139`) | instance | sealed | none — **internal-only**: no facade alias and no `With*` option; the only registration path is `engine.WithExtraMatcher` (`internal/engine/options.go:110`), an internal hook |
| Aggregate comparator | yes — `Registry.RegisterAggregateComparator` (`registry.go:126`) | instance | sealed | none — **internal-only** (see the exclusion below) |
| Correlator | no — an `engine.Build` parameter, never a registry entry | — | — | types-only (`mentat.Correlator`); no registration hook until three real external demands exist (the 007 rule) |
| Reporter | yes — `Registry.RegisterReporter` | instance | sealed | `WithReporter` |

"Sealed" means `engine.Build` calls `reg.Seal()` once wiring completes, after which
any `Register*` panics loudly (FR-009). Registration is only representable inside
the composition root; there is no post-build registration path.

## `AggregateComparator` is internal-only

`AggregateComparator` (`internal/core/core.go:110`; built-in `"aggregate-cel"` wired
at `internal/engine/build.go:51`) is an internal seam — it is deliberately not
aliased on the facade and has no `With*` option; it is excluded from the public seam
set until three real external demands exist.

The same reasoning applies to `Matcher`: it is registry-owned but unexported from the
facade, because a matcher is a strategy *inside* the result comparator rather than an
independent extension point. Widening the surface later is easy; narrowing it is a
breaking change ([stability policy](stability.md)).

## How to choose: the three decisions

### Instance vs factory registration

Register a **factory** when the extension is stateful per engine — it holds
endpoints, HTTP clients, credentials, or anything derived from `Config`. Stores and
judges are factories for exactly this reason (`StoreFactory` and `JudgeFactory` in
`registry.go`): each composed engine needs its own instance built from *its* config,
not a shared one. Register an **instance** when the extension is stateless or safe to
share across concurrent scenarios — drivers, comparators, matchers and reporters all
qualify, because they carry no per-run mutable state and the registry's `RWMutex`
makes concurrent reads safe.

There is a facade nuance worth internalizing before you copy the pattern: the public
`With*` options are **factory-typed even for instance seams**. `WithDriver` and
`WithComparator` take a `func(Config) (T, error)`, and `applyExtras` constructs the
value and then registers the resulting *instance* (`internal/engine/build.go:185-208`).
That is deliberate — it gives an external author access to the resolved `Config` at
construction time and gives Mentat a place to reject a nil seam loudly — but it means
"factory-typed option" and "factory registration" are not the same statement. Read the
`Registration style` column, not the option signature.

### Per-engine vs package-global

**Per-engine is the rule.** `engine.Build` constructs a fresh `registry.New()` per
call, registers every seam into it, and seals it, so two `Run`s never share seam
state: a sequential run cannot leak a custom registration into the next one, and
concurrent runs cannot race a shared map. This is the 007 reentrancy fix (US2,
T010/T011), and it is why `Registry` is a type with methods rather than a package of
globals.

**There are no exceptions.** Reporters used to be one, and the story is worth keeping
because it shows how an exception outlives its reason. They were package-global under
their own `reporterMu` and never sealed, justified by: *reporters are a post-run
rendering concern; `cmd/mentat` calls `report.EmitReports` after `Run` returns, so it
holds results, not an `Engine`, and has no registry to consult.*

That was true when written. The 007 recompose then moved emission **inside** `Run`
(`run.go`), where the engine is still in scope — and the justification quietly became
false while the global stayed. It went unnoticed until feature 010 needed a per-run
`WithReporter` and found that registering into shared state is exactly the reentrancy
defect T010/T011 had closed for every other seam: two concurrent `Run`s would race the
same map, then silently use each other's reporters, with no seal to catch it.

Reporters are now per-engine and sealed like everything else. If you believe your new
seam needs to be global, the lesson is not that there is a category of seam that does —
it is that a structural exception must be re-checked whenever the structure moves. If it
is consumed *during* a run, it belongs in the per-engine registry, no exceptions.

### A seam's parameter types cannot be declared at the facade

Discovered in feature 010 (D5) and non-obvious enough to be worth a checklist line.
The `mentat` package imports `internal/report`, `internal/engine` and
`internal/registry`. Any type those packages must **consume** therefore cannot be
declared in `mentat` — it is an import cycle. That is why every public type on the
facade is an *alias* to an internal declaration.

Facade-declared structs are legal only for **terminal** types: produced at the facade
and never passed back down. `Results` was one, until it became a seam parameter and
stopped being one. When you add a seam, declare its parameter and result types in an
internal package and alias them upward.

### Collision check before construction

A colliding registration must **never run its factory**. In `applyExtras` the
existence check precedes the `ed.factory(cfg)` call for every seam
(`internal/engine/build.go:179-216`), so a caller who registers a name that a built-in
or an earlier registration already holds sees the *collision* error — naming the seam
and the conflicting name — and not some incidental error thrown by a factory that
should never have been invoked. (In a duplicate-name pair the first entry is not
colliding and is still built.)

This ordering is a 007 regression precedent, not an aesthetic preference: inverting it
produces a confusing error that describes a symptom instead of the cause, and it lets a
doomed factory perform side effects (open a connection, spawn a process) that are then
never cleaned up. When you wire a new seam into `applyExtras`, copy the check-then-build
order verbatim and add a test that a colliding registration's factory is not called.

## The new-seam checklist

Adding a seam touches roughly ten places. Work them in order — each depends on the
one before.

1. **Core interface** — declare it in `internal/core`, small and consumer-defined, then
   regenerate mocks with `go generate ./...` (the `//go:generate mockgen` directive
   lives next to the interfaces). Commit the generated mocks; never hand-edit them.
2. **Registry** — add the map to `Registry`, initialize it in `New`, and add
   `Register<Seam>` (routed through `register` so seal handling is inherited) plus the
   lookup method. Decide instance vs factory here.
3. **Engine options funnel** — add the `extra<Seam>s` slice and the internal
   `engine.With<Seam>` option in `internal/engine/options.go`.
4. **Build wiring** — register built-ins in `engine.Build`, and extend `applyExtras`
   with the nil-factory check, the **collision check before construction**, the
   nil-seam check, and the registration.
5. **Facade factory type + option** — add `<Seam>Factory` and `With<Seam>` in `run.go`
   *only if* the seam is a public extension point; skip for internal-only seams.
6. **Facade alias** — add `type <Seam> = core.<Seam>` (and any contract types its
   method signatures name) in `mentat.go`, with a doc comment stating whether it has a
   registration hook.
7. **Golden regeneration** — `MENTAT_UPDATE_GOLDEN=1 go test -run TestPublicSurfaceGolden`,
   and itemize the resulting diff in the PR description. The diff is the acknowledgment
   act, not a formality.
8. **Public-surface justification** — add the symbol with its justification to
   `specs/007-public-extension-api/contracts/public-surface.md` and its data-model
   inventory. The rule is: a symbol appears there with a justification, or it does not
   get exported.
9. **CHANGELOG** — an entry under `Added` / `Changed` / `Removed` naming the symbol,
   plus a migration note if anything breaks.
10. **This directory** — a `docs/extending/<seam>.md` guide in the voice of the existing
    ones (contract, implementer obligations tied to the constitution, registration
    example, walkthrough), and add it to the "See also" lists.
11. **Tests** — unit tests to the 80% floor, a mutation rehearsal proving the gate goes
    red when the seam misbehaves, and an `//go:build e2e` scenario if the seam is
    externally observable.

Update this page's taxonomy table in the same PR. A seam that exists in code but not in
the table above has reintroduced exactly the drift this page was written to end.

## Optional capabilities are not seams

Some extension points are not registered at all. `ExpectationParser`,
`PhraseContributor` and `CaptureParser` are **optional capabilities** an
already-registered comparator may additionally implement, discovered by type assertion
rather than by registration.

They belong in neither the taxonomy table nor this checklist, and the reason is
structural rather than editorial: every column above — registry ownership, registration
style, sealing, public hook — is undefined for something that is never registered. An
optional capability owns no registry entry, is never sealed, and has no `With*` option.

The checklist that *does* apply to one is shorter:

1. Declare the interface in `internal/core` and alias it on the facade (010's D5 forbids
   declaring it at the facade when an internal package consumes it).
2. Discover it by type assertion at the composition root — never by a registration call,
   or it stops being optional.
3. A comparator that does not implement it must keep working unchanged, and a test must
   assert that implementing one capability does not imply another.
4. Add a compile-time witness in an EXTERNAL test package
   (`var _ mentat.Thing = (*yourStub)(nil)`). This is the guard that matters: the
   nameability sweep is seeded from existing aliases and cannot notice a missing one,
   so only an external package failing to build proves the type is truly nameable.
5. Expect the public-surface golden to record the interface and its full method set.

## See also

- [Contributing Gherkin phrases from a comparator](phrases.md) — a worked optional capability
- [Stability policy](stability.md) — what changing the public surface obliges you to do
- [Writing a custom Driver](driver.md)
- [Writing a custom TraceStore](store.md)
- [Writing a custom Comparator](comparator.md)
- [Writing a custom Judge](judge.md)
- [The Evidence a comparator inspects](evidence.md)
