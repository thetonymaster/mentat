# Live Performance Baseline (PRE-implementation) — spec 004

**This is the pre-feature baseline for the SC-001 / SC-005 comparison in T017.**
Recorded per research.md R6: 3 sequential runs each, per-run wall time + median,
same machine, dev harness already running. HEAD contains no 004 implementation
code, so these numbers are the true "before".

## Environment

- Date: 2026-07-13
- Git SHA: `38fe1480cde450b1b7bc3ea17408f67d2c35a9d4` (branch `004-correlation-performance`, clean tree)
- Machine: Apple M4 Pro (arm64), 14 cores, macOS (Darwin 25.5.0)
- Harness: already up before measurement (not restarted) — Tempo `/ready` → HTTP 200
  at `localhost:3200`; orderflow `checkout` SUT answering at `localhost:8080/orders`
- Config: repo-root `mentat.yaml` — poll `{interval: 200ms, stableFor: 3, timeout: 30s}`,
  checkout `max_concurrency: 8`

## Measurement 1 — aggregate e2e suite (SC-001)

Command (from repo root), run 3× sequentially:

```sh
/usr/bin/time -p go test -tags e2e ./e2e/ -run TestAggregate -count=1
```

`-run TestAggregate` matches `TestAggregateScalarGoesRed` (the aggregate suite).
Each run printed `ok  github.com/thetonymaster/mentat/e2e  <t>s` (go test pass).

| Run | Wall time (real) |
| --- | ---------------- |
| 1   | 18.5 s           |
| 2   | 19.9 s           |
| 3   | 20.2 s           |

**Median: 19.9 s.** (Run 1 benefited from nothing notable; the go build cache was
already warm from the pre-measurement binary build.)

## Measurement 2 — `@runs(10,parallel)` scenario (SC-005)

Timing fixture (committed, re-run verbatim by T017):
`features/perf/parallel_runs_baseline.feature` — checkout target, scenario
"happy", tag `@runs(10,parallel)`, trivially-true aggregate
`p95(r, r.latencyMs) >= 0` (a PASSING run is what is timed).

Binary built once before timing (any temp path works; this run used the session
scratchpad):

```sh
go build -o "$BIN" ./cmd/mentat
```

One untimed validation run first: exit 0, godog-reported 14.0 s. Then, from the
repo root, 3× sequentially:

```sh
/usr/bin/time -p "$BIN" run features/perf/parallel_runs_baseline.feature
```

Exit code checked per run directly on the mentat process — all 0 (passed).

| Run | Wall time (real) | Exit |
| --- | ---------------- | ---- |
| 1   | 14.6 s           | 0    |
| 2   | 20.0 s           | 0    |
| 3   | 19.9 s           | 0    |

**Median: 19.9 s.** Spread note: run 1 (and the validation run at 14.0 s) came in
~5 s faster than runs 2–3; with `stableFor: 3` at 200 ms the floor per resolve is
~0.8 s of polling, so the variance is dominated by Tempo ingestion timing across
the 10 serialized resolve waits — exactly the cost 004 removes.

## Blockers

None. All 6 timed runs (plus 1 validation run) passed.

---

# Post-implementation (2026-07-13)

T017 measurements. Same machine, same harness (not restarted between baseline
and now; Tempo `/ready` → 200 re-verified). Post-feature code = working tree on
`004-correlation-performance` (HEAD `38fe148` + uncommitted US1–US4
implementation). Pre-feature comparison for SC-005 = a temporary worktree at
HEAD `38fe148` (removed after measurement). Protocol per research.md R6:
3 sequential runs each, median; every timed run verified pass/exit 0. All 13
timed runs (plus 2 untimed validation runs) passed — no failures, no retries.

## SC-001a — aggregate e2e suite

`/usr/bin/time -p go test -tags e2e ./e2e/ -run TestAggregate -count=1`, 3×:

| Run | Wall time (real) | Result |
| --- | ---------------- | ------ |
| 1   | 20.20 s          | ok     |
| 2   | 18.55 s          | ok     |
| 3   | 13.74 s          | ok     |

**Median: 18.55 s** vs pre-feature 19.9 s = **−6.8%. The ≥40% target is MISSED
on this metric.** Explanation (not excuse): `TestAggregateScalarGoesRed` drives
`features/meta/aggregate_scalar_bad.feature`, which is `@runs(2)` —
**sequential**, not parallel. US1's overlap cannot apply to serialized runs by
design, and the wall time is dominated by per-run Tempo ingestion latency plus
the deliberately preserved stability window (`stableFor: 3` × 200 ms). The
audit's cost model ("ingestion wait dominates parallel batches") does not
describe this suite because it is not a parallel batch. Per spec Assumptions,
the 40% number is renegotiated with evidence: the parallel workload SC-001
actually names ("parallel multi-run") is measured next and clears 40%.

## SC-001b — `@runs(10,parallel)` scenario

`features/perf/parallel_runs_baseline.feature` re-run **verbatim** with a
post-feature binary (`go build -o <tmp>/mentat-post ./cmd/mentat`), from the
repo root. Untimed validation run first: exit 0, godog-reported 5.24 s
(pre-feature validation: 14.0 s). Then 3× `/usr/bin/time -p`:

| Run | Wall time (real) | Exit |
| --- | ---------------- | ---- |
| 1   | 6.72 s           | 0    |
| 2   | 10.43 s          | 0    |
| 3   | 10.66 s          | 0    |

**Median: 10.43 s** vs pre-feature 19.9 s = **−47.6%. ≥40% target MET** on the
parallel multi-run workload.

## SC-003 — `mentatctl agent diff` on saved runs

Two saved runs created via `mentatctl agent run --target checkout --scenario
happy` (run ids `51c676af-bfb4-4f44-949d-e22c42ca1678`,
`23f7030a-a3e5-4dee-82d4-2b158d3ef669`). Untimed validation diff: exit 0,
"tool sequences identical". Then 3× `/usr/bin/time -p mentatctl agent diff A B`:

| Run | Wall time (real) | Exit |
| --- | ---------------- | ---- |
| 1   | 0.04 s           | 0    |
| 2   | 0.03 s           | 0    |
| 3   | 0.03 s           | 0    |

**Median: 30 ms total**, *including* network and process startup. Network share
measured directly with curl against the same Tempo instance: `/api/search`
7–12 ms, `/api/traces/{id}` 6–7 ms per run id; diff resolves both ids
concurrently → RTT share ≈ 15–20 ms, leaving **≈ 10–15 ms excluding network**.
**<200 ms target MET** (pre-feature the same command paid ≥2× the stability
poll, ≥1.2 s of sleep alone).

## SC-005 — full e2e suite, pre vs post

No pre-feature full-suite number existed in this file, and HEAD `38fe148` is
pre-feature, so both sides were recorded now, sequentially (never overlapped),
against the same live harness. Build caches warmed symmetrically before timing
(compile-only `-run ZZZNoSuchTest` pass in the pre-feature worktree; the
post-feature tree was already warm). Command both sides: `/usr/bin/time -p go test -tags e2e ./e2e/ -count=1`.

| Run | Pre (worktree @ 38fe148) | Post (working tree) |
| --- | ------------------------ | ------------------- |
| 1   | 60.43 s (ok)             | 62.60 s (ok)        |
| 2   | 59.53 s (ok)             | 59.69 s (ok)        |
| 3   | 61.29 s (ok)             | 60.89 s (ok)        |
| **Median** | **60.43 s**       | **60.89 s**         |

**Post median is +0.46 s (+0.8%) over pre — strictly not ≤ pre.** Honest
reading: the ranges overlap almost completely (pre 59.53–61.29, post
59.69–62.60) and the median gap is smaller than either sample's spread, so
with n=3 this is **equal within run-to-run noise — no evidence of regression,
but also no strict improvement**. Plausible cause: the e2e suite runs its
scenarios under `t.Parallel()`, so its wall clock is set by the critical path
(longest scenarios / deliberate-timeout red paths), not by the summed resolve
waits 004 removed. SC-005 verdict: **equal-within-noise; the "never worse"
clause is not violated by evidence, but the strict median comparison is +0.8%
and is recorded as such.**

## SC-004 — verdict parity (context)

`make ci` (lint + `go test ./... -race` + coverage gate) exited 0 with zero
failures the same day, and all 6 post-feature e2e suite runs above passed —
zero verdict changes observed anywhere in the corpus.

## Summary

| Criterion | Target | Measured | Verdict |
| --------- | ------ | -------- | ------- |
| SC-001 (aggregate suite, `@runs(2)` sequential) | ≥40% drop | −6.8% (19.9 s → 18.55 s) | **MISSED** — workload is sequential; see renegotiation note |
| SC-001 (`@runs(10,parallel)` scenario) | ≥40% drop | −47.6% (19.9 s → 10.43 s) | **MET** |
| SC-003 (diff, excl. network) | <200 ms | ≈10–15 ms (30 ms incl. RTTs) | **MET** |
| SC-004 (verdict parity) | zero changes | `make ci` + 6× e2e green | **MET** |
| SC-005 (full e2e wall clock) | ≤ pre | 60.43 s → 60.89 s (+0.8%, within noise) | **EQUAL-WITHIN-NOISE** (strict median +0.8%) |
