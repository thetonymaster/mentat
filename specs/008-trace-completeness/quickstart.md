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
tool is found because resolution waited out the barrier.

SC-001 repeat proof (the same gate, tuned): the meta-test loops the scenario
`MENTAT_L3_RUNS` times (unset → 3) and requires 0 green outcomes. Machine-enforce
the 20-run threshold with:

```sh
MENTAT_L3_RUNS=20 go test -tags e2e ./e2e/ -run TestMeta -v
```

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
mentat run features/checkout.feature --config mentat.yaml --junit /tmp/orderflow.xml
```

With the orderflow http target (non-strict): every absence/count/budget verdict
in the emitted report (`--junit` here; likewise `--report-json` / `--report-html`)
carries `trace-completeness: bounded by ingestion window (settle 5s)…` — on pass
AND fail alike. The live console surfaces reasons only on a failing step, so the
pass-side qualifier lives in the report files, not stdout (see SC-003). Switch the
target to `completeness: {mode: strict}` (orderflow sentinel scenario) and the
qualifier disappears.

## 5. Overhead check (SC-005)

Time a well-behaved researchbot scenario before and after the feature branch:
added wall-clock ≤ the spawned settle default (2s), and typically ~0 because the
settle window overlaps ingestion polling that already happens.

## Success = all five sections green

Anything else is a blocker: section 2 failing green means the barrier is not
sound; section 3 producing verdicts means strict mode leaks partial evidence;
section 4 missing qualifiers means the honesty requirement (FR-004) regressed.
