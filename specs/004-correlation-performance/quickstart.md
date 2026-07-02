# Quickstart Validation: Correlation Performance

Details: [data-model.md](./data-model.md), [contracts/resolution-modes.md](./contracts/resolution-modes.md).

## Prerequisites

- Feature 002 merged (its resolve error contracts are the baseline).
- **Before implementing**: record the live baseline —
  `time go test -tags e2e ./e2e/ -run TestAggregate` (3 runs, median) and note it
  in `baseline.md` here.

## Hermetic validation

```sh
go test -race ./internal/correlate/ ./internal/engine/ ./internal/comparator/ ./internal/ctl/
```

Expected:

- correlate: counting-store tests — ≤1 decode per trace per resolution; N-round
  poll performs N cheap checks; changed-payload resets stability; unstable
  deadline still hard-errors (002 regression guard); `ResolveComplete` performs
  zero sleeps and one fetch pass; fan-out fetch error contract preserved.
- engine: delayed-store overlap test — `@runs(10, parallel)` with 300ms lag
  completes well under serialized time; per-target limit still serializes the
  SUT-execution phase (drive-order assertion).
- comparator: compile-counter — 1 compilation per expectation over 500 matched
  spans; verdicts identical to pre-change goldens; `-race` clean.
- ctl: replay/format/diff use known-complete mode (mock asserts no `Resolve`
  stability calls); diff resolves both runs concurrently.

## Live measurement (evidence, not merge gate)

```sh
make harness-up
time go test -tags e2e ./e2e/ -run TestAggregate   # 3 runs, median vs baseline: expect ≥40% drop
time mentatctl agent diff <saved-run-1> <saved-run-2>   # expect < 200ms + RTTs
go test -tags e2e ./e2e/                                # full suite: wall clock ≤ baseline
```

## Verdict-preservation gate

```sh
make ci   # entire unit + e2e corpus: zero verdict changes (SC-004)
```
