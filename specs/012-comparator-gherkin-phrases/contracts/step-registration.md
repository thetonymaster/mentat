# Contract: per-engine step registration, the partition invariant, and collisions

**Fulfils**: FR-002, FR-005, FR-007, FR-007a, FR-007b, FR-007c, FR-009, FR-010, FR-013, FR-014,
FR-019. Decisions: [D3](../spec.md), [D4](../spec.md), [D8](../spec.md).
Research: [R0](../research.md), [R1](../research.md), [R3](../research.md), [R5](../research.md),
[R6](../research.md), [R9](../research.md).

---

## 1. Registration becomes per-engine — and takes its input as a parameter

```
registerSteps(reg stepRegistrar, w *world, phrases []ContributedPhrase)
```

**The parameter is required, not stylistic.** The drift test calls
`registerSteps(spy, &world{})` (`metadata_test.go:40`) with a **zero world, so `w.eng` is nil**.
Reaching through `w.eng` for phrases would panic there, and nil-guarding it would be precisely
the silent fallback Constitution IV forbids. Passing the set in also satisfies the composition
rule — the unit knows its input type and nothing about who calls it — and makes the partition
assertion below natural to write (R5).

The caller (`steps.go:100`) resolves the set from the engine via `Engine.Comparators()` (sorted)
and `Engine.Comparator(name)` plus a type assertion. **No new registry** (R3).

**Isolation obligation (FR-002, US2)**: two engines built in one process must be unable to
observe each other's phrases, in **either** run order. This is the property 007 established for
registries (`registry.go:30-35`) and that `mentat_run_reentrancy_test.go` exists because it
already bit once.

## 2. Ordering is a correctness requirement

godog returns the **first** matching step definition (R1, measured). Two consequences:

1. **Built-ins register first**, from `stepDefs` in table order, before any contributed phrase.
   A colliding contributed phrase therefore can never displace a built-in.
2. **Contributed phrases enumerate in sorted comparator-name order.**
   `Registry.Comparators()` (`registry.go:125-137`) already sorts; 011 added that sort for a
   deterministic error message, and here it becomes load-bearing — map iteration order would
   make collision resolution vary between runs of an unchanged suite.

## 3. The partition invariant (FR-005, D4)

The drift gate is **not** relaxed into "registration may be a superset of the table". It becomes
a partition:

> Every registered pattern is **either** a `stepDefs` row **or** a member of the contributed set
> supplied for this engine — and the counts add up. An unaccounted registered pattern is still a
> hard failure.

`TestStepMetadataMatchesRegistration` (`metadata_test.go:56`) keeps its bidirectional
set-equality and count checks for the built-in half. `TestNoDirectStepRegistration`
(`metadata_test.go:129`) keeps `steps.go` free of direct `sc.Step(` calls, so registration stays
single-pathed.

The gate loses no strength. It gains a second **accounted** source.

## 4. Collision detection (D8)

| Kind | When | Mechanism | Error names |
|---|---|---|---|
| Identical contributed patterns | engine build | Mentat | **both** contributors + the pattern |
| Contributed pattern identical to a built-in's | engine build | Mentat | contributor + the built-in step |
| Unanchored pattern | engine build | Mentat (R8) | contributor + the pattern |
| Genuine overlap, two well-formed anchored patterns | step match | godog, `Strict` | **every** matching expression |

### Why `Strict` rather than a heuristic — measured, not assumed

godog's ambiguity check is gated on `Strict` (`suite.go:547-553`), and `mentat.Run` does not set
it (`run.go:411-419`). The measured behaviour (R1):

```
strict=false -> suiteStatus=0 firstRan=true secondRan=false afterHookStepErr=<nil>
                "1 scenarios (1 passed)"        ← the broad pattern silently swallowed the
                                                  specific one; the scenario PASSED
strict=true  -> suiteStatus=1 firstRan=false secondRan=false
                afterHookStepErr=ambiguous step definition, step text: the widget is green
                    matches:
                        ^the widget is (\w+)$
                        ^the widget is green$
```

Three things this fixes and one it avoids:

- **It is a live defect at `0f9dcea`**, reachable between two built-in patterns today — hence
  SC-011 is a regression test that MUST be observed failing before the fix (FR-007b).
- **No new surfacing mechanism is needed.** The ambiguity reaches Mentat's After hook as a
  non-nil `stepErr`, so `Pass: stepErr == nil` (`steps.go:127`) records a FAILED scenario with
  the message as its reason. `mentat.Run` discarding the suite status is irrelevant.
- **godog's matcher is exact**, so there are no false collisions — which is why the rejected
  alternative (cross-checking every pattern against every registered example) is unnecessary.
- Anchoring does **not** eliminate overlap; two anchored patterns can still both match via
  alternation. `Strict` is the backstop, the build-time checks are the fast feedback.

### `Strict` carries zero golden churn — measured (FR-007c, SC-012)

The spec refused to accept a green `make ci` as evidence here, because the SC-005 stdout goldens
are `//go:build e2e` and `make ci` never compiles them. Both surfaces were run with Docker up:

| Surface | Baseline | With `Strict: true` |
|---|---|---|
| `go test ./...` (incl. hermetic stdout golden) | all `ok` | all `ok`, 0 FAIL |
| `go test -tags e2e -run TestGolden ./e2e/` | — | `PASS` |
| `go test -tags e2e -timeout 25m ./e2e/` | `ok 50.7s` | `ok 42.1s` |

Implementation re-runs both as the task's acceptance evidence; this is the planning-time proof
that the approach is viable, not a substitute for it.

## 5. The step-binding precheck loses its package-level cache (FR-010, R6)

`stepPatternsOnce` / `stepPatterns` (`precheck.go:76-91`) are **deleted**, not adapted. The
`sync.Once` fixes the first-compiled pattern set for the whole process, so with per-engine
phrases a second `mentat.Run` would be checked against the first run's patterns — the same
defect class 007 closed for registries.

`StepBindingFindings` becomes a function of (pattern set, steps, source) rather than of package
state. Compilation cost is trivial (~40 patterns plus contributed) against a single SUT drive.

Consumers to update: the scenario-init precheck path, and `mentat validate` (`validate.go:185`),
whose `checker` (`validate.go:117`) supplies built-ins only — see
[validate-surface.md](./validate-surface.md).

## 6. Rendering (FR-013, FR-014, R9)

- `mentat steps` and the committed `docs/steps.md` keep rendering **built-ins only, byte
  identically**. The generated page gains one sentence stating contributed phrases are
  engine-scoped and therefore not listed there.
- The engine-scoped renderer emits contributed phrases in **contiguous group blocks after** the
  built-in groups, regardless of the group names authors choose. `TestStepDocsGroupsAreContiguous`
  (`docs_test.go:43`) lets the markdown generator emit one heading per group by watching the
  group change; a contributed phrase declaring an existing name (e.g. `"Shape"`) would otherwise
  produce a duplicated heading.
- `TestStepDocsMirrorsTable` (`docs_test.go:12`) keeps proving the built-in view lossless.

## 7. Zero-cost-when-unused (SC-009)

An engine with no contributed phrases MUST produce byte-identical output to `0f9dcea` across
every existing golden. This is the overwhelmingly common case and the cheapest guard against the
feature leaking into paths it has no business touching.

`Strict: true` is the one deliberate exception to "nothing changes when unused" — it changes
behaviour for *every* engine, which is why R0 measured both surfaces and why it is sequenced
first and separately in the plan.
