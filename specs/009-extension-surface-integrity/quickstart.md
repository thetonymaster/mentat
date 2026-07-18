# Quickstart: validating Extension-Surface Integrity (009)

Runnable proof that each of the five closures works. Details live in the
[contracts](./contracts/) — this page only drives them.

## Prerequisites

- Go 1.25, repo root `github.com/thetonymaster/mentat`
- Docker + compose (only for V5 / e2e), `gh` CLI (only for V5 dispatch)
- Hermetic: V1–V4 need no network

## V1 — Surface gate freezes struct fields ([contract](./contracts/surface-golden-v2.md))

```sh
go test ./ -run TestPublicSurface -v          # expect PASS on a clean tree
```

Mutation rehearsal (the acceptance proof; also documented in `surface_test.go`):

```sh
# 1. add a throwaway exported field to Verdict in internal/core/core.go, e.g. `XProbe bool`
go test ./ -run TestPublicSurface -v          # expect FAIL, output names Verdict
# 2. revert the field
go test ./ -run TestPublicSurface -v          # expect PASS again
```

Expected golden state: `specs/007-public-extension-api/contracts/public-surface.golden`
contains field lines for every aliased struct — including `Qualifiers` under
`Verdict` and `Completeness` under `Target` (the two previously invisible drifts).
Deliberate regeneration (only when a surface change is intended):

```sh
MENTAT_UPDATE_GOLDEN=1 go test ./ -run TestPublicSurface && git diff -- specs/007-public-extension-api/contracts/public-surface.golden
```

## V2 — YAML/code config parity ([contract](./contracts/config-resolve.md))

```sh
go test ./internal/config/ -run 'Resolve|Parity' -v
```

Expect the parity table green: every row's YAML-loaded and struct-literal
configs produce deep-equal effective contracts (completeness mode+settle, budget,
judge, extract policy) or identical descriptive errors; includes the
double-`Resolve` idempotency row and both divergence-suspect rows.

Full-path check (library entry): `go test ./ -run Run` — `mentat.Run` with a
code-built Config now resolves before composing (kafkaecho exercises this too,
see V3).

## V3 — Facade nameability ([contract](./contracts/facade-nameability.md))

```sh
go test ./ -run TestFacadeSurfaceExercisesContractTypes -v   # facade-only composite-literal compile test
( cd examples/kafkaecho && go build ./... && go vet ./... )   # external module still compiles
make example                                  # Makefile:32 polices internal imports in examples/
```

The compile test compiling IS the proof; `mentat.Completeness` must be nameable.

## V4 — Seam guide + taxonomy ([contract](./contracts/seam-taxonomy.md))

```sh
ls docs/extending/new-seam.md
grep -n "new-seam" internal/registry/registry.go specs/007-public-extension-api/contracts/public-surface.md
grep -rn "AggregateComparator" docs/extending/
```

Expect: the guide exists with the checklist + three decisions + canonical
taxonomy; both old sites reference it; the internal-only exclusion sentence hits.

## V5 — Nightly L3 lane ([contract](./contracts/nightly-l3.md))

**Pre-merge** — the local equivalent is the only form available (long: 20
meta-runs against live Tempo). This exercises the real 20-run gate; what it does
not exercise is the workflow YAML on a runner:

```sh
make harness-up
MENTAT_L3_RUNS=20 go test -tags e2e ./e2e/ -v -parallel 16
```

**Post-merge, still required** — `workflow_dispatch` resolves workflows only on
the **default branch**, so a new workflow can never be dispatched from the branch
that introduces it (attempting it returns `HTTP 404: workflow nightly-l3.yml not
found on the default branch`). Once merged, dispatch once and record the run URL;
SC-005 is only partly met until then:

```sh
gh workflow run nightly-l3.yml
sleep 5   # the run is not queryable the instant dispatch returns
run_id=$(gh run list --workflow nightly-l3.yml --event workflow_dispatch \
           --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$run_id" --exit-status          # non-zero if the 20-run lane goes red
gh run view "$run_id" --json url --jq .url    # the URL to record
```

`gh workflow run` prints no run ID, and a bare `gh run watch` exits **0 even when
the run fails** — so the naive `gh workflow run … && gh run watch` reports success
on a red lane, which is precisely the signal this story exists to surface. Resolve
the ID explicitly and pass `--exit-status`. The `--event workflow_dispatch` filter
keeps the nightly cron run from being picked up instead.

## Full gates before PR

```sh
make ci                                       # fmt, vet, tests — source of truth
# coverage floor: run the /coverage skill (≥80% per package)
```

Reminder from the spec: every golden diff in this feature is review surface —
itemize it in the PR body.
