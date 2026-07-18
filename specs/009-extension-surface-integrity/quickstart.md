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
latest() { gh run list --workflow nightly-l3.yml --event workflow_dispatch \
             --limit 1 --json databaseId --jq '.[0].databaseId // 0'; }

before=$(latest)
gh workflow run nightly-l3.yml

# Dispatch is async and returns no run id, so poll until a run newer than the
# one we saw pre-dispatch appears. A fixed sleep is a guess; this is not.
for _ in $(seq 30); do
  run_id=$(latest); [ "$run_id" != "$before" ] && break; sleep 2
done
[ "$run_id" = "$before" ] && { echo "dispatch never registered" >&2; exit 1; }

gh run watch "$run_id" --exit-status          # non-zero if the 20-run lane goes red
gh run view "$run_id" --json url --jq .url    # the URL to record
```

Three things this guards, none of them hypothetical:

- **`gh workflow run` emits no run id**, so the id has to be resolved separately.
- **A bare `gh run watch` exits 0 even when the run fails.** The obvious
  `gh workflow run … && gh run watch` chain therefore reports success on a red
  20-run lane — precisely the signal this story exists to surface. `--exit-status`
  is what makes the check real.
- **Resolving "the latest run" can pick the wrong one.** Filtering to
  `--event workflow_dispatch` excludes the 03:00 cron run, and comparing against
  the pre-dispatch id makes sure the run being watched is the one just started
  rather than an earlier dispatch that happens to still be listed first.

## Full gates before PR

```sh
make ci                                       # fmt, vet, tests — source of truth
# coverage floor: run the /coverage skill (≥80% per package)
```

Reminder from the spec: every golden diff in this feature is review surface —
itemize it in the PR body.
