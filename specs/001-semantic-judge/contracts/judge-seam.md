# Contract — `core.Judge` seam & judge registry

## Interface (in `internal/core/core.go`, beside the other seams)

```go
type Judge interface {
    Judge(ctx context.Context, req JudgeRequest) (JudgeVerdict, error)
}
type JudgeRequest struct { Candidate, Expected string }
type JudgeVerdict struct { Match bool; Reason string }
```

### Behavioral contract
- **Inputs**: two plain strings. The implementation MUST NOT accept or reach for `Evidence`,
  a `TraceStore`, or a `Driver` (Constitution I — keeps the seam transport-free).
- **Success**: returns a `JudgeVerdict` whose `Match` reflects whether `Candidate` means
  `Expected`, with a non-empty `Reason` when `Match == false`.
- **Failure** (Constitution IV): returns `(JudgeVerdict{}, error)` — never a guessed verdict.
  A backend MUST error on: missing/invalid credentials, transport failure, rate limit
  (after SDK retries), 5xx, context cancellation/timeout, a model refusal, or a response that
  does not parse into the verdict schema. Errors are `%w`-wrapped and name the cause.
- **Determinism**: implementations SHOULD request structured output and disable thinking;
  `temperature: 0` is sent only on models that accept it (not Opus-tier — research Decision 4).

## Registry (in `internal/registry/registry.go`)

```go
type JudgeFactory func(cfg config.Config) (core.Judge, error)
func RegisterJudge(name string, f JudgeFactory)
func Judge(name string) (JudgeFactory, bool)
```

- **Factory-based** (Judge is stateful), mirroring `StoreFactory`. The two-pattern rationale
  comment in `registry.go` covers this (stateless seam→instance, stateful seam→factory).

## Composition-root wiring (in `internal/engine/build.go`)

```go
judge.RegisterBuiltins()                              // registers "claude"
jf, ok := registry.Judge(cfg.Judge.Backend)           // default "claude"
if !ok { return nil, fmt.Errorf("unknown judge backend %q", cfg.Judge.Backend) }
j, err := jf(cfg)
if err != nil { return nil, fmt.Errorf("build judge %q: %w", cfg.Judge.Backend, err) }
registry.RegisterMatcher("semantic", comparator.NewSemantic(j, cfg.Judge.Votes))
```

- An **unknown backend name** → descriptive error at `Build` (FR-005, US3-AC2).
- The `claude` factory MUST NOT require the API key at construction so `Build` succeeds in
  environments without a key when no semantic scenario runs; the key is checked at first
  `Judge()` call (US2-AC3). *(Implementation choice — alternative: check at Build. The
  contract requires only that a missing key surfaces as a hard error before any model call.)*

## Test seam
- `go generate ./...` produces `mocks.MockJudge`. Unit tests use it via
  `comparator.NewSemantic(mockJudge, votes)` — no registry, no network.
- The L3 godog suite registers a deterministic fake `core.Judge` as `semantic`.
