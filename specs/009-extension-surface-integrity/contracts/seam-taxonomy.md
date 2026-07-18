# Contract: Canonical seam taxonomy + new-seam checklist

**Consumers**: contributors adding seams; readers of
`internal/registry/registry.go` and
`specs/007-public-extension-api/contracts/public-surface.md` (the two currently
divergent sites); specs 010/011 (this feature is their precondition).
**Fulfils**: FR-011..FR-012, SC-004. Decision: [research.md R4](../research.md).

## Canonical home

`docs/extending/new-seam.md` (new). Both existing sites are edited to *reference*
it: the `registry.go:21-22` doc comment and `public-surface.md:19`. After this
feature, exactly one list exists.

## Reconciled taxonomy (the two old lists described different axes; the canonical
table names both)

| Seam | Registry ownership | Registration style | Sealing | Public hook |
|------|--------------------|--------------------|---------|-------------|
| Driver | yes (`RegisterDriver` :74) | instance | sealed at first build | `WithDriver` (factory-typed option) |
| Store | yes (`RegisterStore` :165) | **factory** (stateful) | sealed | `WithStore` |
| Comparator | yes (`RegisterComparator` :102) | instance | sealed | `WithComparator` |
| Judge | yes (`RegisterJudge` :152) | **factory** (stateful) | sealed | `WithJudge` |
| Matcher | yes (`RegisterMatcher` :139) | instance | sealed | none — **internal-only**: no facade alias and no `With*` option; sole registration path is `engine.WithExtraMatcher` (`internal/engine/options.go:110`), an internal hook |
| Aggregate comparator | yes (`RegisterAggregateComparator` :126) | instance | sealed | **none — internal-only (see exclusion)** |
| Correlator | no (engine seam only) | — | — | none — types-only until three real demands (007 rule) |
| Reporter | package-global `registry.RegisterReporter` (:189) — a package-level func, NOT a `*Registry` method; reporters live in no per-engine registry | instance | **never sealed** (package-global, own mutex, `registry.go:177-201`) | types-only |

> **Amended 2026-07-18 during T020/T021.** Two cells above were corrected against
> the source after this contract was written: the Matcher public-hook cell said
> "types-only", which is false — "types-only" means *aliased with no registration
> hook* (correlator, reporter), and Matcher has neither a facade alias nor a
> `With*` option (verified: no `Matcher` in `mentat.go`, zero hits in
> `public-surface.golden`, only `engine.WithExtraMatcher`). The Reporter
> registry-ownership cell was made explicit so the row cannot be misread as
> "reporters live in the engine's registry". Shipping a fourth divergent taxonomy
> is precisely what this feature exists to prevent.

**Mandatory exclusion sentence** (verbatim requirement, currently documented
nowhere): *`AggregateComparator` (`internal/core/core.go:110`; built-in
`"aggregate-cel"` wired at `internal/engine/build.go:51`) is an internal seam —
it is deliberately not aliased on the facade and has no `With*` option; it is
excluded from the public seam set until three real external demands exist.*

## The three tribal decisions (each gets a "how to choose" paragraph)

1. **Instance vs factory registration** — factory when the extension is stateful
   per engine (stores, judges); instance when stateless/shared-safe (drivers,
   comparators, matchers, reporters). Note the facade nuance: `With*` options are
   factory-typed even for instance seams (`applyExtras` constructs then registers
   the instance, `build.go:185-208`).
2. **Per-engine vs package-global** — per-engine registry is the rule (007
   reentrancy fix); reporters are the sole never-sealed package-global exception
   and why (`registry.go:177-182`).
3. **Collision-check-before-construction** — a colliding registration must never
   run its factory (`build.go:179-216` ordering; 007 regression precedent).

## New-seam checklist (the ~10 touchpoints, each a checklist line in the doc)

core interface + `go generate` mocks regen → registry map + Register/lookup
methods + seal handling → engine options funnel → build wiring (collision check
ordering) → `run.go` factory type + `With*` option → `mentat.go` alias(es) →
golden regen (`MENTAT_UPDATE_GOLDEN=1`, diff itemized in PR) →
`public-surface.md` justification → CHANGELOG → `docs/extending/` page → tests
(unit + mutation rehearsal + e2e if the seam is externally observable).

## Acceptance

A contributor can enumerate every touchpoint of a hypothetical new seam from the
guide alone (SC-004); `grep` for the old lists finds only references to the
canonical one; the exclusion sentence exists.
