# Mentat Seam Registries — Design

**Date:** 2026-06-18
**Status:** Approved design, pending implementation plan
**Author:** Antonio Cabrera (Q)
**Companion to:** `2026-06-16-trace-behaviour-test-framework-design.md`
**Related:** `2026-06-18-mentat-v1-gap-closure-design.md` (Spec A — its `schema` matcher rides this seam)

## 1. Purpose

Architecture invariant #3 says **"every seam is an interface, wired at one composition
root via per-seam registries."** Today only two of the seven designed seams actually
have a registry:

| Seam | Registry today | Real impls today |
|---|---|---|
| comparator | ✅ `registry.RegisterComparator` (`registry.go:11`) | 4 (sequence, budgets, result, cel) |
| driver | ✅ `registry.RegisterDriver` (`registry.go:26`) | 2 (shell, http) |
| **matcher** | ❌ a `switch` inside `result.go:44-53` | 5 (exact, contains, regex, json-subset, status) |
| **store** | ❌ `store.NewTempo` hard-coded in both `cmd/*` entrypoints | 2 (Tempo prod, InMem/fixture test) |
| correlator | ❌ `correlate.New` constructed directly | **1** |
| reporter | ❌ godog built-in only | 1 (godog pretty/junit) |
| judge | ❌ — | 0 (Phase 4) |

This spec closes the **matcher** and **store** seams now — the two with a genuine
second implementation and a present consumer. It deliberately leaves correlator,
reporter, and judge wired directly until a second implementation exists (§6), and it
leaves the working comparator/driver registries untouched.

The matcher registry is also a **prerequisite for Spec A's `schema` matcher** and for
Phase 4's `semantic` matcher: both want to be pluggable, not new `switch` arms.

## 2. Scope

**In scope:**

- `core.Matcher` interface + `registry.RegisterMatcher`/`registry.Matcher`; refactor
  `result.go` into a thin dispatcher over registered matchers; register the five
  built-ins at the composition root.
- `registry.RegisterStore`/`registry.Store` (factory-based); a `store:` config field;
  `cmd/*` entrypoints select the store by name instead of hard-coding Tempo.

**Out of scope (deferred, with triggers in §6):**

- correlator registry — one impl; justified once a traceparent complement exists.
- reporter registry — one impl (godog); justified once a non-godog reporter is wanted.
- judge registry — Phase 4 concern; no impl yet.
- Re-working the existing comparator/driver registries — they work and are stateless;
  Chesterton's fence (leave them).

## 3. Matcher registry

### 3.1 Interface (defined in `core`, beside the other seams)

`result.go` matchers read different `Evidence` fields (`Answer`/`Status` via `Target`;
`Body` for `json-subset`/`schema`), so the interface takes the whole `Evidence` plus
the expectation's `want`/`target` strings:

```go
// internal/core/core.go
type Matcher interface {
    Name() string
    Match(ctx context.Context, ev Evidence, want, target string) (Verdict, error)
}
```

Defining it in `core` (where `Comparator`, `Driver`, `TraceStore`, `Correlator`
already live) keeps `registry` importing only `core` — no cycle.

### 3.2 Registry

```go
// internal/registry/registry.go
var matchers = map[string]core.Matcher{}
func RegisterMatcher(name string, m core.Matcher) { matchers[name] = m }
func Matcher(name string) (core.Matcher, bool)    { m, ok := matchers[name]; return m, ok }
```

### 3.3 `result` becomes a dispatcher

`result.Compare` (`result.go:44-53`) stops switching on the matcher name and instead
resolves it from the registry, preserving today's exact errors:

```go
m, ok := registry.Matcher(exp.Matcher)
if !ok {
    return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
}
return m.Match(ctx, ev, exp.Want, exp.Target)
```

The five existing matchers become small `core.Matcher` types in the `comparator`
package (`exact`, `contains`, `regex` keep `targetString`; `json-subset` and `status`
keep their `Body`/`Status` sources). The `result` comparator keeps its "reads only
`ev.Output`" contract — every built-in matcher honours it; a trace-aware matcher would
belong to a different comparator (as CEL already does).

### 3.4 Registration at the composition root

`engine.Build` (`build.go`) registers the built-ins alongside the existing comparator
registrations, via a `comparator.RegisterBuiltinMatchers()` helper so the set has one
home:

```go
comparator.RegisterBuiltinMatchers() // exact, contains, regex, json-subset, status (+ schema, Spec A)
```

Spec A's `schema` and Phase 4's `semantic` then register through the same seam with no
change to `result`.

### 3.5 Test impact (flagged)

`result_test.go` currently calls `Compare` against the built-in switch. After the
refactor those tests register the built-ins first (calling
`comparator.RegisterBuiltinMatchers()` in setup, or registering the specific matcher
under test). This is the cost of moving from a `switch` to a seam and is one-time.

## 4. Store registry

### 4.1 Factory, not instance

Comparators and drivers are stateless singletons, so their registries hold *instances*.
Stores are **stateful** (Tempo endpoint + http client; InMem holds preloaded traces),
so the store registry holds a **factory**:

```go
// internal/registry/registry.go
type StoreFactory func(cfg config.Config) (core.TraceStore, error)
var stores = map[string]StoreFactory{}
func RegisterStore(name string, f StoreFactory)        { stores[name] = f }
func Store(name string) (StoreFactory, bool)           { f, ok := stores[name]; return f, ok }
```

`registry` importing `config` is acyclic (`config` imports neither `registry` nor
`core`). If we prefer to keep `registry` dependency-free, the factory can instead take
the already-parsed `config.Endpoint`/`PollSpec` values; the implementation plan picks
one. The intent — name → constructed `core.TraceStore` — is fixed.

### 4.2 Config selection

`config.Config` gains `Store string` (default `"tempo"`). The two entrypoints
(`cmd/mentat/main.go`, `cmd/mentatctl/main.go:128`) stop calling `store.NewTempo`
directly and instead:

```go
factory, ok := registry.Store(cfg.Store) // cfg.Store defaults to "tempo"
if !ok { return fmt.Errorf("unknown store %q", cfg.Store) }
st, err := factory(cfg)
```

The Tempo factory is registered at the composition root; it wraps today's
`store.NewTempo(cfg.Tempo.Endpoint, nil)`.

### 4.3 Tests keep injecting directly

`engine.Build` keeps its `st core.TraceStore` parameter (`build.go:19`), so hermetic
tests continue to construct `store.NewInMem(traces)` and inject it without touching the
registry. The registry serves the **config-selected production** path only; the test
path is unchanged. (This is why a registry entry for InMem is unnecessary — it is never
selected by config.)

## 5. Two registration patterns (documented rationale)

The codebase now has two deliberate, documented registry shapes:

- **Stateless seam → register an instance** (comparator, driver, matcher). The thing
  has no per-run configuration; one shared value is correct and concurrency-safe.
- **Stateful seam → register a factory** (store). The thing needs config to build, so
  the registry yields a constructor the composition root invokes once.

This asymmetry is intentional and noted in `registry.go` so a future contributor does
not "fix" the store registry into the instance shape.

## 6. Deferred seams — triggers to revisit

- **correlator** — one impl (`correlate.New`). Becomes justified when Spec A's reserved
  traceparent complement is actually built (a second correlator); at that point add
  `RegisterCorrelator` and a `correlator:` config field (default `"baggage"`).
- **reporter** — one impl (godog pretty/junit, wired in `cmd/mentat`). Becomes
  justified when a non-godog reporter is wanted (e.g. a JSON/HTML report); until then a
  registry is an empty abstraction.
- **judge** — Phase 4. The `semantic` matcher rides the matcher seam (§3); its `Judge`
  backend gets `RegisterJudge` when the first `claude` judge lands.

Building these now would be premature abstraction (YAGNI): a registry with a single
member adds indirection without the second member that gives it meaning.

## 7. Testing

- **Matcher:** registry get/put unit tests; a `result` dispatcher test (registered
  matcher resolves and runs; unknown matcher → the existing hard error); the migrated
  built-in matcher tests (now per-matcher type). Coverage of `internal/comparator`
  stays ≥80%.
- **Store:** registry get/put unit tests; a Tempo-factory construction test; an
  entrypoint-level test that an unknown `store:` name errors descriptively. The
  existing Tempo store tests are unchanged (the factory wraps the same constructor).
- **Hermetic:** all unit tests in-memory; the live-Tempo e2e path is unchanged because
  the default `store: tempo` resolves to the same Tempo client as before.

## 8. Decisions made (with rationale)

- **Build matcher + store registries now; defer correlator/reporter/judge** — only
  matcher (5 impls) and store (2 impls) have a real second implementation and a present
  consumer; the rest are single-impl and would be empty abstractions. (Approved.)
- **`core.Matcher` defined in `core`** — beside the other seam interfaces, so
  `registry` stays `core`-only and `result` can dispatch without a cycle. (Approved.)
- **Store registry is factory-based; matcher registry is instance-based** — stateful vs
  stateless; documented so it is not "unified" later by mistake. (Approved.)
- **`engine.Build` keeps taking a built `TraceStore`** — hermetic tests inject InMem
  directly; the registry is the config-selected production path only. (Approved.)
- **Leave comparator/driver registries untouched** — they already satisfy invariant #3
  and work; no reason to churn them. (Approved.)
- **Matcher registry sequenced before Spec A's `schema`** — so `schema` ships as a
  registered matcher, not a `switch` arm that is immediately refactored. (Approved.)
