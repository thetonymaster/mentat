# Quickstart Validation: Verdict Integrity

How to prove the feature works end-to-end. Details: [data-model.md](./data-model.md),
[contracts/evidence-vocabulary.md](./contracts/evidence-vocabulary.md).

## Prerequisites

- Go 1.25, repo checked out, `make ci` green before starting.
- For e2e: Docker running, `make harness-up` (deploy/ Tempo + collector stack).

## Hermetic validation (no network)

```sh
go test ./internal/store/ ./internal/correlate/ ./internal/engine/ \
        ./internal/comparator/ ./internal/report/ ./internal/steps/
```

Expected: the eight red→green pairs (one per audit finding A1–A8) pass, including:

- store: `STATUS_CODE_ERROR`→`Error` normalization, unknown-spelling decode error,
  kind population, search `limit` + full-page truncation error, fixture
  `parentIndex` rejection table.
- correlate: deadline-with-unstable-spans → hard error; query-tag pin test
  (`test.run.id`, F3).
- engine: resolve-failure Evidence retains real Output + `FailureMsg`.
- comparator: `errorCount` on canonical status; aggregate hard error on
  driver-failed sample boundary reference.
- steps: single-run drive failure fails the scenario with the root cause;
  assertion-free scenario goes red on drive failure.
- report: derivation degradation → `DerivationNote`, verdict unchanged.

## Live-harness validation (e2e)

```sh
make harness-up
go test -tags e2e ./e2e/ -run TestL3 -v
```

Expected additions to the meta suite:

- **Error-status L3 (F5)**: orderflow error-mode scenario drives a SUT that emits
  one errored span; the feature asserting `no span has status "ERROR"` exits
  non-zero with a reason containing the errored span — proving A1 is dead on live
  Tempo, not just on fixtures.
- All pre-existing meta tests still red-on-bad, green-on-good (SC-004).

## Coverage gate

```sh
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -5
```

Expected: every touched package ≥ 80% (SC-005).

## Manual smoke (optional)

```sh
mentat run features/ --config mentat.yaml   # healthy SUT: verdicts unchanged
# break the target command in mentat.yaml, rerun:
# → scenario fails, output contains the driver's real error (not "got 0")
```
