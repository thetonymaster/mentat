# Contract: Nightly L3 stability lane

**Consumers**: 008 SC-001 (20-consecutive-run requirement); `e2e/l3runs.go:8-10`
(comment that currently describes a lane that does not exist);
maintainers watching the Actions page.
**Fulfils**: FR-013, SC-005. Decision: [research.md R5](../research.md).

## Workflow

`.github/workflows/nightly-l3.yml` (new — `ci.yml` is today the repo's only
workflow).

| Aspect | Contract |
|---|---|
| Triggers | `schedule:` nightly cron (e.g. `0 3 * * *`) **and** `workflow_dispatch:` |
| Parameterization | job-level env `MENTAT_L3_RUNS: "20"` — consumed by `e2e/l3runs.go:19-30` (`TestMetaLateFlushNeverGreen`, `e2e/completeness_meta_test.go:31`); unset default is 3 (`l3runs.go:11`) |
| Steps | mirror `ci.yml`'s e2e job: checkout, setup-go, `make labs`, `docker compose -f deploy/docker-compose.yml up -d` (+ readiness wait as in ci.yml), `go test -tags e2e ./e2e/ -v -parallel 16`, teardown/log-dump on failure |
| Invocation idiom | direct `go test -tags e2e` like `ci.yml:116` — no new make target (none exists for e2e today; do not create a second divergent invocation path) |
| Failure visibility | runs on the default branch; a red run appears on the repo's Actions page like any CI failure (spec edge case: no silent-failure channel) |

## Side effect on code

`e2e/l3runs.go:8-10`'s comment ("The release/nightly lane sets
MENTAT_L3_RUNS=20") becomes true. If wording drifts from the final lane name,
update the comment — the comment and the workflow must agree.

## Acceptance

1. Workflow file exists with both triggers and the env pinning.
2. One **manually dispatched** run has completed green at the 20-run setting
   before the feature closes (recorded by run URL in the PR body).
3. Per-PR CI is untouched (3-run default stays — raising it was rejected in R5).
