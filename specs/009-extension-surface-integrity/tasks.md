# Tasks: Extension-Surface Integrity

**Input**: Design documents from `/specs/009-extension-surface-integrity/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md (all present)

**Tests**: This project's constitution mandates Test-First / TDD as NON-NEGOTIABLE (Principle V). Test tasks are REQUIRED for US1–US3 (behaviour changes) and each test MUST be written to FAIL before its implementation. US4–US5 are docs/CI (go-coder) with verification steps instead of unit tests.

**Organization**: Tasks are grouped by user story. Routing per constitution: US1–US3 → **go-test-writer**; US4–US5 → **go-coder**; pre-PR audit → **go-reviewer** (gate).

**Conventions that bind every task**: stage files individually (never `git add .`), Conventional Commits, no AI attribution, `gofmt`/`go vet` clean before each commit, every golden diff itemized in the PR body.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1–US5)

## Phase 1: Setup

**Purpose**: Branch, commit the planning artifacts, verify a green baseline.

- [X] T001 Create branch `009-extension-surface-integrity` from `main`; commit the planning artifacts individually: all files under `specs/009-extension-surface-integrity/` plus the SPECKIT block update in `CLAUDE.md` (`docs(009): spec + plan artifacts`)
- [X] T002 Record the green baseline on the branch: `make ci` passes and `go test ./ -run TestPublicSurface -v` PASSES at the branch point; if either is red, STOP and surface to Q before any 009 work (output recorded in the task checkpoint, not assumed)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: None required — the five stories share no new infrastructure; all evidence anchors were re-verified at `2f4073d` during planning ([research.md R0](./research.md)). This phase is intentionally empty.

**Checkpoint**: After T002, user stories can begin (see Dependencies for the one ordering constraint: US3 starts after US1's golden regen).

---

## Phase 3: User Story 1 - Struct-field drift cannot pass the surface gate (Priority: P1) 🎯 MVP

**Goal**: The golden renderer expands exported field sets of aliased structs; the two previously invisible drifts (`Verdict.Qualifiers`, `Target.Completeness`) become frozen golden lines; stability.md's strong claim is restored. Contract: [contracts/surface-golden-v2.md](./contracts/surface-golden-v2.md).

**Independent Test**: Mutation rehearsal — add an exported field to `core.Verdict` → `TestPublicSurface` FAILS naming the type; revert → PASS (quickstart V1).

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

- [X] T003 [P] [US1] RED: add a table-driven renderer unit test in `surface_test.go` asserting struct-alias expansion: `Verdict` row includes a `Qualifiers []string` field line, `Target` row includes `Completeness Completeness`, `ExtractConfig` row EXCLUDES the unexported `compiled`, and a map alias (`Pricing`) stays single-line. Run `go test ./ -run <new test name> -v` — VERIFY: FAIL (renderer doesn't expand structs yet); record the failure output

### Implementation for User Story 1

- [X] T004 [US1] GREEN: implement struct expansion in `surface_test.go` beside the T028 interface machinery — `indexStructs` (parallel to `indexInterfaces` :313-346), `lookupStruct` (parallel to `lookupInterface` :296-309), `renderStructFields` (parallel to `renderInterfaceMethods` :356-380); exported fields only, types printed as written (`go/printer` mode 0), embedded fields as written; reuse `surfaceCtx` dir resolution untouched. VERIFY: T003's test PASSES; `TestPublicSurface` now FAILS against the stale golden (expected — proves the gate sees fields)
- [X] T005 [US1] Regenerate the golden once: `MENTAT_UPDATE_GOLDEN=1 go test ./ -run TestPublicSurface`; VERIFY in `specs/007-public-extension-api/contracts/public-surface.golden`: (a) `Qualifiers` appears under `Verdict`, `Completeness` under `Target`; (b) every pre-existing interface method-set line is byte-identical (no-regression: `git diff` shows only added field lines); capture the itemized diff summary for the PR body; commit test + renderer + golden together
- [X] T006 [US1] Mutation rehearsal, documented: temporarily add `XProbe bool` to `Verdict` in `internal/core/core.go` → VERIFY `go test ./ -run TestPublicSurface` FAILS naming `Verdict`; revert → VERIFY PASS; commit a dated rehearsal narrative comment block in `surface_test.go` beside the T014/T028 blocks (:42-52/:54-70), quoting the observed failure text
- [X] T007 [US1] Restore the strong claim in `docs/extending/stability.md`: delete the interim-gap section (:53-84), state that exported fields of aliased structs are frozen by the golden, and state the documented scope boundary (map/func/`any` aliases stay single-line per contract); VERIFY the doc no longer references spec 009 as "planned"

**Checkpoint**: quickstart V1 fully green; US1 is shippable alone (MVP).

---

## Phase 4: User Story 2 - Code-built configuration behaves identically to file-loaded (Priority: P2)

**Goal**: `config.Resolve` extracted from `Load` (story (a), [research.md R2](./research.md)), called idempotently at the top of `mentat.Run` before `BuildCorrelator`/`BuildStore`/`Build`; all 13 inventory behaviours path-independent; divergence suspects settled by test. Contract: [contracts/config-resolve.md](./contracts/config-resolve.md).

**Independent Test**: Parity table green — same logical config via YAML `config.Load` vs struct literal `config.Resolve` → deep-equal effective contracts or identical descriptive errors (quickstart V2).

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [X] T008 [P] [US2] RED: add a test in `internal/config/config_test.go` calling `config.Resolve(&cfg)` on a struct-literal Config with a shell target and empty completeness, asserting kind-default settle 2s and mode default are applied. VERIFY: does not compile / FAILS (`Resolve` undefined); record output

### Implementation for User Story 2

- [X] T009 [US2] GREEN: extract the post-decode half of `config.Load` (`internal/config/config.go:198-298` resolution/validation body) into `func Resolve(c *Config) error`; `Load` becomes read + strict decode + `Resolve`. VERIFY: T008 PASSES and the entire existing `internal/config` test suite stays green (`go test ./internal/config/ -v`) — this step is behaviour-preserving for the YAML path
- [X] T010 [US2] RED→GREEN one law at a time, table-driven in `internal/config/config_test.go`: (a) idempotency — `Resolve` twice leaves the config deep-equal to once; (b) explicit-value-wins — non-zero `Completeness.Settle` set in code with empty `SettleRaw` is NOT overwritten by the kind default; (c) twin conflict — non-empty `SettleRaw` that parses to a value conflicting with a simultaneously-set non-zero `Settle` is a hard error naming both fields; apply the same three laws to `Target.Budget` vs its raw/timeout source (`config.go:265-269`). VERIFY each row red before its guard is implemented in `resolveCompleteness`/budget resolution
- [X] T011 [US2] Complete the parity table in `internal/config/config_test.go` per the contract's proof obligation: one row per inventory behaviour — defaults #1,3,4,5,6,8,10,12 and hard errors #2,7,11,13 — each row expressed as YAML fixture through `Load` AND struct literal through `Resolve`, asserting deep-equal effective contracts (including `ExtractConfig.Policy()` non-nil for #9) or identical error text. VERIFY: all rows green; any row revealing an unfixed divergence gets its fix inside this task (one failing row at a time)
- [X] T012 [US2] Settle the two divergence suspects by test, not assumption ([research.md R0](./research.md)): (a) zero `Target.Budget` semantics on the code path (what `Drive` does with Timeout 0/KillGrace 0 vs the Load-resolved budget); (b) `validateJudge`'s Load-only temperature-pairing and `MaxCostUSD` rules (`config.go:312-324`) now applying via `Resolve`. Add parity rows for both; VERIFY outcome recorded in test comments (confirmed-divergence-fixed or refuted-with-evidence)
- [X] T013 [US2] RED: add a facade-level test in root `run_test.go` (or extend the existing Run tests): `mentat.Run` with a code-built `Config{Store:"file"}` and no storePath must return the descriptive storePath error (Load behaviour #2) before driving anything. VERIFY: FAILS (Run never resolves today); record output
- [X] T014 [US2] GREEN: call `config.Resolve` at the top of `mentat.Run` in `run.go`, before `BuildCorrelator` (:293), `BuildStore` (:310), and `Build` (:349), wrapping errors as `fmt.Errorf("resolving config: %w", err)`. VERIFY: T013 PASSES; full root-package suite green; `cmd/mentat` + `cmd/mentatctl` tests green (CLI double-resolution exercises idempotency)
- [X] T015 [US2] Truth sweep: correct the now-false comment at `internal/engine/engine.go:73-74` ("kind-defaulted at config load" → resolved on both paths via config.Resolve); VERIFY `examples/kafkaecho` still compiles (`cd examples/kafkaecho && go build ./... && go vet ./...`); VERIFY `internal/config` coverage ≥80% via the `/coverage` skill

**Checkpoint**: quickstart V2 fully green; US1 + US2 independently shippable.

---

## Phase 5: User Story 3 - Every reachable public type is nameable from the facade (Priority: P3)

**Goal**: `config.Completeness` aliased on the facade; the whole reachable set swept; nameability frozen by a facade-only compile test. Contract: [contracts/facade-nameability.md](./contracts/facade-nameability.md). **Starts after US1 T005** (both stories regenerate the golden — keep each regen a clean, story-scoped diff).

**Independent Test**: The compile test compiles using only the `mentat` package; `mentat.Completeness{...}` is a legal composite literal (quickstart V3).

### Tests for User Story 3 (REQUIRED — Test-First) ⚠️

- [X] T016 [US3] RED: extend `mentat_external_test.go` (facade-only imports, precedent :63-68) with a composite literal for the completeness type on `mentat.Target` — `Target{Completeness: mentat.Completeness{Mode: "strict"}}`. VERIFY: compile FAILS (`mentat.Completeness` undefined); record output

### Implementation for User Story 3

- [X] T017 [US3] GREEN: add `type Completeness = config.Completeness` to `mentat.go` (doc comment in facade style); VERIFY T016 compiles and passes; regenerate the golden (`MENTAT_UPDATE_GOLDEN=1`) — VERIFY the diff is exactly the new alias + its expanded field lines (US1 renderer at work); itemize for the PR body
- [X] T018 [US3] Sweep the reachable set: walk exported struct field types transitively from `mentat.Config` and `mentat.Results` (slice/map/pointer elements and embedded types included); for EVERY reachable exported struct add a composite literal (each setting ≥1 field) to the compile test in `mentat_external_test.go`; alias any further reachable-unnameable type found (each new alias = golden regen line, itemized). VERIFY: test compiles facade-only; record the swept type list in a test comment as the sweep's evidence
- [X] T019 [US3] External-module witness: VERIFY `cd examples/kafkaecho && go build ./... && go vet ./...` and `make example` (internal-import policing, `Makefile:32`) both pass untouched

**Checkpoint**: quickstart V3 fully green; SC-002 met.

---

## Phase 6: User Story 4 - Seam addition without tribal knowledge (Priority: P4)

**Goal**: `docs/extending/new-seam.md` as canonical taxonomy + checklist; both divergent sites reference it; AggregateComparator exclusion documented. Contract: [contracts/seam-taxonomy.md](./contracts/seam-taxonomy.md). Routing: **go-coder**.

**Independent Test**: quickstart V4 greps — guide exists, both sites reference it, exclusion sentence hits.

### Implementation for User Story 4

- [X] T020 [P] [US4] Create `docs/extending/new-seam.md` from the contract: the reconciled 8-row taxonomy table (registration style, sealing, public hook per seam), the mandatory AggregateComparator internal-only exclusion sentence (verbatim requirement in the contract), one "how to choose" paragraph per tribal decision (instance-vs-factory, per-engine-vs-package-global with the reporters exception, collision-check-before-construction), and the ~10-touchpoint checklist
- [X] T021 [US4] Point both old sites at the canonical doc: rewrite the `internal/registry/registry.go:21-22` doc comment (registry-ownership axis + "see docs/extending/new-seam.md") and the `specs/007-public-extension-api/contracts/public-surface.md:19` seam sentence (public-hook axis + same reference); VERIFY `go build ./...` (comment-only change) and that neither site still states its own divergent list
- [X] T022 [US4] VERIFY quickstart V4 exactly as written: `ls docs/extending/new-seam.md`; `grep -n "new-seam" internal/registry/registry.go specs/007-public-extension-api/contracts/public-surface.md` (both hit); `grep -rn "AggregateComparator" docs/extending/` (exclusion sentence hits); record outputs

**Checkpoint**: SC-004 met; US4 independent of all other stories.

---

## Phase 7: User Story 5 - Nightly L3 lane (Priority: P5)

**Goal**: `.github/workflows/nightly-l3.yml` enforcing `MENTAT_L3_RUNS=20` on schedule + dispatch; the `e2e/l3runs.go` comment becomes true. Contract: [contracts/nightly-l3.md](./contracts/nightly-l3.md). Routing: **go-coder**.

**Independent Test**: workflow file has both triggers + env pinning; one dispatched run green (quickstart V5).

### Implementation for User Story 5

- [X] T023 [P] [US5] Create `.github/workflows/nightly-l3.yml`: `schedule: cron '0 3 * * *'` + `workflow_dispatch`; job env `MENTAT_L3_RUNS: "20"`; steps mirroring `ci.yml`'s e2e job verbatim (checkout, setup-go, `make labs`, `docker compose -f deploy/docker-compose.yml up -d` + the same readiness wait, `go test -tags e2e ./e2e/ -v -parallel 16`, the same teardown/log-dump on failure); no new make target (contract: don't create a second divergent invocation path)
- [X] T024 [US5] Sync the `defaultL3Runs` doc comment in `e2e/l3runs.go` to name the actual lane (`nightly-l3.yml`); VERIFY `go vet ./e2e/` (comment-only) and `grep -n "nightly" e2e/l3runs.go .github/workflows/nightly-l3.yml` agree on the name
- [X] T025a [US5] Prove the 20-run threshold pre-merge via the local equivalent (`MENTAT_L3_RUNS=20 go test -tags e2e ./e2e/ -v -parallel 16` against `make harness-up`). Dispatching on the feature branch was ATTEMPTED and is IMPOSSIBLE: `gh workflow run nightly-l3.yml --ref 009-extension-surface-integrity` returns `HTTP 404: workflow nightly-l3.yml not found on the default branch` — GitHub resolves `workflow_dispatch` only on the default branch, so a new workflow can never be dispatched from the branch introducing it. Contract amended (see contracts/nightly-l3.md acceptance 2a/2b)
- [ ] T025b [US5] **POST-MERGE, still required**: `gh workflow run nightly-l3.yml && gh run watch` on the default branch; VERIFY green at 20 runs; record the run URL. SC-005 is only PARTLY met until this is done — merging does not close it. If the dispatched run is red, STOP and diagnose per RULE 0 before rerunning (a red 20-run lane is exactly the signal this story exists to surface)

**Checkpoint**: SC-005 **PARTLY met, and stays partly met until T025b**. The workflow exists and the 20-run threshold is proven locally (T025a); the dispatched-run half is post-merge by platform constraint. Do not read the merge as closing this.

---

## Phase 7b: Regression introduced by US2 (unplanned — found by T014's own verification)

**Why this exists**: `config.Resolve` writes into `c.Targets` (`internal/config/config.go:325`).
`mentat.Run` takes `Config` by value, but `Targets` is a map and is therefore shared
with the caller. Wiring `Resolve` into `Run` (T014) made `Run` mutate the caller's
Config, and made two concurrent `Run`s sharing one Config data-race — confirmed
under `-race`, not theorized. This regresses the **spec-007 T010/T011 guarantee that
`mentat.Run` is reentrant and concurrency-safe**. Defensive copying restores the
documented prior behaviour; declaring that `Run` takes ownership of the caller's
Config would be the actual API change, and nothing asked for that.

- [X] T030 RED→GREEN: add a test that shares ONE `Config` across two concurrent `mentat.Run` calls and asserts (a) no data race under `-race` and (b) the caller's `cfg.Targets` is unmutated after `Run` returns. VERIFY RED first (the race must actually reproduce). Then fix by shallow-copying `cfg.Targets` into a fresh map at the top of `Run` before `Resolve`. VERIFY: new test green under `-race`; `TestRunConcurrentIndependent` still green; `make ci` exit 0

---

## Phase 7c: BLOCK finding from the T029 gate audit

**Why this exists**: the raw half of every raw/resolved twin is hard-rejected on a
bad value, but the resolved half was returned with no value validation at all. Each
unvalidated negative then hits a positive-guard downstream and silently DISARMS the
mechanism it configures — `engine.go:274` arms no deadline, `shell.go:87` no kill
escalation, `correlate.go:315` disarms the settle barrier and with it 008's
soundness guarantee for absence assertions. This falsified `Resolve`'s own doc
comment ("byte-identical effective contract", "no silent fallback on bad input").
Not a regression against main, but an incomplete delivery of FR-008..FR-010 living
entirely in code this feature added. Coverage could not catch it: 99.4% with no
branch to miss, and `TestResolveBudgetLaws` had zero rows where the explicit half
is itself invalid.

- [X] T031 RED→GREEN: parity rows asserting IDENTICAL error text from both paths for `Budget.Timeout < 0`, non-zero `Budget.KillGrace <= 0`, `Completeness.Settle < 0`, and a `Target.Budget` carrying either; then validate the explicit half inside `resolveBudgetTwin`/`resolveKillGraceTwin`/`resolveCompleteness` reusing the raw path's error text. Also settle the `Budget{Timeout, Unbounded:true}` contradiction (hard error naming both fields, or documented opt-out). VERIFY: rows RED first; full suite green; `make ci` exit 0

---

## Phase 7d: Review finding on US1 (CodeRabbit, PR #36)

**Goal**: US1 froze each re-exported struct's field SET but not its ORDER. `surfaceRender` sorts the whole line set for top-level determinism, and field lines carried no ordinal, so permuting two fields rendered byte-identically. Order is observable — unkeyed composite literals (`mentat.Verdict{true, nil, …}`) and positional reflection both bind by position, and neither errors at the consumer's call site — so this was a silent break the gate would pass. It also reverses a wrong call made in T004, which withdrew the contract's reorder-detection claim instead of changing the code.

- [X] T032 RED→GREEN: add `TestSurfaceRenderStructFieldOrder` — (a) permuting two exported fields must change the rendering, (b) a 12-field struct with DESCENDING names must still render in declaration order, which only a zero-padded ordinal can satisfy (guards against `[10]` sorting before `[2]`, and against the subtest passing vacuously on alphabetical names). VERIFY both subtests RED first. Then emit a zero-padded positional ordinal per surface field (`field (Verdict)[02] Qualifiers []string`); unexported fields skipped and NOT consuming a position, so internal-only additions still do not churn the golden. Regenerate the golden and PROVE the regen is pure re-annotation (105 field lines before and after; stripping ordinals reproduces the previous set exactly; no non-field line touched). Mutation-rehearse by permuting `core.Verdict.Pass`/`Reasons` — VERIFY RED naming both fields on both sides — then revert byte-identically and VERIFY green. Update `stability.md` (boundary 3 removed — the gate now catches this; remaining boundaries renumbered) and `contracts/surface-golden-v2.md` (reinstate the reorder claim, record both amendments)

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Whole-feature gates before PR.

- [X] T026 [P] Run the `/coverage` skill across all packages; VERIFY every touched package ≥80% (floor is per-package, constitution Principle V); add targeted tests if any package dipped
- [X] T027 Run the local e2e suite once against the harness (`make harness-up && go test -tags e2e ./e2e/ -v -parallel 16`, default 3 runs): US2 touched the config path every e2e scenario loads, and the SC-005 stdout goldens are e2e-only (known gap: green `make ci` ≠ current e2e golden); VERIFY green, record output
- [X] T028 Execute quickstart.md V1–V4 end-to-end as written plus `make ci`; VERIFY all green; fix anything red before proceeding (V5's pre-merge half already proven by T025a; its dispatch half is T025b, post-merge)
- [X] T029 Pre-PR: run **go-reviewer** in `gate` mode on the staged diff; resolve any BLOCK findings; open the PR with every golden diff itemized (T005, T017, T018 summaries) and the mutation-rehearsal narrative referenced. The nightly run URL is explicitly NOT a precondition here — it cannot exist pre-merge and is owned solely by T025b — Conventional Commit title `feat(009): extension-surface integrity`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: T001 → T002; blocks everything.
- **Foundational (Phase 2)**: empty — no blocker beyond T002.
- **US1 (Phase 3)**: starts after T002. Internal chain: T003 → T004 → T005 → T006 → T007.
- **US2 (Phase 4)**: starts after T002, fully parallel with US1 (disjoint files). Chain: T008 → T009 → T010 → T011 → T012; T013 → T014 → T015 (T013 may start once T009 exists).
- **US3 (Phase 5)**: starts after **US1 T005** (golden-file ordering, see phase note). Chain: T016 → T017 → T018 → T019.
- **US4 (Phase 6)**: starts after T002, parallel with everything. Chain: T020 → T021 → T022.
- **US5 (Phase 7)**: starts after T002, parallel with everything. Chain: T023 → T024 → T025.
- **Polish (Phase 8)**: after all desired stories; T026/T027 parallel, then T028 → T029.

### Story-level graph

```text
T001 → T002 ─┬─ US1 (T003→T007) ──→ US3 (T016→T019) ─┐
             ├─ US2 (T008→T015) ─────────────────────┤
             ├─ US4 (T020→T022) ─────────────────────┼─→ T026/T027 → T028 → T029
             └─ US5 (T023→T025) ─────────────────────┘
```

### Parallel Opportunities

- After T002: **four independent tracks** — US1 (go-test-writer), US2 (go-test-writer, disjoint files), US4 (go-coder), US5 (go-coder). US3 queues behind US1's golden regen only.
- [P]-marked story openers T003, T008, T020, T023 can all start simultaneously; T026 alongside T027.
- Within stories, tasks are deliberately sequential: TDD is one failing test at a time (constitution Principle V; global batch-size-3 rule).

## Parallel Example: after T002

```bash
# Four tracks at once (different agents, disjoint files):
Task: "T003 RED renderer unit test in surface_test.go"            # go-test-writer
Task: "T008 RED config.Resolve kind-default test in internal/config/config_test.go"  # go-test-writer
Task: "T020 Create docs/extending/new-seam.md"                    # go-coder
Task: "T023 Create .github/workflows/nightly-l3.yml"              # go-coder
```

## Implementation Strategy

### MVP First (US1 only)

1. T001–T002, then Phase 3 (T003–T007).
2. **STOP and VALIDATE**: quickstart V1 (mutation rehearsal + golden state) — US1 alone already closes the realized-drift hole and restores stability.md's truth; shippable as its own PR if Q prefers small PRs.

### Incremental Delivery

- Each story phase ends at a quickstart-backed checkpoint and is independently shippable in priority order: US1 (gate truth) → US2 (path parity) → US3 (nameability) → US4 (guide) → US5 (nightly lane).
- Single-PR alternative: land all five with the golden diffs itemized per story (T005/T017/T018 kept as separate commits so each regen is reviewable on its own).

## Notes

- Every VERIFY line is an observable checkpoint (run it, read output, record) — per the repo's testing protocol, a task with `VERIFY: DID NOT RUN` cannot be checked off.
- US1's renderer lives in test files (`surface_test.go`) — no library code changes in that story; US2 is the only story touching runtime behaviour (`config.go`, `run.go`).
- Golden regens happen three times: T005 (mass field capture, +102), T017/T018 (alias additions, +4), and T032 (review-driven field ordinal — 105 lines re-annotated, no field added or lost). All deliberate, all itemized in the PR body.
