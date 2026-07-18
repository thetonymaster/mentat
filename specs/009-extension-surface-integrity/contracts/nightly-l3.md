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
2. One **manually dispatched** run has completed green at the 20-run setting.

   **Amended 2026-07-18 (T025) — criterion 2 cannot be met before merge.**
   Attempting it returned:

   ```
   HTTP 404: workflow nightly-l3.yml not found on the default branch
   ```

   GitHub's `workflow_dispatch` API resolves workflows only on the **default
   branch**, so a workflow can never be dispatched from the branch that
   introduces it. The original wording ("before the feature closes") is
   therefore unsatisfiable for any new workflow, and this is a platform
   constraint, not a defect in the lane.

   Split into 2a and 2b:
   - **2a (pre-merge, substantive):** the 20-run threshold is proven locally via
     the quickstart's documented equivalent —
     `MENTAT_L3_RUNS=20 go test -tags e2e ./e2e/ -v -parallel 16` against the
     `deploy/` harness. This exercises the real gate; what it does not exercise
     is the workflow YAML on a runner.
   - **2b (post-merge, required):** dispatch once on the default branch —
     `gh workflow run nightly-l3.yml && gh run watch` — and record the run URL.
     Until 2b is done, SC-005 is only partly met. Do not treat the merge as
     closing this.
3. Per-PR CI is untouched (3-run default stays — raising it was rejected in R5).
