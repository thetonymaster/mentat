# Contract: Judge Ledger, Budget, and Defaults

## Report fields (JSON; HTML renders the same data)

```json
{
  "scenarios": [{
    "name": "...",
    "judge": { "calls": 3, "inputTokens": 1250, "outputTokens": 90,
               "costUsd": 0.0041, "model": "<judge model id>" }
  }],
  "judgeTotal": { "calls": 12, "inputTokens": 5000, "outputTokens": 360,
                  "costUsd": 0.0164 }
}
```

- `judge` present only on scenarios that made judge calls; `judgeTotal` present
  when any did. Absence of judge usage ≠ zeros (no fabricated values).
- Cost uses the existing pricing-table rules; unknown/ambiguous model pricing is
  a hard error (never $0.00 for a real call).

## Budget semantics (`judge.max_cost_usd`)

- Optional; unset = unlimited (today's behaviour).
- Accounting: completed calls only; checked after each scenario; in-flight votes
  finish, no new judge call starts once exceeded.
- Trip: suite aborts with error naming spent, budget, and the scenario that
  crossed it; exit non-zero; reports still emit (with the ledger).

## Defaults policy

- Default judge model: fast tier (Haiku-class; exact id pinned in one config
  constant at implementation, chosen against the live price sheet and the
  existing capability allowlist). Accuracy upgrade = one config line, documented
  next to the constant and in README.
- `votes > 1` with `temperature: 0` → **config load error**:
  `judge: votes=3 with temperature=0 sends near-identical calls; raise temperature (e.g. 0.7) or set votes: 1`.
- L3 semantic meta-tests must pass under the new default (fake judge is
  model-agnostic; the live e2e pins behaviour, not model id).

## Cost target

Fast-tier vs current top-tier list pricing must show ≥80% per-step default cost
reduction (SC-006), recorded in the PR description with the price-sheet math.
