# Quickstart Validation: Public Extension API

Details: [data-model.md](./data-model.md), [contracts/public-surface.md](./contracts/public-surface.md).

## Prerequisites

- Features 003 (sealing) merged; 006 (file store) merged or its file-store
  slice available — the hermetic vehicle for example + library tests.

## Hermetic validation

```sh
go test ./...                      # includes the new facade tests
go test ./ -run TestPublicSurface  # golden surface gate
```

Expected:

- External-style test (`package mentat_test`): drives a feature file through
  `mentat.Run` against the file store; asserts scenario names, verdicts,
  reasons in `Results` (SC-002).
- Duplicate `WithDriver` name → `Run` error naming both registrants.
- Two sequential and two concurrent `Run` calls: independent, race-clean
  (`-race`).
- Surface golden test: current surface matches `public-surface.golden`;
  mutating an exported signature in a scratch branch fails naming the symbol
  (SC-003 rehearsal).

## Example extension (CI job, locally reproducible)

```sh
cd examples/kafkaecho
go build ./... && go test ./...
grep -r "mentat/internal" . && echo "LEAK" || echo "clean"
```

Expected: builds and runs green via `mentat.Run`; grep clean (SC-001).

## CLI equivalence

```sh
mentat run features/ --config mentat.yaml > post.txt
diff pre.txt post.txt   # pre.txt recorded before re-composition
```

Expected: byte-identical (SC-004).

## Docs completeness

`docs/extending/` contains driver, store, comparator, judge guides +
evidence primer; the example follows the driver guide (SC-005 review
checklist at PR).
