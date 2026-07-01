---
description: "Task list for Semantic (LLM-Judge) Result Matcher"
---

# Tasks: Semantic (LLM-Judge) Result Matcher

**Input**: Design documents from `specs/001-semantic-judge/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: TDD is **NON-NEGOTIABLE** (constitution Principle V). Every `[TDD]` test task MUST
be written to **FAIL** before its implementation task. Route behaviour-change tasks through
**go-test-writer**; scaffolding/dep/mock/wiring tasks through **go-coder**; pre-commit audit
through **go-reviewer** (gate).

**Scope**: Decision **A** — ships **US1–US3 + the configurable vote**. **US4** (statistical
semantic over `@runs(N)` / FR-012 / SC-007) is **deferred** (see plan.md → Complexity
Tracking) and has **no tasks here**.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different file, no dependency on an incomplete task)
- **[Story]**: US1 / US2 / US3 (Setup/Foundational/Polish carry no story label)

## Path Conventions

Single Go module at repo root (`internal/…`, `cmd/…`, `features/…`, `e2e/…`).

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Bring in the new dependency and confirm a green baseline.

- [X] T001 Add `github.com/anthropics/anthropic-sdk-go` as a direct dependency: `go get github.com/anthropics/anthropic-sdk-go && go mod tidy`; confirm `go build ./...` — updates `go.mod` / `go.sum` (go-coder) — added v1.53.0 (currently `// indirect`; promotes when `internal/judge` imports it)
- [X] T002 Confirm baseline is green before any change: run `make ci` (or `go test ./...`) and capture current per-package coverage via the `/coverage` skill — baseline `go build ./...` + `go test ./...` all green

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The `Judge` seam, its mock, config, and registry — shared by all stories.

**⚠️ CRITICAL**: No user story work begins until this phase is complete. (US1 strictly needs
only T003–T004; US2/US3 additionally need T005–T008.)

- [X] T003 Add `Judge` interface + `JudgeRequest` + `JudgeVerdict` types to `internal/core/core.go` (additive; the existing `//go:generate mockgen` directive covers them) — per contracts/judge-seam.md
- [X] T004 [P] Regenerate uber gomock (`go generate ./...`) and commit `MockJudge` in `internal/core/mocks/mock_core.go` (go-coder; depends on T003)
- [X] T005 [TDD] Write failing tests for `JudgeConfig` defaults + validation in `internal/config/config_test.go` (backend→"claude", model→"claude-opus-4-8", votes→1; `votes<1` error; even `votes>1` error; temperature finite ≥ 0) — per contracts/config-judge.md
- [X] T006 Implement `JudgeConfig` + defaults + validation in `internal/config/config.go` (make T005 green) — 100% coverage; `validateJudge` mirrors `validatePricing`
- [X] T007 [TDD] Write failing registry tests for `RegisterJudge`/`Judge` factory get/put + unknown-name in `internal/registry/registry_test.go` (depends on T003, T006)
- [X] T008 Implement `JudgeFactory` + `RegisterJudge` + `Judge(name)` (factory-based; extend the stateless-vs-stateful rationale comment) in `internal/registry/registry.go` (make T007 green) — 100% coverage

**Checkpoint**: Seam, mock, config, and registry exist — user stories can begin.

---

## Phase 3: User Story 1 - Assert the meaning of an answer (Priority: P1) 🎯 MVP

**Goal**: `the result means "..."` grades a run's answer by meaning via the `semantic`
matcher (with configurable best-of-N vote) and goes RED when the meaning is wrong.

**Independent Test**: Run the comparator + step + L3 meta tests against a stand-in/gomock
`Judge` — zero network. Correct-meaning → pass; wrong-meaning → red with a reason.

### Tests for User Story 1 (REQUIRED — Test-First) ⚠️

- [X] T009 [P] [US1] [TDD] Failing semantic matcher verdict tests (match→`Verdict{Pass:true}`; no-match→`Pass:false`+reason) with a gomock `Judge` in `internal/comparator/semantic_test.go`
- [X] T010 [US1] [TDD] Add failing vote tests to `internal/comparator/semantic_test.go` (votes=1 single call; votes=3 strict majority; even-N tie → hard error per FR-015)
- [X] T011 [US1] [TDD] Add failing tests to `internal/comparator/semantic_test.go`: empty `want` → fail-fast (FR-013); Judge returns error → matcher returns error with **no Verdict** (FR-007 at matcher level)
- [X] T012 [US1] [TDD] Failing step tests for `the result means` (inline + docstring) building `ResultExpectation{Matcher:"semantic"}`, and the existing `@runs(N>1)` guard hard-erroring, in `internal/steps/steps_test.go`

### Implementation for User Story 1

- [X] T013 [US1] Implement `NewSemantic(j core.Judge, votes int) core.Matcher` in `internal/comparator/semantic.go` (Name "semantic"; candidate = `ev.Output.Answer`; vote → strict majority; tie → hard error; empty-want guard; judge-error propagation) — makes T009–T011 green — 93.9% pkg coverage
- [X] T014 [US1] Implement `^the result means "([^"]*)"$` + `^the result means:$` step handlers in `internal/steps/steps.go` (route through `world.check` → result comparator) — makes T012 green — 82.6% pkg coverage
- [X] T015 [P] [US1] Author the L3 meta feature `features/meta/bad_meaning.feature` (wrong-meaning scenario) plus a green companion scenario
- [X] T016 [US1] Wire the L3 meta-scenario with a deterministic fake `core.Judge` registered as `"semantic"`; assert Mentat goes **RED** on wrong meaning and **GREEN** on correct (mandatory L3; FR-011 / SC-002) — **placed in `internal/steps/semantic_meta_test.go`** (not `e2e/`): the `e2e` package execs the real binary + needs `harness-up`, structurally non-hermetic; the in-process steps harness is where the existing hermetic meta tests live. Fake judge wired through real `engine.Build` via `cfg.Judge.Backend="fake-*"`

**Checkpoint**: US1 is fully functional and testable with a stand-in Judge (MVP).

---

## Phase 4: User Story 2 - Never trust a Judge that could not answer (Priority: P1)

**Goal**: The Claude backend (and the matcher) turn every judge failure into a hard,
descriptive error — never a guessed verdict.

**Independent Test**: Hermetic backend tests for missing-key / malformed-response /
SDK-error / refusal all produce descriptive errors and no verdict; live behaviour `e2e`-gated.

### Tests for User Story 2 (REQUIRED — Test-First) ⚠️

- [X] T017 [P] [US2] [TDD] Failing hermetic claude-backend tests in `internal/judge/claude_test.go`: missing `ANTHROPIC_API_KEY` → descriptive error **before any call** (US2-AC3); valid verdict JSON → `JudgeVerdict`; malformed/unparseable response → error (US2-AC2)
- [X] T018 [US2] [TDD] Add failing error-classification tests to `internal/judge/claude_test.go`: SDK `*anthropic.Error` via `errors.As` (401/429/5xx) → `%w` descriptive errors (US2-AC1); `stop_reason == refusal` → hard error

### Implementation for User Story 2

- [X] T019 [US2] Implement `NewClaude(cfg config.Config) (core.Judge, error)` in `internal/judge/claude.go` (anthropic-sdk-go client; structured output via `output_config.format` schema `{match,reason}`; thinking disabled; `temperature` only on Sonnet 4.6 / Haiku 4.5 per research Decision 4; credential check; error mapping) — makes T017–T018 green — 97% coverage, bound to real v1.53.0 SDK
- [X] T020 [P] [US2] Live `//go:build e2e` tests in `internal/judge/claude_e2e_test.go`: paraphrase-but-correct → match; contradiction → no-match (research Decision 8) — compiles under -tags e2e, t.Skip-guarded

**Checkpoint**: US1 + US2 work; judge failures are honest hard errors.

---

## Phase 5: User Story 3 - Swap & test the backend without touching comparators (Priority: P2)

**Goal**: Config selects the judge backend (default `claude`), wired once at `engine.Build`;
unknown backend errors; the suite runs hermetically with a stand-in.

**Independent Test**: `engine.Build` with default config wires the Claude judge + registers
`semantic`; an unknown backend errors descriptively; a stand-in resolves a semantic scenario
with zero network.

### Tests for User Story 3 (REQUIRED — Test-First) ⚠️

- [X] T021 [P] [US3] [TDD] Failing `judge.RegisterBuiltins` test ("claude" factory registered + resolvable) in `internal/judge/judge_test.go`
- [X] T022 [US3] [TDD] Failing `engine.Build` tests in `internal/engine/build_test.go`: unknown judge backend → descriptive error (US3-AC2); default backend resolves and `"semantic"` is registered after `Build`

### Implementation for User Story 3

- [X] T023 [US3] Implement `judge.RegisterBuiltins()` registering the `"claude"` factory in `internal/judge/judge.go` (make T021 green) — 97.1% coverage
- [X] T024 [US3] Wire `internal/engine/build.go`: call `judge.RegisterBuiltins()`; resolve `registry.Judge(cfg.Judge.Backend)`; construct the judge; `registry.RegisterMatcher("semantic", comparator.NewSemantic(judge, cfg.Judge.Votes))`; unknown-backend → hard error (make T022 green; per contracts/judge-seam.md) — empty backend defaults to "claude" so existing Build callers unbroken; 96.7% coverage
- [X] T025 [US3] Hermetic-suite proof (US3-AC3): a test that a semantic scenario resolves with a stand-in judge and **zero network**, reusing the fake judge from T016, in `internal/steps/semantic_meta_test.go` — GREEN test clears `ANTHROPIC_API_KEY` via `t.Setenv` and still passes, proving the fake (not Claude) was wired and nothing hit the network

**Checkpoint**: All three stories independently functional; backend is config-pluggable.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T026 [P] Document the `semantic` matcher + a sample `judge:` block in `mentat.yaml`/README, including the data-egress note (FR-016) — in `README.md` / `docs/` — added "Semantic result matcher" section + egress callout to README.md
- [X] T027 [P] Verify ≥80% coverage on `internal/judge`, `internal/comparator`, `internal/config`, `internal/registry` via the `/coverage` skill (SC-008) — judge 97.1%, comparator 93.9%, config 100%, registry 100% (all packages pass the floor; total 80.3%)
- [X] T028 `gofmt -l .` clean + `go vet ./...` + `golangci-lint run` (if `.golangci.yml`); run `make ci` — `make ci` (lint+test+cover) all green; gofmt + vet clean
- [X] T029 Run quickstart.md validations (steps 1–6) and confirm the documented outcomes — hermetic paths 1–4 pass (matcher, config, wiring, L3 red+green); paths 5 (live Claude) + 6 (manual smoke) need a key/harness, documented as out-of-band
- [X] T030 go-reviewer `gate` audit of the staged diff (PASS/BLOCK) before commit — **VERDICT: PASS** (no BLOCK/SHOULD-FIX; 5 optional NITs, the `tt := tt` one fixed)

---

## Dependencies & Execution Order

### Phase dependencies
- **Setup (P1)** → no deps.
- **Foundational (P2)** → after Setup. T003 → T004; T005 → T006; (T003, T006) → T007 → T008.
- **US1 (P3)** → needs T003–T004 only. Test tasks (T009–T012) before impl (T013–T014); T015 → T016.
- **US2 (P4)** → needs Setup (T001 SDK) + Foundational (T003 core, T006 config). Independent of US1. T017–T018 → T019; T020 `e2e`-gated.
- **US3 (P5)** → needs Foundational (T006 config, T008 registry) + US2 (T019 claude backend) + US1 (T012 semantic matcher) to wire. T021 → T023; T022 → T024; T025 reuses T016.
- **Polish (P6)** → after the stories you intend to ship.

### Story independence
- **US1** is testable alone with a stand-in Judge (no SDK, no registry).
- **US2** builds the real Claude backend + failure mapping (no US1 dependency).
- **US3** is the integration/wiring story — it composes US1's matcher with US2's backend via the registry.

### Parallel opportunities
- T004 ∥ later foundational reads once T003 lands.
- Across stories after Foundational: **US1** and **US2** can proceed in parallel (different files/packages); **US3** waits on both.
- Within a story, `[P]` tasks touch different files (e.g. T015 feature file ∥ T009 matcher test; T020 e2e ∥ T017 hermetic test). Same-file test tasks (T009/T010/T011) are sequential.

---

## Parallel Example: after Foundational

```bash
# Two developers / two streams in parallel:
Stream A (US1): T009 → T010 → T011 → T012 → T013 → T014 → (T015 ∥) → T016
Stream B (US2): T017 → T018 → T019 → (T020 e2e)
# Then converge:
US3: T021/T022 → T023/T024 → T025
```

---

## Implementation Strategy

### MVP first (US1)
1. Setup (T001–T002) → Foundational T003–T004 (US1 only needs these).
2. US1 (T009–T016). **STOP & VALIDATE**: wrong-meaning goes RED, correct-meaning passes — all hermetic.
3. This proves the capability end-to-end with a stand-in Judge.

### Incremental delivery (shippable to a user)
1. Finish Foundational (T005–T008).
2. US2 (T017–T020) → real Claude backend with honest failures.
3. US3 (T021–T025) → config-pluggable, wired at `engine.Build`. **Now usable by a real user.**
4. Polish (T026–T030).

---

## Notes

- `[P]` = different file, no dependency on an incomplete task.
- Every `[TDD]` test MUST be confirmed **red** before its implementation task.
- Commit per task or logical group (Conventional Commits; `git add` files individually; no AI attribution).
- The Anthropic SDK is isolated to `internal/judge` — `internal/comparator` must not import it (keeps the comparator Evidence-only / transport-free).
- **Out of scope (no tasks)**: US4 statistical semantic over `@runs(N)` — deferred (see the separate hand-off prompt / plan.md Complexity Tracking).
