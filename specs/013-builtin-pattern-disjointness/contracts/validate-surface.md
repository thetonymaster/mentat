# Contract: validate surface — delta for 013

**Feature**: 013 | **Base**: `specs/012-comparator-gherkin-phrases/contracts/validate-surface.md`

This records only what 013 changes. 012's contract remains authoritative for everything else.

## 1. New finding class: `pattern-overlap`

| | |
|---|---|
| Class key | `pattern-overlap` |
| `File` | `""` |
| `Line` | `0` |
| Emitted by | `mentat.Validate` (library), once per overlapping pattern pair |
| Emitted by the `mentat validate` **binary** | **never** — it builds no engine, so it sees no contributed phrase (012 D7), and built-in × built-in is the CI gate's job |
| Granularity | one finding per **pattern pair**, not per sentence and not per feature file |
| Message MUST name | both patterns, the contributing comparator(s) where any, and the witness string |

The full class list becomes: `bad-cel`, `unknown-target`, `unbound-step`, `ambiguous-step`,
`step-argument`, `unknown-shape`, `bad-runs-tag`, **`pattern-overlap`**.

### Why not reuse `ambiguous-step`

They answer different questions and a consumer parsing findings can act on the difference:

- `ambiguous-step` — "how many registered patterns match **this sentence**", located at the
  feature-file line the author wrote. Reachable whenever a sentence falls in an overlap.
- `pattern-overlap` — "can these two patterns ever match the same string", located nowhere,
  reachable with **no feature file at all**.

Reusing the key would make a pattern-level defect look line-level, and would break the standing
guarantee that an `ambiguous-step` finding points at a step someone actually wrote.

## 2. §4 of 012's contract is superseded

012's §4 said `ambiguous-step` is unreachable from the `mentat validate` binary, **as measured**
— measured by a generator substituting nine fixed fillers into capture groups. That sentence was
the reason 013 exists.

**Post-013 position:**

- The built-in set's disjointness is **decided** by a gate, not sampled. The nine-filler hedge is
  retired as the basis for the claim (FR-004, SC-004).
- `TestBuiltinStepPatternsArePairwiseDisjoint` remains, reframed as an **independent cross-check**
  of the decider rather than the evidence for the property (FR-005, D3). Two mechanisms of
  different kinds mean a disagreement proves one is broken.
- `ambiguous-step`'s reachability from the binary is no longer asserted as a property. It is
  gated for built-ins and reachable for contributed phrases; there is nothing left to hedge.

Any wording that describes disjointness as "measured" rather than "decided" is now stale — that is
US4's job to sweep (FR-004).

## 3. Ordering and determinism

`pattern-overlap` findings MUST end up inside the single sorted list, which means **wrapping
`mentat.Validate`'s return**:

```go
return steps.DedupeSortFindings(append(overlap, check.Paths(ro.featurePaths)...)), nil
```

An earlier draft of this section said the findings are "folded in with the existing pre-walk
findings … the idiom `cmd/mentat/validate.go` already uses". **Measured false in two ways.**
`mentat.Validate` (`run.go:573-612`) has no pre-walk findings and never calls
`DedupeSortFindings` — it returns `SuiteCheck{…}.Paths(…)` directly, and `Paths` dedupes
*internally* (`internal/steps/suite.go:92,97`). The `pre` + `DedupeSortFindings` idiom exists only
at `cmd/mentat/validate.go:161` — the binary, which §1 says will **never** emit this class. So the
draft credited an idiom to the one call site that cannot use it.

Appending after `Paths` without re-sorting would leave `File:""`/`Line:0` findings dangling
outside the ordered list, breaking the guarantee this section exists to state.

Pair enumeration MUST be deterministic — registration order, built-ins first — so repeated runs of
an unchanged engine produce byte-identical output.

## 4. Golden impact — run the e2e lane

`make ci` is `lint test cover example`. **The stdout goldens are `//go:build e2e` and `make ci`
does not compile that lane**, so a green `make ci` is not evidence that the goldens are current.

Run `go test -tags e2e` with `make harness-up` before claiming this contract holds. 012 hit exactly
this: both stdout goldens had captured godog's step-definition source line, and the e2e one was
caught only by running the tagged lane.
