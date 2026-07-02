# Codebase audit — 2026-07-01

Six parallel read-only audits (latent bugs, concurrency, performance, architecture
invariants, test quality, improvement opportunities) over `internal/`, `cmd/`,
`e2e/`, `tracelab/`, cross-checked against the design docs. All HIGH findings were
re-verified against the code by hand; finding A1 was verified empirically against
the repo's own deploy stack (grafana/tempo:2.5.0). Architecture invariants 1–5:
**all hold** in the main paths. Total coverage 80.4%. The L3 meta-test suite is
genuine. Findings below are grouped into the clusters used by the follow-up specs.

Finding IDs (A1, B2, …) are stable — specs and plans reference them.

## Cluster A — Verdict integrity (silent false verdicts)

| ID | Sev | Where | Finding |
|----|-----|-------|---------|
| A1 | HIGH | `internal/comparator/budgets.go:253`, `internal/store/tempo.go:139` | `errorCount` compares `Status == "Error"` but the store keeps the wire value — live Tempo returns `STATUS_CODE_ERROR` (empirically verified). `no span has status "ERROR"`, `MaxErrors`, CEL `errors`, and `span.status=Error` selectors are permanently green on live traces. Fixtures share the code's spelling, so unit tests mask it. |
| A2 | HIGH | `internal/engine/engine.go:129-135`, `internal/steps/steps.go:150` | `DriveN.collect` discards the `driveOnce` error whenever `RunID != ""`. Steps always call `DriveN` (even n=1), so driver/resolve failures are swallowed: assertion-free scenarios pass green when the SUT never ran; scenarios with assertions fail with fabricated reasons while the root cause is lost. The engine.go:73-74 comment about `Drive` surfacing errors is dead intent. |
| A3 | HIGH | `internal/correlate/correlate.go:96-101` | On deadline with spans present, `Resolve` returns the merged forest as plain success, bypassing the `StableFor` gate. Absence assertions (`never called`, `exactly N`) can pass against a still-ingesting trace. Flagged independently by 4 of 6 audit agents. |
| A4 | MEDIUM | `internal/store/tempo.go:155-158` | TraceQL search sends no `limit`/paging → Tempo's default (20 traces) silently truncates wide multi-turn forests; the stability poll then stabilizes on the truncated count. |
| A5 | MEDIUM | `internal/store/tempo.go:46-57`, `internal/store/filestore.go` | `Span.Kind` is never populated (decoder has no `kind` field; live Tempo sends `SPAN_KIND_SERVER`), yet `span.kind` is a validated shape-selector key. `absent` kind-assertions always pass; `exists` always fails. |
| A6 | MEDIUM | `internal/engine/engine.go:110-113`, `internal/comparator/aggregate_cel.go:107-116` | Resolve-failure Evidence discards the driver's real `Output`; `@runs(N)` boundary aggregates (`r.status`, `r.answer`) compute over fabricated zeros instead of hard-erroring. |
| A7 | MEDIUM | `internal/store/filestore.go:38-44` | Fixture loader silently promotes out-of-range/omitted `parentIndex` to a root (and accepts self-parent); omitted key on span 0 makes it its own parent with empty `Roots`. Should be a hard descriptive error; only `-1` means root. |
| A8 | LOW | `internal/report/derive.go:42-48`, `internal/comparator/sequence.go:131` | After-hook report derivation can flip a passing scenario to failed: zero `execute_tool` spans falls through to `ServiceSequence`, which hard-errors on any span missing `service.name`. |

## Cluster B — Run lifecycle (hangs, cancellation, signals)

| ID | Sev | Where | Finding |
|----|-----|-------|---------|
| B1 | HIGH | `internal/driver/shell.go:24-39` | `bytes.Buffer` stdio + `cmd.Run()` blocks until every pipe-holder exits; `go run` (the repo's own config) guarantees a grandchild that survives a context kill. No `WaitDelay`, no `Setpgid`/process-group kill → hung SUT hangs `mentat run` forever and leaks the process. |
| B2 | HIGH | `internal/steps/steps.go:150,167,444` | Drive/Compare/Aggregate run on `context.Background()`, discarding godog's scenario context. Nothing bounds the SUT run or judge calls; `ctl.ReplayFeature`'s context threading (replay.go:27) is dead on arrival. |
| B3 | MEDIUM | `cmd/mentat/main.go:88-114` | No `signal.NotifyContext`: SIGTERM/CI-cancel orphans the running SUT (which keeps exporting spans and spending LLM tokens) and drops all report outputs written only after `suite.Run()` returns. |
| B4 | LOW | `internal/engine/engine.go:154-171` | Parallel `@runs(N)` has no internal cancellation: once one iteration hits a structural error, remaining iterations still drive the SUT to completion before the batch errors. |
| B5 | LOW | `internal/registry/registry.go:12-35`, `internal/engine/build.go:19-23` | Package-global registries are unsynchronized; build-once-before-concurrency is comment-enforced and has bitten once already (steps_test.go:1087, commit f0b4505). |

## Cluster C — Correlation performance

| ID | Sev | Where | Finding |
|----|-----|-------|---------|
| C1 | HIGH | `internal/correlate/correlate.go:62-106` | Stability polling re-fetches and fully re-decodes every trace every round, plus a fixed `StableFor×interval` (~600ms) sleep tax per run even when ingestion is complete. `@runs(10)` ≈ ≥6s pure sleep + ~40 redundant full-trace fetches. Fix: cheap change detection per round, decode once after stability. |
| C2 | HIGH | `internal/engine/engine.go:98-114` | Per-target semaphore held through `Resolve` (released via defer after resolution) — `@runs(10, parallel)` with default `max_concurrency: 1` serializes ingestion waits: ~16-36s/scenario that could overlap. Fix: release after `drv.Run`. |
| C3 | MEDIUM | `internal/correlate/correlate.go:73-83` | Per-ref `GetByID` fetches are serial inside each poll round (multi-root runs pay ~RTT×refs×rounds). |
| C4 | MEDIUM | `cmd/mentatctl/main.go:119-139`, `internal/ctl/diff.go:38-42` | Replay/format/diff pay the full stable-poll for immutable historical traces; diff pays it twice (≥1.2s fixed). Needs a known-complete resolve mode. |
| C5 | LOW | `internal/store/tempo.go:117-124` | Resource attrs copied into every span's map on every fetch (S×R inserts), amplified by C1's refetching. |
| C6 | LOW | `internal/comparator/result_span.go:105-122`, `matchers.go:71,200-210` | `every`/`any` quantifiers recompile regex/JSON-Schema per matched span; compile once per expectation. |

## Cluster D — Observability & config integrity

| ID | Sev | Where | Finding |
|----|-----|-------|---------|
| D1 | HIGH (UX) | all of `internal/`, `cmd/` | Zero logging in non-test code. The classic first-user failure (SUT exports nowhere) is 30s of silence, then `correlate.go:98`'s error naming neither store endpoint, TraceQL query, nor injected env. No `--verbose`. |
| D2 | MEDIUM | `internal/config/config.go:73` | Config YAML unmarshal is not strict (`expectations.go:82` is): typo'd keys (`poll.timout`, `judge.vote`) silently fall back to defaults, changing verdict semantics. |
| D3 | MEDIUM | `internal/config/config.go:69`, `internal/engine/build.go:25-26` | `mcp`/`grpc` pass config validation but no driver is registered — failure deferred to mid-suite drive time instead of load/build time. |
| D4 | LOW | `internal/engine/engine.go:94`, `internal/driver/shell.go:27-34` | Unset `otlpEndpoint` exports `OTEL_EXPORTER_OTLP_ENDPOINT=""`, clobbering an inherited working endpoint (last-duplicate-wins); `OTEL_RESOURCE_ATTRIBUTES` replaces rather than merges ambient values. |
| D5 | LOW | `internal/steps/steps.go:238` | Span-ordinal `strconv.Atoi` error dropped → absurd ordinals clamp to MaxInt with a misleading downstream error. |
| D6 | LOW | `cmd/mentat/main.go:50-55`, `cmd/mentatctl/main.go:183-188` | Correlator is the one seam with no registry/builder — poll defaults copy-pasted across both mains, drift one edit away. |

## Cluster E — DX & product gaps (design-doc comparison)

| ID | Where | Finding |
|----|-------|---------|
| E1 | `internal/steps/steps.go:53-94` | ~30-step Gherkin vocabulary documented nowhere; README shows ~6; design-doc drafts drifted. Needs a generated step reference (docs and/or `mentat steps`). |
| E2 | `internal/steps/steps.go:98-111`, `internal/engine/engine.go:79` | No `mentat validate` dry-run: CEL precompile/shape prechecks/step binding/target cross-check all exist but only run at live drive time. |
| E3 | `cmd/mentat/main.go:76-85` | `--junit` swaps the godog formatter → console output lost; godog supports multiple formatters; design §10 promises both. |
| E4 | `internal/core/core.go:85`, `internal/driver/http.go:44` | `RunSpec.Input` has no writer — HTTP targets always POST an empty body; the design's `body fixture "order.json"` step is missing. |
| E5 | `internal/engine/store.go:17`, `internal/store/filestore.go` | Only `tempo` store is registered; fixtures can be written (`--save`) but never served — no offline replay, no docker-free CI smoke. |
| E6 | `internal/comparator/semantic.go:64-77`, `internal/report/derive.go:26-40`, `internal/config/config.go:115` | Judge spend invisible: cost derives only from SUT trace; no judge cost accounting, no suite budget, no verdict cache; default model is Opus-tier (`claude-opus-4-8`) for a binary verdict; `votes>1` at `temperature: 0` sends near-identical calls. |
| E7 | `internal/ctl/run.go:52-63` | `mentatctl agent run` summary omits designed fields (tokens, cost, latency, trace id); no `--prompt-file`/stdin/`-o`/per-call timeout flags. |
| E8 | `internal/core/core.go:179-180` | `ExtractAnswer` = whole trimmed stdout, unconditionally — agents that log to stdout pollute `the result …` assertions; design promises configurable extraction. |
| E9 | `mentat.yaml:5-7`, `e2e/report_meta_test.go:32,67` | SUT driven via `go run` per scenario (toolchain overhead ×N); report_meta e2e tests use `go run` + skip `t.Parallel()` against repo convention. |

## Cluster G — Public extension surface (one-way door; spec separately)

| ID | Where | Finding |
|----|-------|---------|
| G1 | `internal/core/core.go`, `internal/registry/registry.go`, `cmd/mentat/main.go` | Design §2/§7.1 promises "implement the interface + register a factory", but every seam interface and registry is under `internal/` — third-party drivers/stores/comparators require forking. Needs a `pkg/` surface and/or library-mode `mentat.Run(cfg, opts, registerFns...)`. |

## Cluster F — Test hardening (folds into clusters above)

| ID | Where | Finding |
|----|-------|---------|
| F1 | `internal/steps/steps.go:334,534` | Registered steps `resultAttrDoc` and `shapeFanoutExactly` have 0% coverage at any level — dead, unverified wiring. |
| F2 | `cmd/mentatctl/main.go:67` | `dispatch` (all flag parsing + verb routing) untested; `flag.ExitOnError` makes it untestable as written. Package 12.3%. |
| F3 | `internal/correlate/correlate_test.go:106…` | No unit test pins the `Query` argument — `Tag == "test.run.id"` (invariant §5's single implementing line) could regress silently. |
| F4 | coverage profile | Generated `internal/core/mocks` (0%) counted in the total → 80.4%, masking real-coverage regressions against the 80% floor. |
| F5 | missing | No e2e exercises the error-span path against live Tempo — precisely the test that would have caught A1. |
| F6 | missing | Semantic vote-loop partial-vote cancellation (deadline mid-votes must not compute a majority from partial votes) untested at the loop level. |

## Verified clean (do not re-audit)

- godog step state is per-pickle (verified against godog v0.15.1 source); no cross-scenario `world` leakage.
- Comparator failure messages, no-silent-fallback discipline in comparator/config/judge layers, CEL compile-once caches, judge vote fan-out + error classification, report collector locking, HTTP body closing, run-id lifecycle (fresh UUID per drive), e2e prebuilt-binary + parallel conventions (except E9).
- `go vet` clean, `gofmt` clean, `go build -race` clean; all tests pass.
