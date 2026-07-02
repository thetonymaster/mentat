# Quickstart Validation: Observability & Config Integrity

Details: [data-model.md](./data-model.md), [contracts/narration-and-errors.md](./contracts/narration-and-errors.md).

## Hermetic validation

```sh
go test ./internal/config/ ./internal/engine/ ./internal/correlate/ \
        ./internal/driver/ ./internal/steps/ ./cmd/mentat/ ./cmd/mentatctl/
```

Expected:

- config: typo-per-section table (root/poll/judge/targets/reporters) — each fails
  naming the key; valid config unchanged.
- engine: phantom adapter fails Build naming registered set; empty endpoint not
  injected; buffer-logger tests pin `drive.start`/`resolve.done` attributes;
  silent default emits zero log bytes (golden).
- correlate: timeout error contains `store:`, `query:`, `checklist:`; poll
  narration at Debug only; BuildCorrelator defaults table (200ms/30s/3).
- driver: resource-attr merge table — ambient+Mentat merge, Mentat wins
  collision, malformed ambient hard-errors; env echo covers only Mentat-set keys.
- steps: overflow ordinal fails naming the value.
- cmd: `-v`/`-vv` flag mapping; both binaries share BuildCorrelator (their local
  parseDur/orDefault helpers are gone — compile is the proof).

## Scripted diagnosis walk (e2e)

```sh
make harness-down   # collector dead on purpose
go test -tags e2e ./e2e/ -run TestDiagnosis -v
```

Expected: `mentat run` against the dead stack exits red; stderr of the failure
contains endpoint, query, and checklist; rerun with `-vv` shows injected env and
poll rounds. (Test asserts the substrings; the "diagnosable by a human" claim is
reviewed once at PR time.)

## Regression gates

```sh
make ci                        # zero verdict changes (SC-006)
mentat run features/ > out.txt # healthy run, default verbosity:
diff out.txt golden.txt        # byte-identical stdout (SC-005)
```
