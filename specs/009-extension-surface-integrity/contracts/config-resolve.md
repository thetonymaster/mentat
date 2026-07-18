# Contract: `config.Resolve` — path-independent effective contract

**Consumers**: `config.Load` (YAML path), `mentat.Run` (library path, before
`BuildCorrelator`/`BuildStore`/`Build` — `run.go:293/310/349`), CLI paths that
Load-then-Run (`cmd/mentat/main.go:74`, `cmd/mentatctl/main.go:310`).
**Fulfils**: FR-008..FR-010, SC-003. Decision: [research.md R2](../research.md)
(story (a) — chosen over (b) with rationale recorded there).

## Shape

```go
// package config
func Resolve(c *Config) error
```

- `Load` ≡ read file + strict decode + `Resolve` (behaviour of the YAML path is
  byte-identical to today — refactor, not change).
- `mentat.Run` calls `Resolve` on its Config before any composition call; a
  `Resolve` error aborts the run before the SUT is driven, wrapped per repo
  convention (`fmt.Errorf("resolving config: %w", err)`).

## Laws

1. **Idempotency**: for any `c` where `Resolve(c) == nil`, a second `Resolve`
   leaves `c` unchanged and returns nil. (The CLI path re-enters `Resolve` via
   `mentat.Run` with an already-resolved config.)
2. **Explicit-value-wins**: a default applies only to a zero-valued field. A
   non-zero value set in code is never overwritten.
3. **Raw/resolved twins**: where a raw string field exists beside its resolved
   twin (`Completeness.SettleRaw` / `Settle`), non-empty raw is parsed and wins;
   empty raw + non-zero resolved keeps the resolved value (code-path idiom);
   empty raw + zero resolved gets the kind default. A raw value that parses to a
   conflicting non-zero resolved value set simultaneously is a hard error naming
   both fields (ambiguity is never guessed — Constitution IV).
4. **Same errors both paths**: every hard error Load raises today is raised by
   `Resolve`, so the code path inherits them verbatim.

## Behaviour inventory (complete — from `config.go:198-298` at `2f4073d`)

| # | Behaviour | Today (Load-only) | Kind |
|---|-----------|-------------------|------|
| 1 | `Store` default `"tempo"` | :212-214 | default |
| 2 | file-store requires storePath | :218-220 | hard error |
| 3 | `Expectations` default | :221-223 | default |
| 4 | `Poll.SearchLimit` default 100 | :226-228 | default |
| 5 | kill-grace + suite timeout → `Budget` | :233-241 | default |
| 6 | per-target concurrency defaults | :243-252 | default |
| 7 | http url/method required + trimmed | :253-264 | error + normalize |
| 8 | `Target.Budget` resolution | :265-269 | default |
| 9 | extract validate + regex compile (`compiled`) | :270-274 | error + compile |
| 10 | completeness kind-defaults (shell 2s / http 5s settle) | :275-279, :380-405 | default |
| 11 | `validatePricing` | :282 | hard error |
| 12 | judge backend/model/votes defaults | :285-293 | default |
| 13 | `validateJudge` (temperature pairing, `MaxCostUSD`) | :294, :312-324 | hard error |

All 13 move into (or are called from) `Resolve`. The engine's existing
re-validation (`build.go:83-104` judge votes, :141-144 `MaxConcurrency`) stays as
defense-in-depth — `Build` remains safe to call directly.

## Divergence-suspect audit (must be settled by test, not assumption)

- Zero `Target.Budget` semantics in `Drive` on the code path (suspected
  additional divergence beyond the prompt's three).
- `validateJudge`'s Load-only rules (#13) vs `Build`'s partial re-check.

Each gets a row in the parity table; the test outcome is the evidence.

## Proof obligation

Table-driven parity test (FR-009): for each row, the same logical configuration
expressed as (i) a YAML fixture through `config.Load` and (ii) a struct literal
through `config.Resolve`, asserting deep-equal effective contracts — including
resolved completeness (mode, settle), budget, judge, and compiled-extract
observable behaviour (`Policy()` non-nil) — or identical descriptive errors.
Rows MUST cover: every default (#1,3,4,5,6,8,10,12), every hard error
(#2,7,11,13), the explicit-value-wins and raw/resolved-twin laws, idempotency
(double-Resolve), and both divergence suspects.
