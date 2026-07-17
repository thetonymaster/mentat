# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0/).

## [Unreleased]

### Added

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
