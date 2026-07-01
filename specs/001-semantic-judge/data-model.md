# Phase 1 Data Model — Semantic (LLM-Judge) Result Matcher

Entities and types introduced or touched. All new exported types are **additive** — no
existing signature changes.

## New types in `internal/core` (the seam + its values)

```go
// Judge renders a single semantic verdict over two strings. It is a seam: the
// default backend calls Claude, but it is swappable and gomock-able. It receives
// NO Evidence / TraceStore / Driver — only the candidate and the expected meaning.
type Judge interface {
    Judge(ctx context.Context, req JudgeRequest) (JudgeVerdict, error)
}

// JudgeRequest is the matter to be judged. Plain strings keep the Judge transport-
// and Evidence-free (Constitution I): the matcher extracts Candidate from
// Evidence.Output and Expected from the expectation.
type JudgeRequest struct {
    Candidate string // the run's result content (Evidence.Output.Answer)
    Expected  string // the author's expected meaning (from `the result means "..."`)
}

// JudgeVerdict is the structured answer — exactly match + reason (no confidence in v1).
type JudgeVerdict struct {
    Match  bool
    Reason string // human-readable rationale; flows into Verdict.Reasons on a fail (FR-008)
}
```

- `//go:generate mockgen -source=core.go ...` already lives in `core.go`, so
  `go generate ./...` produces `MockJudge` in `internal/core/mocks` automatically.

**Relationships**: `Matcher` (existing) → uses → `Judge` (new). `Verdict` (existing) is the
matcher's output; a failed semantic verdict sets `Pass:false` and `Reasons:[<judge reason>]`.

## New types in `internal/config`

```go
type Config struct {
    // ...existing fields...
    Judge JudgeConfig `yaml:"judge"`
}

type JudgeConfig struct {
    Backend string `yaml:"backend"` // default "claude"
    Model   string `yaml:"model"`   // default "claude-opus-4-8"
    Votes   int    `yaml:"votes"`   // default 1; best-of-N majority
    // Temperature is applied only on models that accept it (Sonnet 4.6 / Haiku 4.5);
    // omitted on Opus-tier (research Decision 4). Optional knob, default 0.
    Temperature float64 `yaml:"temperature"`
}
```

**Validation (in `config.Load`, no silent fallbacks)**:
- `Backend == ""` → default `"claude"`.
- `Model == ""` → default `"claude-opus-4-8"`.
- `Votes == 0` → default `1`; `Votes < 0` → error (`"judge.votes must be >= 1, got %d"`).
- An **even `Votes > 1`** → error at config load (a descriptive error naming the value):
  majority is undefined on a tie, so the stricter, no-silent-fallback option is to fail fast
  at load rather than only at runtime. The matcher also hard-errors on an actual runtime tie
  (FR-015) as defence in depth. See `contracts/config-judge.md`.

## Registry additions (`internal/registry`)

```go
type JudgeFactory func(cfg config.Config) (core.Judge, error) // stateful → factory (like StoreFactory)
func RegisterJudge(name string, f JudgeFactory)
func Judge(name string) (JudgeFactory, bool)
```

## New domain types in `internal/judge`

- `claudeJudge` (unexported) — holds the `anthropic.Client`, model, temperature, and the
  structured-output schema; implements `core.Judge`. Constructed by `NewClaude(cfg) (core.Judge, error)`.
- `RegisterBuiltins()` — registers the `"claude"` factory into the judge registry.

## New matcher (`internal/comparator`)

- `semanticMatcher` (unexported) — holds a `core.Judge` and `votes int`; `Name() == "semantic"`;
  `Match(ctx, ev, want, _ target)` extracts `ev.Output.Answer` as Candidate, `want` as Expected,
  runs N votes, returns the majority `Verdict` (or a tie error). Constructed by
  `NewSemantic(j core.Judge, votes int) core.Matcher`.

## State transitions

A semantic assertion has no persistent state. Per assertion:

```
Match() ──▶ vote loop (1..N) ──▶ each: Judge.Judge() ──▶ JudgeVerdict | error
                                   │
        any judge error ───────────┴──▶ hard error (FR-007), no Verdict
        majority match=true  ─────────▶ Verdict{Pass:true}
        majority match=false ────────▶ Verdict{Pass:false, Reasons:[reason]}
        tie (even N) ────────────────▶ hard error (FR-015), no Verdict
```
