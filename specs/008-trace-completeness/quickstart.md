# Quickstart Validation: Trace Completeness Contract

Runnable proof that the feature works end-to-end. Details live in
[data-model.md](./data-model.md) and
[contracts/completeness-contract.md](./contracts/completeness-contract.md).

## Prerequisites

- Feature 002 (verdict integrity) merged — the hardened stability gate this
  feature builds on.
- Live harness stack: `make harness-up` (Tempo + OTel Collector from `deploy/`).
- `go build ./...` clean; mocks regenerated after the seam change
  (`go generate ./...`).

## 1. Hermetic proof (no infra)

```sh
go test ./internal/correlate/ ./internal/config/ ./internal/engine/ ./internal/report/
```

Expected: PASS, including new table-driven cases for settle-window termination,
strict sentinel state machine (missing / dup / short / exceeded / exact), config
validation errors, qualifier attachment, and the drive-before-resolve ordering
pin. Coverage stays ≥80% per touched package (`/coverage` skill).

## 2. Late-flush L3 proof (the reason this feature exists)

```sh
go test -tags e2e ./e2e/ -run TestMeta -v
```

Expected: the `late-flush` meta-scenario asserts that a scenario declaring
`the tool "delete_record" is never called` against the late-flushing researchbot
goes **RED with a comparator failure on the complete forest** — the forbidden
tool is found because resolution waited out the barrier. Repeat check
(SC-001): the meta-test loops the scenario and requires 0 green outcomes.

## 3. Strict-mode proofs

```sh
go test -tags e2e ./e2e/ -run TestStrict -v
```

Expected:
- `sentinel-good`: verdicts normal, no qualifier in report output.
- `sentinel-short`: run ends in a hard error naming run id, declared count,
  observed count, elapsed — exit code non-zero, no verdict.
- `sentinel-dup`: hard error naming both sentinel span ids.

## 4. Qualifier visibility (request-scoped honesty)

```sh
mentat run features/checkout.feature --config mentat.yaml
```

With the orderflow http target (non-strict): every absence/count/budget verdict
in console and JUnit output carries
`trace-completeness: bounded by ingestion window (settle 5s)…` — on pass and
fail alike. Switch the target to `completeness: {mode: strict}` (orderflow
sentinel scenario) and the qualifier disappears.

## 5. Overhead check (SC-005)

Time a well-behaved researchbot scenario before and after the feature branch:
added wall-clock ≤ the spawned settle default (2s), and typically ~0 because the
settle window overlaps ingestion polling that already happens.

## Success = all five sections green

Anything else is a blocker: section 2 failing green means the barrier is not
sound; section 3 producing verdicts means strict mode leaks partial evidence;
section 4 missing qualifiers means the honesty requirement (FR-004) regressed.
