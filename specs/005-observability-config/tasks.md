# Tasks: Observability & Config Integrity

**Input**: Design documents from `/specs/005-observability-config/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/narration-and-errors.md

**Tests**: MANDATORY (constitution Principle V) — red→green pairs via
**go-test-writer** (buffer-backed slog handlers, pinned substrings);
flag/doc/golden scaffolding via **go-coder**.

## Phase 1: Setup

- [X] T001 Capture the golden happy-path stdout of a green `mentat run` against the live Tempo harness into cmd/mentat/testdata/golden-green.txt for SC-005 (go-coder) — DONE live: captured normalized default-verbosity stdout (only godog's duration line normalized to `<DURATION>`); golden-check in e2e/golden_test.go (`//go:build e2e`, live harness) verified non-flaky over 3 runs.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: logger seam — US1 and US3 narration hang off it.

- [X] T002 Failing tests: `engine.Build` accepts `WithLogger(*slog.Logger)`; default is a discard handler; logger reaches correlator and drivers (constructor injection, no globals) in internal/engine/build_test.go (go-test-writer, red)
- [X] T003 Implement logger option + plumb-through (engine → correlate/driver constructors) in internal/engine/build.go, internal/engine/engine.go, internal/correlate/correlate.go, internal/driver/shell.go, internal/driver/http.go (go-test-writer, green)

**Checkpoint**: silent-by-default logger available at every seam.

---

## Phase 3: User Story 1 — Correlation failures diagnosable from output alone (P1) 🎯 MVP

**Goal**: D1 dead — narration + enriched timeout error + checklist.

**Independent Test**: dead collector → error alone names endpoint/query/checklist; `-v` shows lifecycle.

- [X] T004 [US1] Failing tests: zero-span timeout error contains `store: <endpoint>`, `query: { .test.run.id = "<id>" }`, `checklist:`; unstable-deadline error (feature 002) gains store/query lines in internal/correlate/correlate_test.go (go-test-writer, red)
- [X] T005 [US1] Implement enriched errors (endpoint threaded into correlate via the store or Build wiring) in internal/correlate/correlate.go (go-test-writer, green)
- [X] T006 [US1] Failing tests (buffer handler): Info narration — `drive.start`(target,adapter,run_id), `resolve.start`(run_id,store_endpoint,query), `resolve.done`(spans,roots,rounds,elapsed); Debug — `resolve.poll`(round,spans_seen,stable_streak), `drive.env` (Mentat-set keys only, never inherited env), `drive.done`(exit_code); silent default emits zero bytes in internal/engine/engine_test.go, internal/correlate/correlate_test.go, internal/driver/shell_test.go (go-test-writer, red)
- [X] T007 [US1] Implement narration at the researched points in internal/engine/engine.go, internal/correlate/correlate.go, internal/driver/shell.go (go-test-writer, green)
- [X] T008 [US1] Failing tests: `-v`/`-vv` flags on both binaries map to Info/Debug slog handler on stderr in cmd/mentat/main_test.go, cmd/mentatctl/main_test.go (go-test-writer, red)
- [X] T009 [US1] Wire flags + handler in cmd/mentat/main.go, cmd/mentatctl/main.go (go-test-writer, green)
- [X] T010 [US1] Failing e2e: dead-collector diagnosis walk — red run's stderr carries endpoint/query/checklist; `-vv` rerun shows injected env + poll rounds in e2e/diagnosis_test.go (go-test-writer — implemented with a reachable-but-empty fake Tempo to hit the zero-span-timeout+checklist path deterministically, no docker)

**Checkpoint**: the first-user failure is self-explanatory; MVP shippable.

---

## Phase 4: User Story 2 — Bad configuration fails at load, precisely (P1)

**Goal**: D2 + D3 dead — strict YAML, registry-truth adapter validation.

**Independent Test**: misspelled key fails naming it; `adapter: grpc` fails at startup listing registered drivers.

- [X] T011 [P] [US2] Failing table test: one typo'd key per config section (root, poll, judge, targets.<n>, reporters) → load error in the yaml.v3 strict form `field <key> not found in type config.<Type>` (names the unknown key and its containing config type, not a dotted full path); valid config loads unchanged; absent optional keys still fine in internal/config/config_test.go (go-test-writer, red)
- [X] T012 [US2] Switch to strict decode (`yaml.Decoder.KnownFields(true)`, expectations-loader pattern) in internal/config/config.go (go-test-writer, green)
- [X] T013 [US2] Failing tests: `registry.Drivers()` listing; `engine.Build` rejects targets whose adapter has no registered driver, error names target+adapter+registered set; `mcp`/`grpc` removed from `defaultConcurrency` in internal/registry/registry_test.go, internal/engine/build_test.go, internal/config/config_test.go (go-test-writer, red)
- [X] T014 [US2] Implement Drivers() + Build-time validation + allowlist shrink in internal/registry/registry.go, internal/engine/build.go, internal/config/config.go (go-test-writer, green)

**Checkpoint**: config typos and phantom adapters die at startup.

---

## Phase 5: User Story 3 — Never sabotage the SUT's own telemetry (P2)

**Goal**: D4 dead — no empty-endpoint injection; resource-attr merge.

**Independent Test**: ambient endpoint survives unset config; merge keeps both attribute sets, Mentat wins collisions.

- [X] T015 [US3] Failing tests: empty `cfg.OTLPEndpoint` → variable not injected; set → config wins in internal/engine/engine_test.go (go-test-writer, red)
- [X] T016 [US3] Conditional endpoint injection in internal/engine/engine.go (go-test-writer, green)
- [X] T017 [US3] Failing table tests: `OTEL_RESOURCE_ATTRIBUTES` merge — ambient-only, Mentat-only, both (Mentat wins collisions incl. `test.run.id`), malformed ambient → hard error naming value; percent-decode/encode round-trip in internal/driver/shell_test.go (go-test-writer, red)
- [X] T018 [US3] Implement merge (inverse `otelEncode` parse + overlay + re-encode) in internal/driver/shell.go (go-test-writer, green)
- [X] T019 [US3] e2e correlation regression: full meta suite still correlates every run (no tag loss from merge) — run and verify, no new code (go-test-writer) — DONE live: full `go test -tags e2e ./e2e/` PASS (55s) against harness-up Tempo — happy path, meta suite, hung-SUT, orderflow http, diagnosis all green; the resource-attr merge did not drop correlation.

**Checkpoint**: working developer environments stay working.

---

## Phase 6: User Story 4 — Ordinal honesty + single-source correlator (P3)

**Goal**: D5 + D6 dead.

**Independent Test**: overflow ordinal fails naming the value; both binaries share one correlator construction.

- [X] T020 [P] [US4] Failing test: unparseable/overflow span ordinal → step-parse error naming ordinal text + step in internal/steps/steps_test.go (go-test-writer, red)
- [X] T021 [US4] Surface `strconv.Atoi` error in `parseSpanSpec` in internal/steps/steps.go (go-test-writer, green)
- [X] T022 [P] [US4] Failing tests: `engine.BuildCorrelator(cfg, logger)` — defaults table (200ms/30s/3 named constants), config overrides in internal/engine/correlator_test.go (go-test-writer, red)
- [X] T023 [US4] Implement BuildCorrelator; delete copy-pasted `parseDur`/`orDefault` + construction from cmd/mentat/main.go and cmd/mentatctl/main.go (go-test-writer, green)

**Checkpoint**: all stories green.

---

## Phase 7: Polish & Cross-Cutting

- [X] T024 [P] Coverage gate `/coverage` ≥80% touched packages (go-coder) — PASS: config 98.9%, engine 94.9%, correlate 99.2%, driver 95.1%, steps 83.2%, registry 100%; cmd/* exempt (main entrypoints)
- [X] T025 [P] Golden happy-path check: green run stdout byte-identical to T001 golden (SC-005); full `make ci` (SC-006) (go-coder) — DONE: `make ci` (SC-006) PASS (lint+test+cover, total 82.5%); golden check PASS live (e2e/golden_test.go) — also proves stdout(default)==stdout(-vv), narration stays on stderr.
- [X] T026 Sync contracts/narration-and-errors.md pinned substrings/attribute names with implementation; README section for `-v`/`-vv` and the diagnosis checklist; changelog callouts for the two intentional breaks (strict config, phantom adapters) (go-coder) — contract verified (one drift fixed: yaml.v3 emits `field <key> not found in type`, not a dotted path); README "Verbosity & diagnostics" section + CHANGELOG breaking-change entries added

---

## Dependencies & Execution Order

- Phase 2 (logger seam) blocks US1 narration (T006+) but NOT US2 (config) — US2 can start immediately after Setup.
- US1: T004–T005 (errors) before/parallel to T006–T007 (narration, same file — serialize edits in correlate.go); flags (T008–T009) last; T010 needs all.
- US2 independent; T013–T014 touch build.go — serialize with T003 (same file).
- US3 independent after Phase 2 (engine/driver files — serialize T015–T016 with T007's engine edits).
- US4 fully parallel except T023 touches both mains (serialize with T009).
- Coordinate with features 002/004 if in flight: correlate.go and engine.go are shared hotspots (trivial merge conflicts expected, noted in spec Assumptions).
- MVP = Phases 1–3.

## Parallel Example (after Phase 2)

```text
go-test-writer A: T004→T010 (US1 chain)      |  go-test-writer B: T011→T014 (US2 config)
go-test-writer C: T020→T021 (steps ordinal)  |  go-coder:        T001 golden capture
```

## Implementation Strategy

US1 and US2 are both P1: US2 is smaller and file-disjoint — land it first for an
early win, then US1 (the diagnosis experience), then US3, US4. Golden-stdout
parity (SC-005) is the regression tripwire for every narration task.

---

## Phase 8: Convergence

**Purpose**: close residual gaps found by `/speckit-converge` assessing the merged
code (#28) against spec/plan/tasks/contract. Four parallel cluster audits (US1, US2,
US3, and US4 + cross-cutting) confirmed all 26 prior tasks present in code, coverage ≥80% on every
touched package, and the contract's pinned substrings test-locked. Two `partial` gaps
remain (ordered MEDIUM then LOW). Constitution I–V upheld (IV strengthened).

- [X] T027 Add the driven command as a `command` (argv) attribute on the `drive.start` Info narration record in internal/engine/engine.go — it currently narrates target+adapter+run_id but omits the command, which FR-002 and US1 acceptance scenario 2 both require ("target **and command** driven"); the assembled command is already at engine.go:153. Add a buffer-handler test pinning the `command` attribute per FR-002 (partial) — DONE (TDD): added `"command", spec.Command` at engine.go:163; new table test `TestDriveStartNarratesCommand` (engine_test.go) pins `command="[...]"` for base argv + appended drive args; RED proven first, silent-default zero-byte guardrail still green; engine cover 95.0%.
- [X] T028 Add a through-Run shell test in internal/driver/shell_test.go using `t.Setenv` to set an ambient `OTEL_EXPORTER_OTLP_ENDPOINT` alongside a configured endpoint, asserting the SUT child process receives the configured value (config-wins-over-ambient via exec dedupEnv last-wins) — this "configured value wins" behaviour is currently proven only by code reasoning + mock-level `spec.Env`, with no exec-level regression lock per US3/AS3 and SC-004 (partial) — DONE (TDD): added non-parallel table test `TestShellConfigEndpointOverridesAmbient` (shell_test.go) — execs real `sh -c` printing `OTEL_EXPORTER_OTLP_ENDPOINT`, rows: config overrides ambient / ambient passes through when no config; deliberate-failure sanity check confirmed it exercises the exec path; test-only, no prod change; driver cover 95.1%.

_Not a task (needs a spec decision, outside converge's append-only code scope):_ US1
acceptance scenario 2 places injected-env values and per-poll span counts under `-v`,
but the authoritative contract (contracts/narration-and-errors.md) and the code gate
them under `-vv` (Debug). Code matches the contract; reconcile spec.md only if AS2 is
authoritative.
