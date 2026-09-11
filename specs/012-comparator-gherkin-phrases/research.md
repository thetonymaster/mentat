# Research: Comparator-Contributed Gherkin Phrases (012)

**Date**: 2026-09-10 | **Baseline**: `0f9dcea` | **godog**: `v0.15.1` (pinned)

Two of these items were settled by **running an experiment**, not by reading source. Both
touched claims the spec inherited from 011's D1, and one of them was false. Where a finding is
empirical, the exact command and its output are recorded so the next reader does not have to
re-run it to trust it.

---

## R0 — Does enabling `Strict` churn any committed golden? **MEASURED: no.**

**Decision**: Enable `Strict: true` in `mentat.Run`'s godog options (`run.go:411-419`). SC-012
is satisfied by the evidence below rather than deferred to implementation.

**This was the feature's one named risk.** The spec predicted zero churn and explicitly refused
to let a green `make ci` stand as evidence, because the SC-005 stdout goldens are
`//go:build e2e` and `make ci` never compiles them. So the experiment ran both surfaces.

**Method**: patched `Strict: true` into `run.go`, ran each surface, reverted, re-ran the e2e
surface to establish the matching baseline. Docker was up (`make harness-up`) so live-Tempo
tests actually executed rather than being skipped.

| Surface | Command | Baseline | With `Strict: true` |
|---|---|---|---|
| All non-e2e (incl. hermetic stdout golden `mentat_golden_test.go`) | `go test ./...` | all `ok` | all `ok`, **0 FAIL** |
| e2e stdout golden (SC-005) | `go test -tags e2e -run TestGolden ./e2e/` | — | `PASS` (14.68s) |
| Full e2e suite (L3 meta-tests included) | `go test -tags e2e -timeout 25m ./e2e/` | `ok 50.7s` | `ok 42.1s` |

**Zero golden churn on both surfaces.** The prediction held, and it is now a result.

**Rationale for why it held**: Mentat registers no pending steps, and undefined steps already
produce a failed scenario through the collector (011's `TestUndefinedStepFailsTheRun`), so the
two behaviours `Strict` changes were already Mentat's behaviour by another route.

**Alternatives considered**: leaving `Strict` off and detecting overlap with a hand-written
heuristic (spec's rejected option). Unnecessary given R1, and strictly worse — a heuristic can
false-collide, godog's matcher cannot.

**Caveat for planning**: this is a property of a *pinned* dependency. A godog bump re-opens R0
and R1 together.

---

## R1 — What does godog do when two patterns match one step? **MEASURED: silently picks the first.**

**Decision**: Mentat MUST NOT rely on godog for collision detection *unless* `Strict` is on;
with `Strict` on, godog's matcher is exact and sufficient. This is the evidence behind D8.

**011's D1 said** "godog reports an ambiguous match; Constitution IV requires a loud failure,
never last-wins." **That is false as stated** for `mentat.Run`. The check is gated
(`suite.go:547-553`) and `mentat.Run` does not set `Strict`.

**Method**: a throwaway godog suite registering a broad pattern first (standing in for a
built-in `stepDefs` row) and a specific one second (standing in for a contributed phrase), both
matching `the widget is green`. An `sc.After` hook mirroring Mentat's verdict-authoritative
hook (`steps.go:126-129`) recorded the `stepErr` it received.

```
strict=false -> suiteStatus=0 firstRan=true  secondRan=false afterHookStepErr=<nil>
                pretty output: "1 scenarios (1 passed)"

strict=true  -> suiteStatus=1 firstRan=false secondRan=false
                afterHookStepErr=ambiguous step definition, step text: the widget is green
                    matches:
                        ^the widget is (\w+)$
                        ^the widget is green$
                pretty output: "1 scenarios (1 ambiguous)"
```

**Three things this establishes, in descending order of importance:**

1. **It is first-wins, not last-wins, and it is silent.** The broad pattern ran; the specific
   one never did; the scenario **passed**. Because built-ins register from `stepDefs` before any
   contributed phrase would, a contributed phrase colliding with a built-in is silently
   shadowed and the author's assertion never executes. A green verdict nobody wrote.
2. **This is a LATENT defect at `0f9dcea`, not a live one.** *(Corrected 2026-09-11 at
   implementation time — see R10. The original text claimed it was "reachable between two
   built-in patterns today"; that was asserted, never measured, and it is false.)* The godog
   behaviour above is real and `mentat.Run` really does not set `Strict`, but the 40 built-in
   patterns are **pairwise disjoint**, so no sentence can reach the ambiguous branch through
   Mentat's public surface today. Contributed phrases are what first make it reachable — which
   is why `Strict` is a **prerequisite** of this feature rather than an independent bugfix it
   happens to carry. SC-011's regression test is therefore written at the step-registration
   level (two patterns registered directly), with the end-to-end counterpart arriving only once
   phrases exist.
3. **`Strict` routes the ambiguity through the exact path Mentat already trusts.** The After
   hook receives it as a non-nil `stepErr`, so `Pass: stepErr == nil` (`steps.go:127`) yields
   `Pass=false` and `Reasons=[<message naming every matching expression>]`. **No new
   surfacing mechanism is required** — the collector records a FAILED scenario with a useful
   reason, and `mentat.Run` discarding the suite status is irrelevant.

**Alternatives considered**: parsing godog's `Ambiguous` formatter callback, or checking suite
status. Both rejected — the After-hook path already works and is the one 011 established.

---

## R2 — Where does the phrase-declaration type live?

**Decision**: `internal/core`, alongside `ExpectationParser`, aliased on the facade.

**Rationale**: The type appears in a published seam's method set, so 010's nameability sweep
will demand a facade alias automatically (SC-008). 010's D5 forbids declaring it *at* the
facade, because root imports `internal/steps`/`internal/engine`/`internal/registry` and those
packages consume it. `internal/core` imports only stdlib plus `internal/trace`
(`core.go:5-13`), so it is a leaf for this purpose and adding the type creates no cycle —
verified: neither `internal/engine` nor `internal/core` imports `internal/steps`.

**Alternatives considered**: a new `internal/phrase` package (more moving parts for one type
group); declaring at the facade (forbidden by D5).

---

## R3 — How is the contributed set resolved per engine?

**Decision**: Reuse the existing accessors. `Engine.Comparators()` (`engine.go:207`, sorted)
enumerates names; `Engine.Comparator(name)` (`engine.go:200`) resolves each instance; a type
assertion finds the ones that contribute phrases. Add one accessor mirroring `Comparators()`
exactly.

**Rationale**: No registry change is needed — `Registry.Comparators()` already sorts
(`registry.go:125-137`), and 011 made that sort load-bearing precisely so a derived list is
deterministic between runs. Deterministic order matters more here than it did for 011's error
message: it fixes godog registration order, which under R1 decides which pattern wins a
collision. **Sorted enumeration is therefore a correctness requirement, not a tidiness one.**

**Alternatives considered**: a dedicated phrase registry seam (a seventh registry for data
already reachable through the sixth — rejected as over-abstraction; the composition rule wants a
second implementation that exists now, and there is none).

---

## R4 — How is the godog handler built, given runtime-unknown arity?

**Decision**: `reflect.MakeFunc` over `reflect.FuncOf(N × string, error)`, with N from the
compiled pattern's `NumSubexp()`; a docstring-carrying phrase appends
`*messages.PickleDocString` as the final parameter.

**Rationale**: godog rejects `[]string` and variadic handlers — `reflect.Slice` accepts
`[]byte` only (`internal/models/stepdef.go:222-233`) — and its arity check is one-sided
(`stepdef.go:58`): too few args error, **surplus args are silently discarded** because the
conversion loop runs `i < numIn`. Deriving N from the same regex godog matches against makes
the two structurally unable to disagree, converting a silent-drop hazard into an impossibility.

**This constrains only the bridge.** Because Mentat synthesizes the handler, the
comparator-facing seam is unconstrained by godog's rules — which is precisely what freed D6 to
choose a `[]string`-shaped sibling interface.

**Alternatives considered**: a fixed set of pre-declared arities (1..N) — brittle and caps
phrase expressiveness at an arbitrary N; a zero-arg handler (binds fine, since `len(Args) >= 0`,
but cannot see any capture).

---

## R5 — How does the drift test express the partition without a nil-engine panic?

**Decision**: Change `registerSteps` to take the resolved phrase set as a parameter:
`registerSteps(reg stepRegistrar, w *world, phrases []ContributedPhrase)`. The caller
(`steps.go:100`) resolves it from the engine; the drift test passes what it wants to assert.

**Rationale — this is a real constraint, not a style choice.** The drift test calls
`registerSteps(spy, &world{})` (`metadata_test.go:40`) with a **zero world, so `w.eng` is nil**.
If `registerSteps` reached through `w.eng` for phrases it would panic there, and nil-guarding it
would be exactly the silent fallback Constitution IV forbids. Passing the set in also matches
the composition rule — the unit knows its input type and nothing about who calls it — and makes
the partition assertion natural: drive the spy with a known phrase set, then assert every
registered pattern is either a `stepDefs` row or a member of that set, with counts adding up.

**Alternatives considered**: giving `world` a phrase field (keeps the nil-eng problem, just
moves it); a package-level phrase var (forbidden by D3).

---

## R6 — How does the step-binding precheck lose its package-level cache?

**Decision**: Delete `stepPatternsOnce`/`stepPatterns` (`precheck.go:76-91`). Compile the
pattern set per engine and pass it to `StepBindingFindings`, which becomes a function of
(pattern set, steps, source) rather than of package state.

**Rationale**: The `sync.Once` fixes the first-compiled pattern set for the process, so with
per-engine phrases a second `mentat.Run` would be checked against the first run's patterns.
Same defect class 007 closed for registries (`registry.go:30-35`), and the property US2 exists
to prove. Compilation cost is trivial (≈40 patterns) and can be done once per engine build if
it ever matters.

**Consumers to update**: the scenario-init precheck path, and `mentat validate`
(`validate.go:185`) whose `checker` (`validate.go:117`) supplies no phrases — see R7.

---

## R7 — What shape does the validate entry point take? (D7)

**Decision**: A facade function accepting the same `Option`s `mentat.Run` accepts, returning
the existing `Finding` values. `steps.Finding` (`precheck.go:23`) is aliased on the facade —
legal under 010's D5 because it is declared in `internal/steps`, beneath root, not at it.

**Rationale**: The consumer already constructs their options for `mentat.Run`; reusing them
means validation sees exactly the engine the run will use, with no second artifact. The binary's
`validate` keeps its `checker` and its current strictness for built-ins, and documents that
contributed phrases are outside a compiled binary's reach.

**Alternatives considered**: a manifest flag on the binary — rejected in D7 with the full
argument (generating it requires running consumer Go code anyway, so the standalone-lint benefit
only lands if the file is committed, at which point it can drift and needs its own regeneration
gate). Recorded as a deferred *layer*: if wanted later, the manifest becomes a generated
artifact rendered by this entry point and guarded byte-identically, the way `docs/steps.md`
already is (`steps_cmd.go:3`).

---

## R8 — How is anchoring checked? (FR-007a)

**Decision**: Require the pattern to begin `^` and end `$`, rejecting otherwise at engine build.
Check the *unescaped* terminal `$` (a pattern ending `\$` is not anchored).

**Rationale**: All 40 built-in patterns are already `^…$`, so the rule matches existing practice
rather than imposing a new one, and an unanchored contributed pattern is the realistic way to
swallow a built-in (R1's experiment used exactly that shape). Anchoring does not eliminate
overlap — two anchored patterns can still both match via alternation or character classes —
which is why `Strict` (R0/R1) remains the backstop rather than the anchoring rule alone.

---

## R9 — Where do contributed phrases appear in the rendered reference?

**Decision**: The engine-scoped renderer emits contributed phrases in their own group(s) after
the built-in groups. `mentat steps` and `docs/steps.md` keep rendering built-ins only,
byte-identically, and the generated page gains a sentence stating that contributed phrases are
engine-scoped.

**Rationale**: `TestStepDocsGroupsAreContiguous` (`docs_test.go:43`) lets the markdown generator
emit one heading per group by watching for the group to change — an interleaved group produces a
duplicated heading. A contributed phrase declaring an existing group name (e.g. `"Shape"`) would
break that, so the renderer MUST group contributed phrases contiguously regardless of the names
authors choose. This is an edge case the spec lists and the reason FR-014 extends the
contiguity guarantee to the engine-scoped path.

---

## R10 — Are any two built-in patterns ambiguous today? **MEASURED: no, they are pairwise disjoint.**

*Added 2026-09-11 at implementation time. It corrects R1's claim #2, which was inherited into
plan.md and tasks.md.*

**Decision**: Treat `Strict: true` as a **prerequisite** for contributed phrases, not as a fix
for a defect users can hit at `0f9dcea`. The change itself is unaltered; only its justification
and the shape of its regression test change.

**Why it was checked**: R1's claim #2 asserted the ambiguity was "reachable between two built-in
patterns today". R1 measured godog's *behaviour* but never measured *reachability*. This feature's
own quickstart says to check whether a surprising thing is a premise nobody re-verified, so it
was verified before writing a test whose framing depended on it.

**Method**: two probes over the 40 `stepDefs` patterns.

| Probe | Construction | Result |
|---|---|---|
| 1 | Each row's own `example`, keyword stripped, matched against all 40 compiled patterns | 0 sentences matched >1 pattern |
| 2 | Sentences generated from each pattern's parsed syntax tree, expanding **every alternation branch**, × 9 fillers (`"x"`, `""`, `"a b"`, `"1"`, `"2nd"`, `"true"`, `"0.5"`, `"tool-name"`, `"a/b.c"`) — 1530 unique sentences | **0 sentences matched >1 pattern** |

**Why it holds structurally**, not just on the sample: every pattern carries a distinct literal
skeleton. Terminal literals alone separate most of the table — `… exists$` vs `…"$` vs `…:$` —
and the remainder differ on literal prefixes (`the result contains "` / `the result of ` /
`the run satisfies` / `the runs satisfy` / `a span matching` / `at least` / `exactly`).

**And no other registration route exists**: `TestNoDirectStepRegistration`
(`metadata_test.go:129`) keeps `steps.go` free of direct `sc.Step(` calls,
`TestStepMetadataMatchesRegistration` (`:56`) forbids duplicate rows by count, and 011's `Extend`
row is a single pattern.

**Consequences**:

1. SC-011 cannot be an end-to-end test at `0f9dcea` — there is no way to construct the collision
   through the public surface. It is a step-registration-level test (T004); the end-to-end
   version is T038, after phrases exist.
2. The "ship the defect fix as its own PR" strategy loses its rationale. It stays a **separate
   first commit** on this branch for reviewability of the `Strict` flag, not a separate PR.
3. "The built-in patterns are pairwise disjoint" is worth **pinning as a test** — V4 (a
   contributed pattern identical to a built-in's) implicitly assumes the built-in set is
   internally unambiguous, and nothing asserted that before. Added as a guard in US3.

**Alternatives considered**: leaving the claim in place since the remedy is unchanged. Rejected —
a spec that misstates why a change is needed will mislead the next reader into thinking the
change can be reverted once 012 ships.

---

## Summary of what changed versus the spec's assumptions

| Item | Spec assumed | Research found |
|---|---|---|
| `Strict` golden churn | predicted zero, demanded proof | **measured zero**, both surfaces (R0) |
| godog ambiguity | 011's D1: "reports an ambiguous match" | **false without `Strict`** — silent first-wins, scenario PASSES (R1) |
| Ambiguity reachable today | R1 (first draft): "a live defect, reachable between two built-ins" | **false** — the 40 built-in patterns are pairwise disjoint, measured over 1530 generated sentences. Latent, not live; phrases are what make it reachable (R10) |
| Surfacing ambiguity | might need a mechanism | **none needed** — reaches the existing After hook as `stepErr` (R1) |
| `registerSteps` signature | not considered | **must take phrases as a parameter** — the drift test uses a nil-engine world (R5) |
| Registry work | possible new seam | **none** — existing sorted accessors suffice, and the sort is load-bearing for collision determinism (R3) |

No `NEEDS CLARIFICATION` items remain.
