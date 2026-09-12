# Composition audit — 2026-09-11

Lens: code-composition principles — composition over inheritance, single responsibility,
loose coupling, interface-based design, high reusability — plus the repo's own composition
rules (IO at the edges, an abstraction needs a second implementation, ordering as types,
no package-level mutable state, every documented claim must hold) and two ergonomics axes:
adopting Mentat from an external repo, and authoring with it. Target: `main` @ `f6bb402`,
features 001–012 shipped. The `.claude/worktrees/` checkouts for 013/014 are excluded.

This audit does not repeat the 2026-07-01 audit (bugs, concurrency, performance); its
clusters A–G were consumed by specs 001–012 and its "verified clean" list is not re-checked.
Where a finding here overlaps a prior ID, the prior ID is cited.

Method: four parallel read-only reviews by axis (seams + composition root; steps/godog
layer; comparator + report + judge; IO edges + config), each returning file:line evidence
and separating OBSERVED from INFERRED. Every HIGH and MEDIUM below was then re-verified by
hand against the code, and the HIGH ones were reproduced with the built binary. The
ergonomics part was measured by running that binary from directories outside the repo.
Nothing in the repo was modified.

Baseline at `f6bb402` (observed): `go test ./... -count=1` → 24 packages ok, 0 failures,
7.7 s; `go vet` clean; `examples/kafkaecho` green in 0.35 s. Size: ~15 k non-test LOC in
18 internal packages, ~38 k test LOC, 2 063 test functions.

Finding IDs are stable: CO-n (composition), ER-n (ergonomics). Severity: HIGH = a
principle is violated and a concrete cost has already been paid or is reproducible;
MEDIUM = violated, cost plausible but not yet paid; LOW = cosmetic or a stale claim.

## Scorecard

| Principle | Verdict | Basis |
|---|---|---|
| Composition over inheritance | HOLDS, exhaustively | `go/ast` scans over every non-test file in `internal/`, `cmd/` and the root package (127 files, mocks excluded): 0 struct embeds, 0 interface embeds |
| Interface-based design | HOLDS at the seams | 11 seam interfaces in `core`/`result`; one composition root that seals a per-engine registry; facade of zero-cost aliases guarded by a golden gate that renders method sets; the `PrecheckEngine` and `stepRegistrar` consumer interfaces each have a real second implementation |
| Loose coupling | PARTIAL | import graph acyclic; but three leaf packages import `registry` to self-register (CO-6), the result comparator takes the concrete registry for one lookup (CO-5), and the composition prelude is copied into four places (CO-3) |
| Single responsibility | PARTIAL | `world` and `steps.go` are one responsibility each (measured, not split); `run.go` carries two copies of the same 45-line assembly; `budgets.go` and `matchers.go` each hold two edit-reasons (LOW) |
| High reusability / earned abstraction | PARTIAL | Driver (2+1 external), TraceStore (3+1), Comparator (6), Matcher (7), Reporter (3) earn their seams. `AggregateComparator` (1 impl, literal-named), the store registry inside `BuildStore` (always empty in every engine), the option middle layer (11 types for one shape) and three orphan facade aliases do not (CO-4, CO-7, CO-8, CO-9) |
| Ordering as types | **FAILS in one place that matters** | a `Then` with no `When` is compared against zero Evidence and reports PASSED (CO-1); `Build*` accept an unresolved `Config` and two silent defaults exist only to keep that legal (CO-2) |
| IO at the edges | PARTIAL | drivers, stores, judge and both binaries are proper edges; reads beneath the facade take a path string plus `os` in `steps`, `expectations` and `filestore` (CO-15); the clock is called, not injected, in `correlate` (CO-14) |
| No package-level mutable state | HOLDS with one exception | every package-level `var` is written only at declaration, except the two compile seams in `comparator/matchers.go:34-37`, reassigned by one serial test (CO-12) |
| Documented claims hold | PARTIAL | 21 claims tested; 9 do not hold or hold only partially (§1.4) |

## Part 1 — Composition findings

### 1.1 HIGH

| ID | Where | Finding |
|---|---|---|
| **CO-1** | `internal/steps/steps.go:290-309` (`checkExp`) vs the guard at `:632-634` (`checkRuns`) | **A `Then` step with no `When` before it is compared against a zero-valued Evidence and reports PASSED.** `checkExp` hands `w.ev` to `Engine.Compare` with no "was anything driven" check; its sibling `checkRuns` has one. Reproduced with the built binary on a feature whose scenarios contain no `When`: `Then the result equals ""`, `the response status is 0`, `the result contains ""`, `the result matches regex ".*"` and `the run satisfies "true"` all PASS; `mentat validate` reports "no issues found"; the JSON report records `Pass=true` with zero `Runs` and no `RunIDs`. Trace-reading steps fail loudly (`budgets: Evidence.Trace is nil`, `cel: binding "tokens": evidence has no trace`); `json-contains` and `matches schema` fail for the wrong reason (`unexpected end of JSON input`, `got null, want object`). So the hole is exactly the Output-only matchers (exact / contains / regex / status) and trace-free CEL. This is the ordering-as-hidden-state failure the lens names (a mutation that may not have run, plus an accessor that returns a valid-looking answer), and it is the mirror image of prior-audit A2 (assertion-free scenarios passed when the SUT never ran). Fix shape, three layers: (1) mirror `:632` in `checkExp` — `if len(w.evs)==0 { return fmt.Errorf("%s: no run driven; use a When ... step first", name) }`; (2) defence in depth in `report.Derive`: a scenario with `Pass && len(Runs)==0` is a derivation defect, not a result; (3) a static `validate` class for a `Then`-family step with no preceding `When` in the pickle, so it is caught before any SUT is touched. |
| **CO-2** | `internal/engine/build.go:30` (signature), `:100-106`, `:143-146`; `internal/config/config.go:283-293`, `:359-364`, `:234-242`; `run.go:289-299` | **`Build`, `BuildStore` and `BuildCorrelator` accept an unresolved `config.Config`; "Resolve before ANY composition call" is a comment, and the cost has already been paid twice.** (a) `judge.votes` is validated in `Build` (`:100-106`, "must be a positive odd integer") and again in `config.validateJudge` (`:359-364`, "must be >= 1" / "must be odd … majority is undefined on an even-N tie"): one rule, two places, different wording. (b) `build.go:143-146` clamps `MaxConcurrency < 1` to 1 with no comment. `Resolve` already guarantees ≥1 (`:283-293`); coverage shows the clamp is reached only by `engine`'s own tests, which hand `Build` an unresolved target (`build_test.go:269`). The clamp makes "Resolve never ran" indistinguishable from "resolved to 1", and `config.go:234-242` itself documents that the sibling `> 0` guards (deadline, `WaitDelay`, settle) silently *disarm* when Resolve is skipped. So the codebase already contains, in its own words, the reason this ordering should be a type. Fix shape: `Resolve` returns a `config.Resolved` value (also closing CO-11) and `Build*` accept only that; delete the votes re-check and the clamp; engine tests call `Resolve` first. Merges reviewer findings A2 and D1. |
| **CO-3** | `run.go:254-347` (`Run`), `run.go:631-687` (`buildEngineForInspection`), `cmd/mentatctl/main.go:207-232, 305-323`, `cmd/mentat/validate.go:120-140` | **The "single composition root" is assembled in four places, and one of them has already diverged.** `Run` and `buildEngineForInspection` each carry the same 45-line prelude (Targets copy → `Resolve` → logger → correlator → store funnel → `BuildStore` → `engineOptions` → `Build`); a normalised diff shows they differ only in variable names, return types and two guards. `Run` does not call the shared builder. `mentatctl` is a third assembly. The binary's `validate` is a fourth: it builds a `checker` from two comparators and never calls `engine.Build`, so the adapter-has-a-driver check is skipped — measured: `validate` says "no issues found" on `adapter: kafka`, `run` refuses it at build (ER-1). `run.go:616-622` claims inspection "can never answer about a different engine than the one a run will use"; that holds for the library path by copy discipline and does not hold for the binary. Fix shape: `Run` calls the shared builder; the CLI checker validates each target's adapter against the driver names it can see (`shell`, `http`), or the README states the limit beside the contributed-phrases limit. Merges A1, A15 and ER-1. |

### 1.2 MEDIUM

| ID | Principle | Where | Finding | Fix shape |
|---|---|---|---|---|
| CO-4 | Over-abstraction (middle layer) | `run.go:67-131, 196-234, 476-523`; `internal/engine/options.go`; `build.go:172-238`; `store.go:38-50` | Five `xReg` structs in `run.go` mirror six `namedX` structs in `options.go` (11 types, one `{name, factory}` shape). The nil-factory check appears at 11 sites across three layers. Six closures wrap factory types that are already identical by alias; a reviewer removed all six and their two then-unused imports in an overlay build and `go build` + `go vet` passed. ~626 lines to carry five name→factory pairs into seven maps. | `runOptions` accumulates `[]engine.Option` directly; delete the `xReg` types; nil-check once, in the engine. |
| CO-5 | Accept interfaces; ordering by hidden state | `internal/comparator/result.go:34,45`; `result_span.go:45`; `build.go:47` vs `:52,107,114` | `NewResult(reg *registry.Registry)` takes the whole concrete registry and uses exactly one method, `Matcher(string)`. It captures the registry while it is still OPEN (line 47) and the matchers it will resolve are registered later (52, 107, 114); nothing in the types says the handle will hold them. Through `registry`, the "portable" comparator package transitively imports `config` and `result`. The correct pattern already exists one package over: `report/emit.go:19` `ReporterResolver`. | `type MatcherLookup interface{ Matcher(string) (core.Matcher, bool) }` in `comparator`; `*registry.Registry` satisfies it unchanged; register the result comparator after the matchers. |
| CO-6 | Single composition root / import direction | `comparator/matchers.go:22`; `judge/judge.go:9`; `report/register.go:12`; callers `build.go:52,58,68` | Three leaf packages import `registry` so the root can delegate registration to them, while `Build` wires comparators inline and registers the `semantic` matcher itself (`:107`). "The leaf owns its list" is therefore already split across two files. Harmless today; the cost is three leaf→registry edges and the matcher list in two places. | Leaves export data (`comparator.BuiltinMatchers() []core.Matcher`, `report.Builtins()`), the root registers. The `JudgeFactory` type would move to `core` or the judge leaf keeps its import. |
| CO-7 | Second-implementation test | `core.go:109`; `registry.go:142-153`; `build.go:51`; `engine.go:143-159, 267-270`; `steps.go:637`; `precheck.go:248` | `AggregateComparator` has one implementation (`aggregate_cel.go`), no public hook (011/012 and `new-seam.md:47` say so), and every lookup passes the literal `"aggregate-cel"`. A sealed, mutex-guarded map with one entry, keyed by a literal, is a field with extra steps. Keep the interface: the gomock and `validate.go`'s `checker` are real consumers. | Hold it as an `Engine` field until a second implementation or a public hook is committed. |
| CO-8 | Over-abstraction | `internal/engine/store.go:23-63`; grep `RegisterStore` | `BuildStore` constructs a full 7-map `Registry`, registers two store factories, resolves one, seals the registry and discards it. `RegisterStore` is called nowhere else, so the engine's own registry has an always-empty `stores` map and `BuildStore`'s registry has six always-empty maps. The seal on a discarded local protects nothing. | A `map[string]StoreFactory` literal inside `BuildStore`; drop `stores` from `Registry`. |
| CO-9 | Every documented benefit must hold; over-export | `mentat.go:94-97, 188-196`; `surface_test.go:1373-1380`; `specs/007-public-extension-api/contracts/public-surface.md:5`; `core.go:130,172,204` | (a) `Correlator`, `CompletenessContract` and `ResolveRequest` are aliased "so an external Correlator implementation can name them", but no public signature references them and there is no hook to plug one in; the nameability sweep seeds from every alias and walks outward only, so an orphan alias is invisible to it. (b) The 007 manifest rule "appears here with a justification, or it does not get exported" has zero entries for eleven current public symbols (`Correlator`, `Reporter`, `WithReporter`, `ReporterFactory`, `PhraseContributor`, `CaptureParser`, `ExpectationParser`, `StepDoc`, `Finding`, `Validate`, `StepReference`). (c) The three optional seams `ExpectationParser`, `PhraseContributor`, `CaptureParser` have zero non-test implementations anywhere in the module, including `examples/`; every adopter is the first implementer. | Drop the three orphan aliases until a hook exists, or add the hook. Enforce the manifest from the golden or delete the rule. Have one built-in comparator contribute a phrase, or label the seams experimental. |
| CO-10 | Single source of truth | `internal/steps/steps.go:24-30`; `precheck.go:73` vs `metadata.go:121,311,318,325,332,397` | Six regex literals re-declare `stepDefs` patterns byte-for-byte for the prechecks (CEL precompile, shape, target). No test pins them to the table. Editing a `stepDefs` row leaves the precheck matching the old text: validation silently stops firing for that step while the step still runs, and `mentat validate` reports CLEAN — the drift class 012 closed for registration but not here. | Derive the six from `stepDefs` (lookup by handler identity or a `key` field), or a pin test asserting `re.String() == stepDefs[i].pattern`. |
| CO-11 | Ownership as convention | `config.go:334`; `run.go:281-287`; `run.go:645-651`; `contracts/config-resolve.md` | `Resolve(*Config)` writes `c.Targets[name] = t` into a map the caller may share. Both facade callers carry a verbatim 7-line defensive copy plus a "DO NOT simplify this away" comment; two tests guard it; the contract doc's Laws 1–4 never mention the hazard. The memory notes record this nearly re-breaking in 009. | `Resolve(cfg Config) (Config, error)` allocating a fresh `Targets`; 3 production + 8 test call sites. Subsumed by CO-2's `Resolved` type. |
| CO-12 | No package-level mutable state | `comparator/matchers.go:34-37`; writers `matchers_test.go:70,80` | `compileRegexp` and `compileSchemaDoc` are package vars reassigned by one test to count compilations, restored via `t.Cleanup`, unguarded. Safe only because that test is serial (its comment says so) and Go holds top-level parallel tests until serial ones finish. Adding `t.Parallel` there, or a second swapping test, races silently. | Inject: `regexMatcher{compile func(string) (*regexp.Regexp, error)}` set in the registration helper; the test builds its own instance. |
| CO-13 | Ordering as a type | `matchers.go:104-122, 238-260`; consumers `result.go:51`, `result_span.go:52` | `Compile(want) (core.Matcher, error)` returns the same type as the uncompiled prototype, and both `regexMatcher.Match` and `schemaMatcher.Match` detect a nil artifact and compile per call. The comment "the result comparator always compiles first" holds today, but nothing enforces it: any future caller that skips `compileMatcher` re-opens prior-audit C6 silently. | `Compile` returns a distinct bound type; prototypes do not implement `Match`; the registry holds a `MatcherFactory`. |
| CO-14 | IO at the edges (clock) | `internal/correlate/correlate.go:162, 251, 269, 304, 341` | `time.Now` / `time.After` are called directly; no clock is injected anywhere in the repo. Tests use real 1 ms sleeps, real 150/120 ms settle windows and wall-clock bounds labelled "generous, CI-safe" and "deliberately NOT t.Parallel". `correlate` is pure orchestration over an interface, not an edge. `PollConfig` is internal-only, so injection has no public cost. | `Now` / `After` funcs on `PollConfig` or a `WithClock` option; the settle and deadline tests become deterministic. |
| CO-15 | IO at the edges (reads) | `steps/steps.go:224`; `steps/suite.go:103`; `expectations/expectations.go:37,60`; `store/filestore.go:205,215` | Four readers beneath the facade take a path string and call `os.ReadDir`/`os.ReadFile`. Their tests are disk-bound (`t.TempDir` at 9 sites in `steps`, 3 in `expectations`, 10 in `store`). An injected `fs.FS` costs no public seam (all callers are internal), removes disk from every `SuiteCheck` test, and rejects `..` — but collides with the documented "absolute fixture path used as-is" for the body-fixture step, which would have to become a deliberate decision. | `SuiteCheck.FS fs.FS` + `fs.Sub` per feature dir; `expectations.Load(fs.FS)`; `NewFileStore(fs.FS, name)`; `os.DirFS` at the two binaries. |
| CO-16 | Seam shape leaks a strategy | `core.go:278-292`; `store/filestore.go:110-160`; `correlate.go:428-437` | `FetchPayload`/`DecodePayload` exist for the correlator's per-round byte-hash change detection. Tempo and `FileStore` fit (bytes are native); `InMemStore` must JSON-marshal the forest and on decode rebuild Roots↔Spans pointer aliasing by ID, ~40 lines and an error path that exist only to satisfy the seam, which `core.go:281-284` codifies. Every future store and the gomock pay the round-trip. | The seam returns an observation `{Key comparable; Forest *trace.Trace}`: Tempo keys by byte hash, `InMem` by generation. |

### 1.3 LOW

| ID | Where | Finding |
|---|---|---|
| CO-17 | `registry.go:34-36, 74-81` | All 24 `Register*` call sites are inside `Build`/`BuildStore` before `Seal`; godog goroutines start after `Build` returns. The `RWMutex` guards no production race; the `sealed` flag is the guard. The comment credits the mutex. Cheap, keep; fix the comment. |
| CO-18 | `engine.go:181-188`; `ctl/replay.go:21` | `PinRun` "MUST be called before any concurrent Drive" by comment; three methods branch on `e.pinned`. Its sole caller pins immediately after `Build`, so `engine.WithPinnedRun(id)` would fix it at construction. |
| CO-19 | `engine.go:27` vs `:39-44`; `engine_test.go:185` | "Build is the only way to construct it" and the lazy `sync.Once` that "keeps every existing construction path working" cannot both be true; the other path is a test's `&Engine{}` literal. |
| CO-20 | `build.go:30-33` | `Build` accepts nil `st`/`cor` ("nil in tests"); `isNilSeam` guards factory outputs but not the two direct parameters. |
| CO-21 | `run.go:113-121`; `registry.go:22-27`; `new-seam.md:21,24,34-39`; `mentat.go:15-17,24-27`; `public-surface.md:17` | Five documents disagree with the code and each other on the seam count ("six", "four have options"; the registry has seven maps and there are five `With*` options); `registry.go` calls reporters package-global in a file that records their move in 010; `run.go` says 011 "has not been started". |
| CO-22 | `new-seam.md:137-175`; `git show --stat 1206a56` | Feature 010 touched 19 non-test, non-spec files; the checklist omits the `Engine` accessors and the consumer interface, and items 8 and 10 of the checklist were not done by the feature that wrote it. |
| CO-23 | `phrase.go:15-16`; `stepargs.go:89-214` | "This file is the ONLY place that knows godog's step-handler signature rules" — `stepargs.go` encodes the same four measured behaviours. A godog bump has two places to re-measure. |
| CO-24 | `phrase.go:57-62` | "Reflection is forced, not chosen": the arity corpus (built-ins 0–5 captures, contributed 0–2) is bindable by ≤12 fixed closures with compile-time types and none of the typed-nil hazard the file records. Defensible; the wording overclaims. |
| CO-25 | `phrase.go:23-35` vs `stepargs.go:58-60` | The FR-010 package-state enumeration omits three `stepargs.go` vars; `docType` and `docHandlerType` are the same value declared twice. All immutable. |
| CO-26 | `steps.go:250-253`; `:224-231` | `if n < 1 { n = 1 }` masks a world whose Before never ran (reachable only from tests); the body fixture is read before `requireBodyAdapter` refuses a shell target. |
| CO-27 | `comparator/cel.go:42-73` vs `aggregate_cel.go:38-69`; `cel/cel.go:81-99` vs `cel/aggregate.go:63-80` | The program cache (RWMutex double-check + map) is byte-identical modulo the program type; the leaf bindings are composed, the dispatch is copied. Two real implementations exist, so a generic `programCache[P]` is earned by the lens. |
| CO-28 | `judge/claude.go:44,50,68` | `NewClaude` constructs the SDK client internally (injectable constructor unexported); `os.Getenv(apiKeyEnv)` is read inside `Judge` at call time — documented, but the process env is reached from inside the method. |
| CO-29 | `comparator/cel.go:113`; `judge/judge.go:7-8`; `comparator/semantic.go:24-25,51-54`; `report/ledger.go:42-60` | Stale "added in later tasks" (already bound); "re-registers the same factory" holds only pre-Seal; `votes < 1` clamped to 1 with a comment calling it defence in depth (the silent-default shape CLAUDE.md names); `Price` mutates its argument in place. |
| CO-30 | `comparator/budgets.go`; `comparator/matchers.go` | `budgets.go` holds the comparator plus the cost/token derivation library consumed by `cel`, `aggregate_cel`, `report` and `mentatctl`; `matchers.go` holds registration, the compile lifecycle and six matchers. Two edit-reasons per file. |
| CO-31 | `core.go:276,291`; `tempo.go:38,116-118` | `Caps() StoreCaps{StructuralQuery}` is implemented by all three stores and the mock and read by nobody; `GetByID` "remains for one-shot callers" has 0 non-test callers and 24 test callers. |
| CO-32 | `driver/http.go:38-41`; `driver/options.go`; `tempo.go:31-34` | The HTTP driver's client is not injectable and its 30 s timeout is untested; `driver.Option` has exactly one option (`WithLogger`), which does not earn the pattern until `WithHTTPClient` exists; Tempo's nil-client 30 s default is undocumented. |
| CO-33 | `internal/ctl/run.go:57,65`; `ctl.go:29-44` | `ctl.Run` takes an `io.Writer` yet calls `os.WriteFile` in the same function; `~/.mentat/last` is read and written via `os.Getenv("HOME")` inside the package. |

### 1.4 Documented claims tested

| Claim | Verdict | Evidence |
|---|---|---|
| `build.go:19` "single composition root" | PARTIAL | four assembly sites, one diverged (CO-3) |
| `run.go:616-622` inspection and run compose the same engine | PARTIAL | holds by copy discipline for the library; false for the binary (CO-3) |
| `run.go:317-319` custom factory "passed through untouched" | DOES NOT HOLD as written | wrapped in an identity closure; removable (CO-4) |
| `matchers.go:112-114` "the result comparator always compiles first" | HOLDS | both call sites; nothing enforces it (CO-13) |
| `registry.go:34-36` mutex prevents post-seal races | PARTIAL | the seal flag does; the mutex guards no production race (CO-17) |
| `metadata.go:7` `stepDefs` is the single source of truth | PARTIAL | true for registration (drift test); six precheck literals are unpinned copies (CO-10) |
| `phrase.go:15-16` only place that knows godog's rules | DOES NOT HOLD | `stepargs.go:89-214` (CO-23) |
| `phrase.go:59` reflection "forced" | PARTIAL | fixed-arity route exists (CO-24) |
| `mentat.go:95` Correlator "contracts reference it" | DOES NOT HOLD | no public signature does (CO-9) |
| `public-surface.md:5` manifest rule | DOES NOT HOLD | 0 entries for 11 current symbols (CO-9) |
| `engine.go:27` "Build is the only constructor" | DOES NOT HOLD | `engine_test.go:185` (CO-19) |
| `new-seam.md:21,24` six seams / four options | DOES NOT HOLD | seven maps, five options (CO-21) |
| `stability.md:58-61` golden renders interface method sets | HOLDS | `surface_test.go:1085-1112` |
| `stability.md:127-133` sweep walks seam signatures | HOLDS, outward only | orphan aliases pass (CO-9) |
| `new-seam.md:123-129` collision check runs before the factory | HOLDS | `build.go:184-234`, `store.go:46-48` |
| `expectations/clause.go:2-3` comparator never imports expectations or touches files | HOLDS | `go list`; grep `os.` in comparator: 0 |
| `judge/claude.go:2-3` only package importing the Anthropic SDK | HOLDS | grep |
| `result/result.go:12-13` `core` imports nothing from `result`; `result` is a leaf | HOLDS | `go list` |
| `config.go:236-240` downstream `> 0` guards disarm when Resolve is skipped | HOLDS, and is why CO-2 is HIGH | `build.go:145` coverage split |
| every `Resolve` caller copies `Targets` | HOLDS | `run.go:281-287, 645-651`; `config.go:212` owns its value |
| `testio.go:3` production never imports it | HOLDS, unenforced | importer grep empty |

### 1.5 Verified clean (do not re-audit)

- Zero struct or interface embedding in any non-test file (AST scans by three reviewers over disjoint axes; the regex grep's only hits were `iota` consts).
- Per-engine registries hold: `registry.New()` at `build.go:41` and `store.go:23`; no package-global seam state; `TestRunDoesNotMutateCallerConfig` pins `Run`'s Targets copy; `mentatctl` gets `Resolve` through `config.Load`.
- Every package-level `var` in `internal/`, `cmd/` and the root is written only at its declaration, except CO-12. `Engine.resolveOnce` is a field, not a global. `universalFlags`/`verbFlags` are read-only tables.
- `isNilSeam` is earned (catches `(*T)(nil)` inside an interface) and pinned at five test sites.
- `PrecheckEngine` (3 methods) and `stepRegistrar` are both earned by real second implementations (`cmd/mentat/validate.go:177`, `metadata_test.go:12`). A `world`-level Engine interface (10 of 15 methods used) has no second implementation in code or in `specs/`; leave it concrete.
- `world` per-scenario isolation observed; After hook runs after a Before failure and records a correct empty; all nine docstring and both table handlers nil-guard.
- `internal/trace` is pure (`time` as types only); `genai` is constants and the single source of `gen_ai.*` keys for reads; `internal/result` imports only `core`.
- Reporter seam honoured: all three built-ins write only to the injected writer; the only file IO in `report` is the atomic temp-file rename at the package's outermost function.
- Drivers: pure logic (`mergeResourceAttrs`, `otelEncode/Decode`, `scenarioFromArgs`, `buildBaggage`) is split from the exec/http edge; process-group lifecycle tests spawn real `sh`/`sleep` and that is the right trade at that edge. Tempo's HTTP client is injected.
- Run ids come from `uuid.NewString()` injected at the composition root (`engine/correlator.go:66`). No `rand.`, `net.Dial`, `http.DefaultClient` anywhere.
- `go vet` and `go test -race` clean on every package the reviewers compiled.

## Part 2 — Adoption and user ergonomics (measured, not inferred)

Everything in this part was run on 2026-09-11 from directories OUTSIDE the repo, using
a binary built from `main` @ `f6bb402`, against the `deploy/` Tempo + collector stack and
the orderflow SUT that were already running on this machine. Scratch inputs live only in
the session scratchpad; the repo was not modified.

### 2.1 Cold start from an external directory

| Step an adopter takes | What happened | Cost |
|---|---|---|
| Obtain the binary | No tags (0), no release workflow, no `go install` path in README or CI. The only route is clone + `go build ./cmd/mentat`. | cold build 7.2 s wall; 39 MB binary |
| Bring up a trace backend | `deploy/docker-compose.yml` (Tempo 2.5.0 + collector 0.105.0). An adopter with their own Tempo skips this. | not measured (stack was already up) |
| Write `mentat.yaml` for an HTTP service target | 8 lines derived from the README quickstart. `validate` accepted it. | 1 file |
| Write one feature | 15 lines, copied from the README's step vocabulary (`mentat steps`, 302 lines, 7 groups). | 1 file |
| `mentat validate features/` | `validate: no issues found`, exit 0 | < 0.1 s |
| `mentat run -v features/` (HTTP target, orderflow) | 1 scenario, 7 steps, all passed | 6.0 s (resolve 6.03 s, 29 poll rounds) |
| Same for a spawned agent target (`bin/researchbot`) **without** `otlpEndpoint` | RED at `When I run scenario`: trace-not-found after the 5 s poll, with a 3-item checklist | 5.2 s |
| Same **with** `otlpEndpoint: http://localhost:4318` | 1 scenario, 5 steps, all passed | 9.0 s (resolve 9.02 s, 43 rounds) |
| Library route (`examples/kafkaecho`: custom driver + store + test, separate module, facade-only imports) | `go test` green | 270 LOC total; 0.35 s test |
| Offline replay, README recipe verbatim (`mentatctl agent replay <id> --feature <f> --config <c>`) | FAILED: `mentatctl: replay: --feature is required to re-evaluate a run` | see ER2 |
| Offline replay, flags before the id | Ran, then RED on `the result contains "Q3 revenue"` with `got ""` | see ER3 |

Per-scenario wall time is dominated by the completeness barrier (5 s request-kind settle,
2 s spawned-kind settle) plus ingestion lag, not by Mentat's own work. That is the price
of the "complete trace or loud error" guarantee (feature 008) and is tunable per target.

### 2.2 Authoring mistakes, graded against the repo's own error standard

The standard (CLAUDE.md, Constitution IV): name what failed, the value involved, and what
to do. Grade: A = all three, B = what + value, C = what only.

| Mistake | `validate` output | Grade |
|---|---|---|
| Misspelled step (`in orderr:`) | `bad/typo.feature:5: [unbound-step] no step matches "the services are called in orderr:"` | B (no "did you mean", no pointer to `mentat steps`) |
| Target not in config | `bad/unknown_target.feature:3: [unknown-target] unknown target "nope" (not a configured target)` | B (does not list configured targets; `run`'s adapter error does list the registered set) |
| Docstring under a step that takes none | `[step-argument] step "the response status is 201" carries a docstring, but the built-in step "^the response status is (\d+)$" cannot receive one — it would be silently discarded …` | A |
| CEL that does not compile | `[bad-cel] cel: compiling "tokens >>> 5": ERROR: <input>:1:9: Syntax error …` with caret | A |
| Typo'd YAML key (`poll.timout`) | `bad.yaml:0: [config] … line 2: field timout not found in type config.PollSpec` | A (strict unmarshal; prior-audit D2 is closed). `:0` is a placeholder line. |
| Adapter with no driver (`adapter: kafka`) | `validate: no issues found`, exit 0. `run`: `mentat: build engine: engine: target "orders": adapter "kafka" has no registered driver (registered: http, shell)` | validate: **miss** (ER1); run: A |
| SUT exports no trace (no `otlpEndpoint`) | `correlate: no trace for run "…" within 5s (0 spans seen)` + store + TraceQL query + checklist `(1) is the collector/Tempo up? (2) does the SUT export OTLP to the endpoint above? (3) were OTEL_RESOURCE_ATTRIBUTES applied? (run with -vv …)` | B (see ER4: "the endpoint above" is Tempo's query port, and the fix key is never named) |
| Unsupported flag on a `mentatctl` verb | `mentatctl: flag "--prompt" is not supported by the "replay" command`, exit 1 | A |
| `validate` where `features/` does not exist | two findings for one cause: `[no-features] no .feature files found under features` and `[path] stat features: no such file or directory` | LOW (ER6) |

### 2.3 Ergonomics findings

| ID | Sev | Where | Finding |
|---|---|---|---|
| ER1 | MEDIUM (the ergonomics face of CO-3) | `cmd/mentat/validate.go:120-140` vs `run.go` `buildEngineForInspection` | The **binary's** `validate` never calls `engine.Build`: it constructs a `checker` from two comparators and the built-in step patterns. So the adapter-has-a-driver check (`engine/build.go` target loop) is skipped, and `validate` certifies a config `run` refuses at build. The **library** `mentat.Validate` builds a real engine and would catch it. README advertises validate as checking "targets". This is the Validate/Run asymmetry class 012's D7 exists to remove, reachable from the binary with the built-in adapter set alone. Fix shape: have the CLI checker validate each target's adapter against the built-in driver names it can see (`shell`, `http`), or document the limit beside the contributed-phrases limit. |
| ER2 | MEDIUM | `README.md:148` | The replay recipe puts the run id before the flags. `mentatctl` parses with Go's `flag`, which stops at the first positional, so `--feature`/`--config` are never seen and the command fails asking for `--feature`. Verified: flags-first works. Fix: swap the order in the README, or parse flags after positionals. |
| ER3 | MEDIUM (Constitution IV) | `internal/engine/engine.go:293`, `internal/ctl/save.go:21-23`, `README.md:136-155` | A pinned replay returns `Evidence{RunID, Trace}` with **zero Output**, and `--save` writes only `runScenario` + `spans`. Any `the result …` step on replay therefore reports `result contains: want "Q3 revenue", got ""` — a verdict computed over fabricated empty output, the same shape as prior-audit A6. The README says a saved fixture "drives a suite green"; the hermetic proof (`filestore_replay_test.go`) gets its output by actually driving an echo SUT on the live path, which the documented `mentatctl agent replay` route never does. Fix shape: hard-error on a result step under a pinned run ("replay carries no driver output"), or persist Output in the fixture. |
| ER4 | MEDIUM | `README.md` (0 hits for `otlpEndpoint`), `docs/` (0 hits), `internal/correlate` checklist text | `otlpEndpoint` is the one key a spawned-agent adopter must set and it is documented nowhere user-facing; it appears only in the repo's own `mentat.yaml`. The trace-not-found checklist's item 2 says "the endpoint above", but the endpoint printed is Tempo's query endpoint (3200), not the OTLP ingest endpoint (4318), and the key is never named. Verified with `-vv`: unset → no `OTEL_EXPORTER_OTLP_ENDPOINT` injected (prior D4 is closed), so the SUT exports wherever its own default points. Fix: document the key; make checklist item 2 say `set otlpEndpoint (currently unset)` when it is unset. |
| ER5 | LOW | `cmd/mentatctl/main.go` `bindRunFlags` + `verbFlags` | One flagset is bound for every verb, so `mentatctl agent replay -h` lists `-prompt`, `-scenario`, `-save`, `-o`, … which `checkFlags` then rejects for `replay`. The help contradicts the guard. |
| ER6 | LOW | `cmd/mentat/validate.go` path walk | A missing `features/` yields two findings for one cause. |

Observation, not a finding: the CLI defaults an empty path list to `./features` while the
library `Run` refuses it ("no feature paths"). Both are documented choices; they differ.

### 2.4 Does it provide value, and to whom

What an adopter gets that a hand-rolled godog + Tempo client would have to rebuild:

1. **Correlation done right**: per-run `test.run.id` injection, tag-first resolution, the
   stability poll, and the completeness contract (settle / strict sentinel) with the
   ingestion-window qualifier on verdicts. This is the genuinely hard part, and it is the
   part the prior audit found most defects in (A3, C1, C2) and features 004/008 fixed.
2. **A tested assertion vocabulary**: 40 steps over tool order, budgets (tokens/cost/latency),
   result matchers (incl. the LLM judge), CEL over the span forest, shape selectors, and
   service order for microservices — the same feature file grammar for an agent and a service.
3. **Authoring tooling**: `validate` (static, no SUT), a generated and drift-tested step
   reference, and loud errors on the common mistakes (table 2.2).
4. **Proof it goes red**: the L3 meta corpus (`features/meta/`) is run nightly against live Tempo.
5. **An extension surface that provably suffices**: a separate module with its own `go.mod`,
   facade-only imports, CI-enforced by a grep tripwire, 270 LOC to add a driver + store.

What it costs an adopter:

- A Tempo + collector, and OTel instrumentation on the SUT that honours
  `OTEL_RESOURCE_ATTRIBUTES` (spawned) or baggage (http/grpc). Without OTel there is no product.
- Cloning to build (no release), and knowing about `otlpEndpoint` (ER4).
- 6–9 s per scenario today, dominated by the completeness barrier.
- Result-only assertions are cheaper in a plain test; Mentat pays off when the assertion is
  about *how* the system behaved (which tools/services, in what order, within what budget),
  which a boundary-only test cannot see.

Verdict: the value is real and specific to teams already exporting OTel who want behavioural
assertions on agents or service chains. The adoption path works end to end from an external
directory in two files, but the documentation has three holes an adopter will hit in their
first hour (ER1, ER2/ER3, ER4), and none of them is a code-architecture problem.

## Part 3 — What to do first

Ordered by verdict-integrity first, then by how much ceremony each removes per line changed.

1. **CO-1** — three-line guard in `checkExp`, a `Pass && len(Runs)==0` refusal in `report.Derive`, and a static `validate` class for a `Then` with no `When`. Closes a green verdict nobody earned. Pin with an in-process L3 test that the no-When feature goes RED; the probe feature in this audit is the corpus.
2. **CO-3 / ER-1** — `Run` calls the shared builder; the CLI checker validates adapters against the built-in driver set. Then the README's list of what `validate` checks is true for both entry points.
3. **CO-2 + CO-11** — `config.Resolved` returned by `Resolve`, accepted by `Build*`; delete the votes re-check, the concurrency clamp and both defensive copies. Engine tests resolve first.
4. **CO-5, CO-6, CO-7, CO-8** — the registry diet: `MatcherLookup` for the result comparator, leaves export builtins and the root registers, `aggregate-cel` becomes an `Engine` field, `BuildStore` uses a map literal. Each is independent and small.
5. **CO-4** — the options diet, after 4, since it shares the same files.
6. **CO-10, CO-12, CO-13** — pin the six precheck literals to `stepDefs`; inject the compile funcs; give `Compile` its own return type.
7. **ER-2, ER-3, ER-4** and the LOW doc drifts (CO-21, CO-23, CO-25, CO-29) — one documentation pass. ER-3 also needs a decision: hard-error on a result step under a pinned run, or persist Output in fixtures.
8. **CO-14, CO-15, CO-16** — clock and `fs.FS` injection, and the store-observation seam, when the disk-bound or timing-bound tests start costing something. None has a public-surface cost today.

CO-9's three items are a decision for Q, not a task: drop the orphan aliases or add a
Correlator hook; enforce the 007 manifest or delete the rule; ship one built-in contributed
phrase or label the optional seams experimental.

## Overlaps with the 2026-07-01 audit

| This audit | Prior ID | Relation |
|---|---|---|
| CO-1 | A2 | mirror image: A2 was assertion-free scenarios passing when the SUT never ran; CO-1 is assertions passing when nothing ran |
| ER-3 | A6 | same shape: a verdict computed over fabricated zero Output, now on the replay path |
| CO-2 | B5, D3 | D3 moved adapter validation to Build; B5's build-once discipline became the seal; the remaining comment-enforced ordering is Resolve-before-Build |
| CO-13 | C6 | C6 introduced compile-once; the per-call fallback is the door C6 left open |
| ER-4 | D4 | D4 is closed (unset endpoint is no longer injected); the documentation half was never written |
| ER-1 / CO-3 | E2 | E2 asked for `validate`; the binary and library implementations diverged on the way in |

## Method notes for the next audit

- Four read-only reviewers sharing one scratchpad collided once: one overwrote another's
  `overlay.json` mid-run, and the affected reviewer re-ran under a unique path. Give
  parallel reviewers unique scratch subdirectories.
- Two briefs contained a wrong premise that the reviewers corrected from evidence:
  `config.Load` takes `[]byte` and imports no `os` (the file reads live in callers), and
  the seam count in `new-seam.md` is out of date. A reviewer that pushes back on its brief
  is worth more than one that confirms it.
- Every HIGH here was reproduced with the built binary, not only by reading. Reading found
  CO-1; the binary showed which five steps pass and that `validate` and the JSON report
  both let it through.
