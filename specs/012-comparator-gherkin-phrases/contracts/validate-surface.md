# Contract: the validate surface (library entry point + the binary's documented limit)

**Fulfils**: FR-009, FR-011, FR-011a, FR-012, FR-015. Decision: [D7](../spec.md).
Research: [R6](../research.md), [R7](../research.md).

---

## The problem this closes

`mentat validate` does not build an engine. It constructs a `checker` by hand
(`cmd/mentat/validate.go:117`) holding a `cel` and an `aggregate-cel` comparator and nothing
else — deliberately: no registry, no store, no driver. A Go consumer's `WithComparator` calls
live in **their** module and cannot reach a compiled `mentat` binary.

Applied unchanged to a suite using contributed phrases, `StepBindingFindings`
(`precheck.go:95`, called at `validate.go:185`) emits one `unbound-step` finding per phrase: a
**false red on a valid feature file**, from the one command whose entire job is certifying that
a suite is well-formed.

No flag closes that gap. Only running the consumer's code does.

---

## 1. The library entry point (FR-011)

A facade function accepting the same `Option`s `mentat.Run` accepts, returning the existing
`Finding` values. The consumer already assembles those options to run their suite; reusing them
means validation sees **exactly** the engine the run will use.

- Returns `[]Finding` — `steps.Finding` (`precheck.go:23-28`), aliased on the facade. Legal
  under 010's D5 because it is declared in `internal/steps`, beneath root, not at it (R7).
- Collect-all, not fail-fast: `Finding` exists precisely because the prechecks return every
  defect rather than stopping at the first (`precheck.go:16-22`).
- Contributed phrases are checked **strictly** here — an `unbound-step` from this path means
  what it says, because the pattern set is the engine's own (R6).
- Reuses the existing precheck logic verbatim. This entry point is a composition of existing
  parts, not a second implementation of validation.

## 2. The binary keeps its strictness and documents its limit (FR-011a)

`mentat validate` keeps its strictness for built-in steps, and its documentation states plainly
that contributed phrases are outside what a compiled binary can see, pointing at the library
entry point.

> **Extended at convergence (Phase 9, 2026-09-11).** "Keeps its current behaviour" was true when
> written and is no longer. The binary now also enforces the **step-argument** check for built-in
> rows (`steps.BuiltinStepArguments()`), emitting a `step-argument` finding. That is not a
> widening of what a binary can see — each built-in row's expected argument is derived by
> reflection from the handler it registers, all of which are compiled into the binary. It is
> strictly more strictness over exactly the steps the binary already validated, and it closes an
> unearned green that needed no comparator to reach. Contributed phrases remain out of reach, so
> D7 and FR-011a are unchanged.

**It gains no manifest flag and no second source of phrase truth.**

### Why the manifest was rejected (recorded because it is the intuitive option)

Having the consumer emit a manifest of their phrases for the binary to read appears to preserve
a single command. It does not pay:

1. **Generating the manifest already requires running the consumer's Go code**, so it removes no
   dependency — it only relocates it.
2. The standalone-lint benefit therefore only materialises if the file is **committed**, at
   which point it can drift from the engine that produced it.
3. Closing that drift means rebuilding the `stepDefs` drift-test machinery for a second
   artifact — in a feature whose entire purpose is protecting a single-source-of-truth
   invariant.

### The path that stays open

If a standalone lint job is later genuinely wanted, the manifest becomes a **generated** artifact
rendered by this entry point and guarded by a byte-identity regeneration test — the same
`go:generate` + golden pattern `docs/steps.md` already uses (`cmd/mentat/steps_cmd.go:3`). The
engine stays authoritative and the drift gate is structural rather than remembered.

Out of scope for 012, and recorded so it is a **layer, never a fork**.

---

## 3. Finding semantics after this feature

| Path | Sees contributed phrases | `unbound-step` means |
|---|---|---|
| Scenario-init precheck | yes (engine-aware) | the step binds nothing that will run |
| Library validate entry point | yes (engine-aware) | same, statically |
| `mentat validate` binary | no (structurally) | no **built-in** step matches; the docs state contributed phrases are out of reach |

**No ENGINE-AWARE path reports a valid file as broken** (SC-005). The binary is outside that
scope by D7/FR-011a, and the distinction is load-bearing rather than pedantic.

> **Corrected at convergence (T073/T070, measured 2026-09-11).** This line previously read "No
> path reports a valid file as broken", which contradicted the table directly above it and was
> the more memorable half. Measured against the shipped feature, using the worked example from
> [`docs/extending/phrases.md`](../../../docs/extending/phrases.md):
>
> ```
> $ mentat validate -config mentat.yaml phrase.feature
> phrase.feature:3: [unbound-step] no step matches "the revenue floor is 4 USD"
> validate: 1 issue(s) found          # exit 1
> ```
>
> The binary **does** report that valid file as broken, exactly as the table says it must: a
> compiled binary cannot reach a consumer's `WithComparator` calls, and no flag closes that. The
> same sentence a reader is told to write in `phrases.md` fails the binary's check. 013 is
> CLI/`mentatctl` UX and inherits this contract, so it is corrected here rather than footnoted.

Consumers reach SC-005's guarantee through `mentat.Validate`, which builds their engine — that
is the whole reason D7 published it.

The binary does not soften its finding class for built-in steps — a genuinely misspelled built-in
step still fails there, which is the reason the rejected "soft finding class" option was not
taken: it would have weakened the gate for everyone to accommodate a case the binary cannot see.

---

## 4. What this contract does not change

- `Finding`'s shape (`File`, `Line`, `Class`, `Message`) — unchanged.
- The `PrecheckEngine` consumer-defined interface keeps serving both `*engine.Engine` and
  validate's lightweight `checker` (`precheck.go:64-70`); comparators still never see a store or
  driver (Constitution I).
- `mentat validate`'s exit-code semantics and its other finding classes (`bad-cel`,
  `unknown-target`, `unknown-shape`, `bad-runs-tag`). Convergence Phase 9 ADDED one,
  `step-argument`, for built-in rows only — see the note in §2.
