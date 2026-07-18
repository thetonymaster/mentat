# Contract: The Public Surface Manifest

This document is the reviewed record of Mentat's public API. The golden file
(`public-surface.golden`, generated at implementation) enforces it mechanically;
this file explains it. **Rule: a symbol appears here with a justification, or it
does not get exported.**

## Stability policy (pre-1.0)

- The module is v0: breaking changes are permitted but MUST be deliberate —
  golden file updated in the same PR, changelog entry, migration note.
- Silent surface drift is a CI failure (the golden test names the symbol).
- Tagging v1.0 (freeze) is an explicit future decision, out of scope here.

## Surface groups (justification model)

| Group | Membership rule |
|-------|-----------------|
| Seam interfaces | the PUBLIC-HOOK axis: four seams have registration hooks (driver, store, comparator, judge); correlator/reporter are types-only until three real demands exist; matcher/aggregate-comparator are internal-only and absent from this surface. This is not the same set as the registry-ownership axis — for the canonical table of both, see [docs/extending/new-seam.md](../../../docs/extending/new-seam.md) |
| Contract types | a type is exported iff a seam method signature or documented seam contract requires it |
| Registration | `With*` options consumed by `Run` — no package-level mutable registration, no exported registries |
| Entry point | `Run`, `Config`/`LoadConfig`, `Results`/`ScenarioResult` |

The full symbol inventory with per-symbol justification lives in
[data-model.md](../data-model.md) and is maintained as part of this contract.

## Behavioural contracts

| Behaviour | Contract |
|-----------|----------|
| `Run` reentrancy | fresh sealed composition root per call; sequential and concurrent calls safe (no shared registration state) |
| duplicate registration name | `Run` fails naming the adapter and both registrants |
| post-composition registration | unrepresentable (options only exist at `Run`) |
| ctx cancellation | run-lifecycle (feature 003) semantics; `Results.Interrupted` set |
| CLI equivalence | `Results` status ⇔ CLI exit semantics; CLI is consumer zero of this API |

## Extension author obligations (enforced by review, taught by docs/extending/)

- Errors: wrapped, descriptive, never zero-value success (constitution IV).
- Drivers: apply `RunSpec` correlation tags via their transport (tag-first).
- Stores: return complete forests or loud errors (complete-or-loud).
- Comparators: consume `Evidence` only — no I/O, no store/driver reach-through.
- Judges: classify API errors; never guess a verdict.
