# Tasks: DX & Product Completeness

**Input**: Design documents from `/specs/006-dx-completeness/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/cli-surface.md, contracts/judge-ledger.md

**Tests**: MANDATORY (constitution Principle V) — red→green pairs via
**go-test-writer**; Makefile/docs/goldens via **go-coder**. Nine stories (US1–US9
= audit E1–E9 reordered by priority); each independently shippable.

## Phase 1: Setup

- [ ] T001 Baseline: `make ci` green; capture `mentatctl agent run` golden output (US7 additive-lines contract) in internal/ctl/testdata/run-golden.txt (go-coder)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: none — the nine slices are mutually independent by design; Phase 2 intentionally empty. (Config file `internal/config/config.go` is the only shared hotspot — serialize config-touching green tasks: T011, T017, T024, T031.)

---

## Phase 3: User Story 1 — Step reference, generated (P1, E1) 🎯 MVP

**Independent Test**: `mentat steps` lists every registered step; drift test fails on missing metadata.

- [ ] T002 [US1] Failing tests: step metadata table — every registration carries `{pattern, summary, example}`; drift test (registered pattern without metadata → fail); count matches registration list in internal/steps/metadata_test.go (go-test-writer, red)
- [ ] T003 [US1] Restructure registration into the metadata table consumed by `RegisterSteps` in internal/steps/steps.go, internal/steps/metadata.go (go-test-writer, green)
- [ ] T004 [US1] Failing tests: `mentat steps [--format md|text]` renders grouped steps + selector/quantifier/ordinal grammar + CEL variables; md output == committed docs/steps.md (regeneration-clean) in cmd/mentat/steps_cmd_test.go (go-test-writer, red)
- [ ] T005 [US1] Implement `steps` subcommand + generator + commit generated docs/steps.md (go:generate wiring) in cmd/mentat/main.go, cmd/mentat/steps_cmd.go, docs/steps.md (go-test-writer, green)

---

## Phase 4: User Story 2 — mentat validate (P1, E2)

**Independent Test**: seeded corpus (unbound step, bad CEL, unknown target, bad shape pattern) → 4 findings, one run, exit 1, no SUT/store/judge contact.

- [ ] T006 [US2] Failing tests: validate collects ALL findings {file, line, class, message} across the four seeded defect classes + expectations/config classes; clean corpus exit 0; zero feature files → finding+exit 1; `--format json`; no network possible (noop store/driver injected) in cmd/mentat/validate_test.go (go-test-writer, red)
- [ ] T007 [US2] Implement `mentat validate [paths...]`: gherkin parse → step binding vs metadata table → CEL precompile → shape-pattern + expectations + target checks (reusing the scenario-init precheck funcs) in cmd/mentat/validate.go, exported prechecks in internal/steps/precheck.go (go-test-writer, green)

---

## Phase 5: User Story 6 — Judge cost ledger, budget, defaults (P1, E6)

**Independent Test**: fake judge with fixed usage → per-scenario + total ledger in JSON/HTML; 1-cent budget aborts; votes>1@temp0 fails at load.

- [ ] T008 [US6] Failing tests: `core.JudgeUsage` captured per call (SDK usage fields), aggregated across votes by the semantic matcher into Verdict detail in internal/judge/claude_test.go, internal/comparator/semantic_test.go (go-test-writer, red)
- [ ] T009 [US6] Implement usage capture + aggregation (core.JudgeUsage type, `go generate` mocks) in internal/core/core.go, internal/judge/claude.go, internal/comparator/semantic.go (go-test-writer, green)
- [ ] T010 [US6] Failing tests: collector sums per-scenario + suite `judgeTotal`; JSON `judge{}` objects (absent when no calls — no fabricated zeros); HTML section; cost via pricing table with ambiguous-model hard error in internal/report/collector_test.go, internal/report/report_test.go (go-test-writer, red)
- [ ] T011 [US6] Implement ledger rendering + `judge.max_cost_usd` config + post-scenario budget check aborting with spent/budget/scenario named in internal/report/collector.go, internal/report/json.go, internal/report/html.go, internal/config/config.go, internal/steps/steps.go (go-test-writer, green)
- [ ] T012 [US6] Failing tests: default judge model constant is fast tier; `votes>1 && temperature==0` → load error naming both remedies; L3 semantic meta-tests pass under new default in internal/config/config_test.go, internal/steps/semantic_meta_test.go (go-test-writer, red)
- [ ] T013 [US6] Implement default swap + config guard; record price-sheet math for SC-006 in the PR description in internal/config/config.go (go-test-writer, green)

---

## Phase 6: User Story 3 — JUnit + console together (P2, E3)

**Independent Test**: one run with `--junit` → file exists AND console non-empty.

- [ ] T014 [P] [US3] Failing test: `--junit` adds the junit formatter (godog multi-format `pretty,junit:file`) with console preserved; junit write failure still fails the run in cmd/mentat/main_test.go (go-test-writer, red)
- [ ] T015 [US3] Implement multi-formatter wiring in cmd/mentat/main.go (go-test-writer, green)

---

## Phase 7: User Story 4 — HTTP request bodies (P2, E4)

**Independent Test**: httptest server receives the doc-string body verbatim; missing fixture fails naming resolved path.

- [ ] T016 [P] [US4] Failing tests: `I send the request with body:` (doc-string) and `... with body fixture "<path>"` (relative to feature dir; absolute ok) set `RunSpec.Input`; http driver sends non-empty Input; missing fixture → error naming resolved path in internal/steps/steps_test.go, internal/driver/http_test.go (go-test-writer, red)
- [ ] T017 [US4] Implement body steps + Input plumbing + http driver send in internal/steps/steps.go, internal/engine/engine.go, internal/driver/http.go (go-test-writer, green)

---

## Phase 8: User Story 5 — File store: offline replay (P2, E5)

**Independent Test**: saved run replays to identical verdicts, network disabled; absent id errors; `@runs(2)` hard-errors.

- [ ] T018 [P] [US5] Failing tests: file store `Query` scans storePath for run-id-tagged fixtures (absent → not-found naming dir+id), `GetByID` loads by trace id with canonical vocabulary (feature 002), `@runs(N>1)` → hard error in internal/store/filestore_test.go (go-test-writer, red)
- [ ] T019 [US5] Implement directory-backed file store + register `"file"` factory + `storePath` config in internal/store/filestore.go, internal/engine/store.go, internal/config/config.go (go-test-writer, green)
- [ ] T020 [US5] Offline e2e-style test: saved fixture suite runs green via file store with no docker (hermetic — lives in unit tier) in internal/steps/filestore_replay_test.go (go-test-writer)

---

## Phase 9: User Story 8 — Configurable answer extraction (P2, E8)

**Independent Test**: marker config extracts after last marker; absent marker fails naming it; default unchanged.

- [ ] T021 [P] [US8] Failing table tests: extraction modes whole (today's behaviour), marker (last occurrence; absent → run failure naming marker), pattern (first capture group; no match → failure naming pattern); config validation (marker/pattern required per mode, pattern must compile with ≥1 group) in internal/core/core_test.go, internal/config/config_test.go (go-test-writer, red)
- [ ] T022 [US8] Implement policy-parameterized `ExtractAnswer` + `targets.<n>.extract` config + driver application in internal/core/core.go, internal/config/config.go, internal/driver/shell.go (go-test-writer, green)

---

## Phase 10: User Story 7 — mentatctl surface (P3, E7)

**Independent Test**: run summary shows tokens/cost/latency/trace ids; prompt-file/stdin/-o/--timeout work.

- [ ] T023 [P] [US7] Failing tests: summary gains additive lines (tokens in/out, cost, latency ms, root trace ids) with existing lines byte-stable vs T001 golden prefix; `--prompt-file` (`-`=stdin), `-o` (answer only), `--timeout` in internal/ctl/run_test.go, cmd/mentatctl/main_test.go (go-test-writer, red)
- [ ] T024 [US7] Implement summary enrichment + flags in internal/ctl/run.go, internal/ctl/format.go, cmd/mentatctl/main.go (go-test-writer, green)

---

## Phase 11: User Story 9 — Prebuilt SUTs + e2e conventions (P3, E9)

**Independent Test**: `make labs` builds binaries, rebuilds on source change; report-meta tests use mentatBin + t.Parallel.

- [ ] T025 [P] [US9] Add `make labs` (bin/researchbot, bin/orderflow, captures) with Go-source prerequisites; `harness-up` depends on it; point mentat.yaml + e2e configs at binaries in Makefile, mentat.yaml, e2e/ configs (go-coder)
- [ ] T026 [P] [US9] Fix e2e/report_meta_test.go: `go run` → `mentatBin`, add `t.Parallel()` (top + subtests); verify suite green (go-test-writer)

---

## Phase 12: Polish & Cross-Cutting

- [ ] T027 [P] Coverage gate `/coverage` ≥80% touched packages (go-coder)
- [ ] T028 [P] Full quickstart.md validation: offline replay proof (docker down), doc walkthrough note for SC-001, `make ci` (SC-008) (go-coder)
- [ ] T029 Sync both contract docs with implementation (flag spellings, JSON field names, price math); README updates (steps ref, validate, file store, judge budget); changelog for `--junit` behaviour change (go-coder)

---

## Dependencies & Execution Order

- No foundational phase — stories are file-disjoint except `internal/config/config.go` (serialize T011, T013, T017, T019, T022 greens — or land as micro-PRs in that order) and `internal/steps/steps.go` (T003 before T007's precheck export; serialize T017/T021 steps edits).
- US5 (file store) consumes feature 002's canonical vocabulary — land after 002 or include its spellings.
- US2 depends on US1's metadata table for step binding (T007 after T003).
- Priority order: US1, US2, US6 (P1) → US3, US4, US5, US8 (P2) → US7, US9 (P3). MVP = US1.

## Parallel Example (kickoff)

```text
go-test-writer A: T002→T005 (US1) then T006→T007 (US2)
go-test-writer B: T008→T013 (US6 judge chain)
go-test-writer C: T014→T015 (US3), then T016→T017 (US4)
go-coder:        T001, T025 (Makefile), T026
```

## Implementation Strategy

Ship US1 (step docs) as MVP — it unblocks human authoring immediately. US2 and
US6 complete the P1 tier. Every story is a checkpoint; nothing here blocks the
correctness features (002–005), which take precedence if scheduling conflicts.
