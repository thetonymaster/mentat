# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0/).

## [Unreleased]

### Changed

- **BREAKING (authoring): a step carrying an argument its definition cannot receive is
  now rejected.** godog discards any argument a step handler did not declare — the
  conversion loop runs `i < numIn` — so a docstring or data table written under a step
  that takes neither was dropped in silence and the scenario reported **PASSED** with the
  expectation never read. Measured on the pinned `godog v0.15.1`: `the result contains
  "hi"` with a surplus docstring gave suite status 0, `1 scenarios (1 passed)`.

  This applies to all 40 built-in steps and to comparator-contributed phrases. A built-in
  row's expected argument is derived by reflection from the handler it registers, never
  from a list kept beside it; a contributed phrase declares its own through the `:$`
  pattern convention. Rejection happens at scenario init, before any SUT is driven, and
  statically in `mentat.Validate` and `mentat validate` (a new `step-argument` finding
  class).

  The opposite direction moved earlier too: a step that OMITS a docstring its definition
  requires used to fail when the step ran, with godog's own
  `func expected more arguments than given`. It is now caught at scenario init, naming the
  step and what it expects.

  **A suite that passed before may now fail.** That is the point: the step it fails on was
  reporting a verdict that never read your expectation. The fix is to move the argument to
  the step that takes it, or remove it. `docs/steps.md` shows the argument every built-in
  step accepts, and `mentat validate` names the file, the line and the step.

### Added

- **A comparator can contribute the Gherkin sentences that invoke it.** A feature file can
  read in your domain language —

  ```gherkin
  Then the revenue floor is 4 USD
  ```

  — instead of naming a registry key and handing it a payload. Two new **optional** seams,
  discovered by type assertion: `PhraseContributor` declares the sentences, `CaptureParser`
  turns a sentence's regex captures into the comparator's own `Expectation`. A comparator that
  implements neither is completely unaffected. Phrases are declared with `ContributedPhrase`
  (pattern, group, summary, example) and need no separate registration — the phrase resolves
  *through* the comparator that declared it, so there is nothing to keep in sync.

  `CaptureParser` is the **sibling** of the previous release's `ExpectationParser`, not its
  replacement: one receives a docstring body, the other the captures. Which seam serves a
  phrase is decided once, at engine build, from the shape of the pattern — never guessed at
  runtime from what the comparator happens to implement.

  Both routes produce **identical verdicts**: same pass/fail, same reason text, same
  completeness qualifiers, same expectation value. Contributing a phrase adds an invocation
  route, not a second semantics.

  Every pattern rule is enforced when the engine is built, before any scenario runs, and every
  error names the comparator and the offending value: the pattern must compile, must be
  anchored (a trailing `\$` and top-level alternation both look anchored and are not), must
  not duplicate another contributed pattern or a built-in's, and must carry a non-blank group,
  summary and example.

  Phrases are scoped to the engine that registered them: two suites in one binary never see
  each other's sentences. See `docs/extending/phrases.md`.

- **`mentat.Validate` and `mentat.StepReference`.** `mentat validate` builds no engine, so it
  cannot reach a consumer's `WithComparator` registrations and reports a suite written in
  contributed phrases as unbound — structural, not an omission. Both new entry points accept
  the same `Option`s as `mentat.Run`, so they answer about **exactly** the engine the run will
  use: `Validate` returns the same `Finding` values the CLI prints, and `StepReference` renders
  the step reference for your own engine, built-ins plus your phrases. `StepReference` returns
  an error rather than a partial list if any phrase is malformed.

- **`mentat.Run` now sets godog's `Strict`.** Without it, two patterns matching one step
  resolved silently to the first registered and the scenario **passed** — a green verdict
  nobody wrote. Measured to churn zero goldens on both the hermetic and e2e surfaces.

- **A step matching more than one step definition is now reported statically**, as a new
  `ambiguous-step` finding class naming every pattern that matched. Under `Strict`, godog
  refuses such a step at match time and fails the scenario; `mentat.Validate` used to
  report the same suite CLEAN, so the validator certified a suite the runner would refuse.

  The case is reachable because pattern validation rejects only *identical* patterns: a
  contributed `^the (\w+) reading is fine$` coexists with a contributed or built-in
  `^the alpha reading is fine$`, and both match the same sentence.

  This is **not** regex-overlap analysis, which Mentat does not compute and neither does
  the runner. Both classify per sentence, so two patterns that could collide on a sentence
  no scenario contains are reported by neither. It reaches suites through `mentat.Validate`,
  which sees the engine's contributed phrases; the `mentat validate` binary sees built-in
  patterns only, and no two of those are known to match the same sentence, so the class is
  not expected to fire there.

- **Custom comparators are drivable from a `.feature` file.** A comparator registered
  with `WithComparator` could be composed but not *invoked* by an authored step: every
  `Then` step chose its comparator and its expectation type at compile time. One generic
  step now takes both from the scenario —
  `Then the "revenue-shape" comparator is satisfied by:` followed by a docstring. The
  quoted name resolves through the same registry the built-in steps use, and the
  docstring goes to the comparator's own parser. The step inherits everything a built-in
  step gets: completeness qualifiers, judge-usage accounting, and the `@runs(N>1)` guard.
  Three failure modes are loud and specific — an unregistered name (the error lists the
  names that *are* registered), a comparator that cannot be driven from Gherkin, and a
  parser error (wrapped, never converted into a failing verdict, because a parse failure
  is not an assertion failure).
- **`ExpectationParser` on the facade.** The optional companion to `Comparator`:
  `ParseExpectation(text string) (Expectation, error)`. Implement it and your comparator
  becomes reachable from Gherkin; omit it and nothing changes. It is discovered by type
  assertion, so `Comparator` itself is unchanged and every existing implementation —
  including `examples/kafkaecho` — compiles and behaves identically. It takes text and
  nothing else: no context, no target, no `Evidence`, so it cannot become a second
  channel to run data alongside `Evidence`.
- **`Engine.Comparators()`** lists the registered comparator names, sorted, so an
  unknown-name error can name the alternatives instead of just rejecting the input.

- **Custom reporters — `WithReporter`.** A caller-supplied `Reporter` can now be
  registered for a run and selected by name through `WithReports`, exactly as a built-in
  json/html/junit reporter is. It receives the same `Results` a library caller receives
  from `Run`, so a custom reporter can render everything the built-ins do. Registration
  is scoped to one `Run`: two concurrent runs may register different reporters under the
  same name and each uses its own. A name already taken by a built-in or an earlier
  registration is a loud collision error, never a silent overwrite.
- **`ExtractPolicy`, `HTTPSpec`, `AggregateDetail`, `RunRecord` on the facade.** These
  were frozen on the public surface but had no facade name, so an external module could
  not write them — a driver author could not populate `RunSpec.Extract` or `RunSpec.HTTP`,
  and a comparator author could not attach `Verdict.Detail`.
- **`ExtractWhole`, `ExtractMarker`, `ExtractPattern` on the facade.** The legal values of
  `ExtractPolicy.Mode`. Naming the struct was not enough on its own: `Mode` is a plain
  string, so without these a driver author writes `Mode: "pattern"` as a literal and a
  typo becomes a run-time extraction error rather than a compile error.
- **`Results` carries the full run outcome.** `Total`, `StartedAt` and `Duration` at the
  suite level; `Tags`, `Qualifiers`, `Sequence`, `Runs` and `Aggregate` per scenario. The
  facade's result types were previously lossy against what the built-in reporters saw.
- **Mechanical nameability gate.** `TestFacadeNameabilitySweep` walks the reachable set —
  now including seam method parameter and result types, not only data reachable from
  `Config`/`Results` — and fails on any type the facade cannot name, reporting the type
  *and* the position that reaches it. This closes stability boundary 4, which had been a
  hand-performed check.

- **Public extension API — extend without forking.** A root `github.com/thetonymaster/mentat`
  package re-exports the seam interfaces (`Driver`, `TraceStore`, `Comparator`, `Judge`,
  plus `Correlator`/`Reporter` as types), the `Evidence`/`Verdict`/`Output`/`Config`
  contract types, and the trace-forest types (`Trace`/`Span`) as zero-cost aliases, so a
  third-party module can implement a custom adapter without importing anything under
  `internal/`. Custom seams register at the `mentat.Run` call via `WithDriver`/`WithStore`/
  `WithComparator`/`WithJudge` (a duplicate name is a loud collision naming both
  registrants, never a silent overwrite); `Run(ctx, Config, opts...)` embeds a suite
  in-process and returns structured `Results`/`ScenarioResult`, and `LoadConfig` reads the
  `mentat.yaml` surface. `docs/extending/` documents each seam and the pre-1.0 stability
  policy; `examples/kafkaecho` is a standalone module proving the surface suffices.
  (Registering a custom *comparator* composed but could not yet be invoked from a
  `.feature` step; that grammar landed in feature 011, above.)

### Fixed

- **Comparator-contributed Gherkin phrases were resolved per call instead of per engine
  build.** The engine re-invoked every contributing comparator's `ContributedPhrases()`
  seam each time a surface asked for the vocabulary. A comparator that does not declare
  its phrases from immutable state — one that memoizes lazily, flips a flag between
  calls, or builds its list from configuration it also writes to — could therefore be
  documented on one vocabulary, validated on a second and executed on a third, with
  nothing in the system noticing. The engine now captures one snapshot during
  `engine.Build`, after the seam registry is sealed, and answers every later request
  from it; each answer is a copy, so neither the contributing comparator nor a caller
  can reach the engine's own set.

  **No public API changed**, and nothing changes for a comparator that already declares
  its phrases from immutable state — which is every comparator in this repository, in
  `examples/`, and in the documented seam. The fix aligns the code to a contract three
  artifacts already published: `mentat.PhraseContributor` ("resolved once per engine
  build"), `core.PhraseContributor` ("consulted ONCE PER ENGINE BUILD … a comparator
  that mutates it afterwards has no effect on the built engine") and feature 012's
  phrase-seam contract. None of the three needed correcting — the code was the odd one
  out.

  The defect was **latent rather than live** through the public API: `Run`, `Validate`
  and `StepReference` each build their own engine and each consulted the seam exactly
  once, so no shipped path observed the drift. It is fixed because the published
  contract says once per build, and because the first surface to share an engine — or
  the first in-process consumer holding one — would have made it live.

- **`the response body json-contains:` panicked on a missing docstring.** It
  dereferenced the docstring directly, so a malformed step crashed the run where its
  eight sibling docstring handlers return a descriptive error naming the step. All nine
  handlers now guard it, and all nine have a test for that guard — two of them had the
  guard but no test, which is the same failure in review as having no guard at all.

### Changed

- **BREAKING — `Reporter.Report` now takes `Results`, not `RunReport`.** `RunReport` no
  longer exists; the run-result types moved to an internal package and are aliased on the
  facade. Migration:
  - An implementation of `Reporter` changes its parameter type to `mentat.Results`. The
    field names it reads are unchanged — `Results` *is* the former report type, with the
    facade's separate (and lossy) struct collapsed into it.
  - `mentat.Results` and `mentat.ScenarioResult` gained fields **and changed field
    order**. Keyed composite literals — the normal form — are unaffected. An *unkeyed*
    literal will either fail to compile or, worse, still compile and mean something
    different; search for unkeyed literals of both types before upgrading.
  - `ScenarioResult.RunIDs` is unchanged for readers, but is now derived from the new
    `Runs` field and excluded from report serialization, so emitted report files are
    byte-identical to before.
  - Reporters are registered per-`Run` on the engine registry rather than in a package
    global. `registry.RegisterReporter` as a package-level function is gone.
- **The `mentat` CLI is now consumer zero of the public API.** `mentat run` composes
  entirely through `mentat.Run` (flags → `Config` + `With*` options → `Run` →
  `Results.ExitCode`) — one composition path, no forked internal wiring. The green happy
  path is byte-identical (SC-004). One consequence: the judge ledger is now priced
  unconditionally (previously only when a report flag was set), so a run configured with
  an unknown/ambiguous judge-model price now fails loudly even without `--junit`/
  `--report-json`/`--report-html`, rather than silently skipping pricing.
- **`mentat steps` — generated step reference.** Prints every registered Gherkin
  step (pattern, summary, example) grouped by concern, plus the shared
  selector/quantifier/ordinal grammar and CEL variables. `--format md` regenerates
  `docs/steps.md` (via `go generate ./...`); a drift test keeps that file
  byte-identical to the registered steps.
- **`mentat validate [paths...]` — static authoring checks.** Runs step-binding,
  CEL, shape-pattern, target, and expectations prechecks over the feature corpus
  without driving a SUT or contacting a store/judge (no network by construction).
  Reports **all** findings and exits 1 on any (or when no feature files are found).
  `--format json` emits `{"findings":[{"file","line","class","message"}]}`.
- **File store — offline replay.** `store: file` + `storePath: <dir>` replays saved
  run fixtures from a directory with no live Tempo and no network. Fixtures are keyed
  by their recorded `runScenario` (the saved run id), so replay runs on the pinned
  path (`mentatctl agent replay <id>`); a `@runs(N>1)` scenario is a hard error and a
  missing id fails loud (naming dir + id).
- **Judge cost ledger and budget (US6).** Judge token usage is captured per call and
  aggregated into a per-scenario `judge{calls,inputTokens,outputTokens,costUsd,model}`
  object and a suite `judgeTotal` in the JSON and HTML reports (present only when judge
  calls occurred — no fabricated zeros). `judge.max_cost_usd` sets an optional
  post-scenario spend ceiling that aborts the suite (naming spent/budget/scenario) once
  crossed; reports still emit.
- **HTTP request bodies.** Two new steps set the request body verbatim:
  `I send the request with body:` (doc-string) and
  `I send the request with body fixture "<path>"` (relative to the feature dir; absolute
  ok) — a missing fixture fails naming the resolved path.
- **Configurable answer extraction (US8).** `targets.<name>.extract: {mode, marker, pattern}`
  where `mode` is `whole` (default — trimmed stdout), `marker` (text after the last
  occurrence of `marker`), or `pattern` (first capture group of `pattern`). A missing
  marker/pattern match fails loud naming it; config validation requires the field per
  mode and a pattern with ≥1 capture group. Marker/pattern extraction requires the
  shell adapter (it reads stdout); a non-shell target (e.g. `http`) carrying such a
  policy is rejected at config load, never silently ignored.
- **`mentatctl agent run` enrichment (US7).** The summary gains additive lines
  `tokens: in <n> out <n>`, `cost: $<x.xxxx>`, `latency: <ms> ms`, and
  `traces: <root trace ids>` (existing lines byte-stable). New flags: `--prompt-file`
  (`-` = stdin; mutually exclusive with `--prompt`), `-o <file>` (write the answer
  only), and `--timeout <dur>` (bound the invocation).

### Changed

- **`--junit` composes with the console instead of replacing it.** Requesting JUnit
  (like `--report-json` / `--report-html`) now emits the file *and* keeps the godog
  `pretty` console output in the same run — the console is never silenced by asking for
  a machine report. JUnit is written from the collector (so it carries the interrupted
  marker), and a JUnit write failure still fails the run.
- **Default judge model is now the fast tier.** An omitted/empty `judge.model` resolves
  to `claude-haiku-4-5` (`config.DefaultJudgeModel`), ~80% cheaper per token than the
  former `claude-opus-4-8` default (Opus 4.8 $5/$25 vs Haiku 4.5 $1/$5 per MTok). Set
  `judge.model` to upgrade accuracy. Loading now rejects `votes > 1` with
  `temperature: 0` (near-identical calls; raise the temperature or set `votes: 1`).
- **Strict config load (breaking).** Unknown or misspelled keys anywhere in
  `mentat.yaml` now fail to load instead of being silently ignored. A dropped typo
  (e.g. `poll.timout` for `poll.timeout`) previously fell back to a default and could
  quietly change verdict semantics; loading is now strict and the error names the
  offending key (`field timout not found in type config.PollSpec`). Absent optional
  keys are unaffected — absence still applies documented defaults.
- **Phantom adapters rejected at startup (breaking).** A target whose `adapter` names
  no registered driver (e.g. `mcp`, `grpc`) now fails at engine build (`engine.Build`)
  listing the registered adapters, instead of loading and then failing mid-suite when
  that target first runs
  (`engine: target "svc": adapter "mcp" has no registered driver (registered: http, shell)`).
- **Fixture/trace strictness (breaking).** Span `status` and `kind` spellings are
  now normalized through a canonical vocabulary at store-decode time. Unknown
  spellings that previously loaded silently now fail loudly with a decode error
  naming the span and the offending value
  (`filestore: span 3 ("checkout"): trace: unknown span status "FOO"`).
- OTLP wire spellings keep working: `STATUS_CODE_UNSET`/`STATUS_CODE_OK`/
  `STATUS_CODE_ERROR` and `SPAN_KIND_INTERNAL`/`SPAN_KIND_SERVER`/
  `SPAN_KIND_CLIENT`/`SPAN_KIND_PRODUCER`/`SPAN_KIND_CONSUMER` normalize to the
  canonical set; omitted `status` → `Unset`, omitted `kind` → unspecified.
- **Fixture `parentIndex` is now required and validated for a forest (breaking).**
  Every span must set `parentIndex` (`-1` = root); an omitted value used to decode
  to `0` and silently attach the span to span 0 — it now fails loudly
  (`filestore: span 2 ("payment"): parentIndex is required (use -1 for root)`).
  Parentage is also walked for reachability, so cyclic/rootless fixtures
  (e.g. `0 → 1 → 0`) are rejected instead of loading as a non-forest.

### Fixed

- Error assertions (`no span has status "ERROR"`, `MaxErrors`, CEL `errors`,
  `span.status=Error` selectors) now count spans that arrive with the live-Tempo
  wire spelling `STATUS_CODE_ERROR`, not only the in-repo fixture spelling — they
  were permanently green on live traces before. Closes audit finding A1
  (`docs/audits/2026-07-01-codebase-audit.md`) and the `002-verdict-integrity`
  spec (`specs/002-verdict-integrity/`).
