# Phase 1 Data Model: DX & Product Completeness

References research.md R1–R9.

## Step metadata (internal/steps)

| Field | Type | Rules |
|-------|------|-------|
| `pattern` | string | The registered regex (single source with registration). |
| `summary` | string | One-line purpose. Required — drift test fails on empty. |
| `argGrammar` | string | Selector/quantifier/ordinal notes where applicable. |
| `example` | string | One valid Gherkin line. Required. |

Rendered identically by `mentat steps` and generated `docs/steps.md`
(regeneration-clean check in CI).

## Validation finding (cmd/mentat validate)

| Field | Type | Notes |
|-------|------|-------|
| `file`, `line` | string, int | Feature file position (or config path). |
| `class` | enum | `unbound-step` \| `cel` \| `shape-pattern` \| `expectations` \| `target` \| `config` |
| `message` | string | The precheck's own error text. |

Output: human table (default) or JSON array (`--format json`). Exit 1 if any.
Zero feature files found → finding of class `config`, exit 1.

## Judge ledger (internal/core, judge, comparator, report)

| Field | Type | Rules |
|-------|------|-------|
| `JudgeUsage.Calls` | int | Incremented per API call (votes count individually). |
| `JudgeUsage.InputTokens/OutputTokens` | int | From SDK usage metadata. |
| `JudgeUsage.CostUSD` | float64 | Via pricing table; unknown model → hard error (existing rule). |
| `JudgeUsage.Model` | string | Model id used. |

Flow: judge call → semantic matcher aggregates votes → Verdict detail →
collector per-scenario + suite total → JSON `judge{}` objects + HTML section.
Budget: `judge.max_cost_usd` (config, optional); checked post-scenario;
exceeded → suite abort error naming spent/budget/scenario.

## Config additions (internal/config)

| Key | Type / Default | Rules |
|-----|----------------|-------|
| `judge.max_cost_usd` | float, unset = no budget | > 0 when set. |
| `judge.model` | default → fast tier (Haiku-class id pinned at impl) | capability allowlist unchanged. |
| `judge.votes>1` with `temperature: 0` | **load error** | names both remedies. |
| `targets.<n>.extract` | `{mode: whole\|marker\|pattern, marker, pattern}`; default whole | marker required iff mode=marker; pattern must compile and contain ≥1 capture group iff mode=pattern. |
| `store` | gains `file` | with `storePath` dir; required iff store=file. |

## Answer extraction (internal/core)

| Mode | Semantics | Failure |
|------|-----------|---------|
| `whole` | trimmed full stdout (today's behaviour) | never fails |
| `marker` | text after **last** occurrence of marker, trimmed | marker absent → run failure naming marker |
| `pattern` | first capture group of first match | no match → run failure naming pattern |

## File store (internal/store)

| Operation | Behaviour |
|-----------|-----------|
| `Query(tag, value)` | scan `storePath` fixtures for recorded run id; absent → not-found error naming dir + id |
| `GetByID` | load fixture by trace id; canonical status/kind vocabulary (feature 002) |
| multi-run (`@runs(N>1)`) | hard error — one recorded sample per run id |

## mentatctl summary (internal/ctl)

Additive lines after the existing output: `tokens: in/out`, `cost: $x.xxxx`,
`latency: <envelope ms>`, `traces: <root ids>`. New flags: `--prompt-file`
(`-` = stdin), `-o <file>` (answer only), `--timeout <dur>`.

## Build system (Makefile, tracelab)

`make labs`: builds `bin/{researchbot,orderflow,capture...}` with Go-source
prerequisites; `harness-up` depends on it; configs reference binaries.
