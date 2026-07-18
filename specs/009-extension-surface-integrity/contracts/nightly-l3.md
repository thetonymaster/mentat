# Contract: Nightly L3 stability lane

**Consumers**: 008 SC-001 (20-consecutive-run requirement); the `defaultL3Runs`
doc comment in `e2e/l3runs.go`, which names this lane (T024 synced it — before
009 it described a lane that did not exist); maintainers watching the Actions
page.
**Fulfils**: FR-013, SC-005. Decision: [research.md R5](../research.md).

## Workflow

`.github/workflows/nightly-l3.yml` (added by 009; before it, `ci.yml` was the
repo's only workflow).

| Aspect | Contract |
|---|---|
| Triggers | `schedule:` nightly cron (e.g. `0 3 * * *`) **and** `workflow_dispatch:` |
| Parameterization | job-level env `MENTAT_L3_RUNS: "20"` — consumed by `parseL3Runs` in `e2e/l3runs.go` (via `TestMetaLateFlushNeverGreen`, `e2e/completeness_meta_test.go`); unset default is 3 (`defaultL3Runs`) |
| Steps | mirror `ci.yml`'s e2e job: checkout, setup-go, `make labs`, `docker compose -f deploy/docker-compose.yml up -d` (+ readiness wait as in ci.yml), `go test -tags e2e ./e2e/ -v -parallel 16`, teardown/log-dump on failure |
| Invocation idiom | direct `go test -tags e2e` like `ci.yml:116` — no new make target (none exists for e2e today; do not create a second divergent invocation path) |
| Failure visibility | runs on the default branch; a red run appears on the repo's Actions page like any CI failure (spec edge case: no silent-failure channel) |

## Known limitation: nothing enforces the mirror

`nightly-l3.yml` and `ci.yml`'s e2e job must stay step-for-step identical, and
both files say so in comments — but only a one-time hand-diff at 009 close ever
verified it (parsed both YAMLs, compared step lists: 9 steps, identical). Two
files that must agree, with no mechanism keeping them agreeing, will drift.

The mirror covers the **job's steps only**. Workflow-level settings deliberately
differ: `ci.yml` cancels superseded in-progress runs per PR, while this lane sets
`cancel-in-progress: false` — each nightly run is the SC-001 evidence for its
trigger, so cancelling one would destroy that evidence rather than save time.

Fix when someone touches either lane: extract the e2e job into a reusable
workflow (`workflow_call`) or a composite action parameterised on
`MENTAT_L3_RUNS`, so ci.yml passes 3 and nightly passes 20 into one definition.
Deliberately not done in 009 — it would have meant restructuring the working CI
lane inside a feature about surface integrity. No spec owns this yet; it is
small enough to ride along with whichever feature next edits CI.

## Side effect on code

The `defaultL3Runs` doc comment in `e2e/l3runs.go` used to promise a lane that
did not exist ("The release/nightly lane sets MENTAT_L3_RUNS=20"); T024 rewrote
it to name `.github/workflows/nightly-l3.yml` outright, so it is now true. The
comment and the workflow must agree — if either lane name changes, change both.
Symbol names, not line numbers, are used as anchors here deliberately: the
earlier `l3runs.go:8-10` references in this contract went stale the moment the
comment they pointed at was edited.

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
   - **2b (post-merge, required):** dispatch once on the default branch and
     record the run URL. Watch the dispatched run **by id, with
     `--exit-status`** — `gh workflow run` emits no run id, and a bare
     `gh run watch` exits 0 even when the run fails, so the obvious
     `gh workflow run … && gh run watch` chain would report success on a red
     20-run lane. The full snippet is in [quickstart.md](../quickstart.md) V5.
     Until 2b is done, SC-005 is only partly met. Do not treat the merge as
     closing this.
3. Per-PR CI is untouched (3-run default stays — raising it was rejected in R5).
