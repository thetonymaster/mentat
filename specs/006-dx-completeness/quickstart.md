# Quickstart Validation: DX & Product Completeness

Details: [data-model.md](./data-model.md), [contracts/](./contracts/).

## Per-slice hermetic validation

```sh
go test ./internal/steps/ ./cmd/mentat/ ./internal/store/ ./internal/judge/ \
        ./internal/comparator/ ./internal/report/ ./internal/config/ \
        ./internal/core/ ./internal/ctl/ ./cmd/mentatctl/
```

Expected highlights per slice:

- **E1** drift test: every registered step has metadata; `docs/steps.md`
  regeneration-clean; count matches registration table.
- **E2** validate: seeded corpus (unbound step, bad CEL, unknown target, bad
  shape pattern) → 4 findings, one run, exit 1; clean corpus exit 0; no network
  possible (noop store/driver by construction).
- **E3**: run with `--junit` produces file + non-empty console (captured).
- **E4**: body doc-string and fixture steps set the request body verbatim
  (httptest server asserts); missing fixture fails naming resolved path.
- **E5** file store: replay of a saved fixture yields identical verdicts with
  no network; absent id errors; `@runs(2)` hard-errors.
- **E6**: ledger flows judge→matcher→report (fake judge with fixed usage);
  1-cent budget aborts multi-call suite naming spent/budget/scenario;
  votes>1@temp0 config fails at load; default model constant is fast tier.
- **E7**: mentatctl summary golden includes tokens/cost/latency/traces;
  prompt-file/stdin/-o/--timeout table.
- **E8**: extraction table — whole (unchanged), marker last-occurrence, marker
  absent → failure naming marker, pattern capture, no-match → failure naming
  pattern.
- **E9**: `make labs` builds binaries; rebuild-on-source-change check; the two
  report-meta tests use `mentatBin` and `t.Parallel()` (convention grep +
  suite passes).

## Offline replay proof (no infrastructure)

```sh
make harness-down   # docker stopped on purpose
mentat run features/smoke.feature --config mentat-file-store.yaml
```

Expected: green verdicts from the saved fixture, zero network.

## Doc walkthrough (once, at PR review)

Author a new feature file using only `mentat steps` / `docs/steps.md` (SC-001).

## Regression gate

```sh
make ci   # zero verdict changes (SC-008); coverage floor holds
```
