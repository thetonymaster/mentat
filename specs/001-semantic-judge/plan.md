# Implementation Plan: Semantic (LLM-Judge) Result Matcher

**Branch**: `001-semantic-judge` | **Date**: 2026-06-29 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/001-semantic-judge/spec.md`

## Summary

Add the `semantic` result matcher — the framework's only non-deterministic matcher —
which asks a pluggable **`Judge`** seam whether a run's result *means* what the author
expected. The default Judge backend calls Claude (Anthropic API) via the official Go SDK.
The matcher rides the existing matcher seam (zero change to `result.go`); the Judge is a
new `core` seam registered by a factory in a judge registry and wired once at
`engine.Build`. A `the result means "..."` Gherkin step authors it. Failures (transport,
auth, malformed verdict, vote tie) are hard, descriptive errors — never a guessed verdict.
Hermetic tests substitute a gomock `Judge`; live Claude is `e2e`-gated. A configurable
best-of-N majority vote (default N=1) is built in.

This plan commits **User Stories 1–3** (single-run semantic matching, trust-on-failure,
pluggable+hermetic backend) plus the vote mechanism. **US4** (statistical semantic over
`@runs(N)`) is **deferred pending a Q decision** — see Complexity Tracking.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: existing — `cucumber/godog`, `google/cel-go`, OTel/Tempo,
`go.uber.org/mock`, `yaml.v3`. **New (direct)**: `github.com/anthropics/anthropic-sdk-go`
(official Go SDK; powers the Claude judge backend). Added via go-coder (go.mod/go.sum +
`go mod tidy`).

**Storage**: N/A. The Judge is stateless per call; the matcher reads only
`Evidence.Output`. No persistence, no cache (verdict caching is out of scope, §Out of Scope).

**Testing**: `go test`, table-driven; **uber gomock** for the new `core.Judge` interface
(regenerated into `internal/core/mocks/`); `godog` for the `the result means` step and the
L3 meta-test; live-Claude judge test gated behind `//go:build e2e`.

**Target Platform**: Linux/macOS; Mentat CLI + library. No new platform surface.

**Project Type**: Single Go module (library + `cmd/mentat`, `cmd/mentatctl`).

**Performance Goals**: Latency is dominated by the LLM call; one call per assertion at the
default vote N=1. No throughput target. Cost note: a semantic assertion costs N judge
calls; under a (future) `@runs(M)` it would be M×N. Default model `claude-opus-4-8`
($5/$25 per MTok); `claude-haiku-4-5` ($1/$5) is the cost-optimized configurable option.

**Constraints**:
- **No silent fallbacks** (Constitution IV): every judge failure is a hard `%w`-wrapped error.
- **Evidence-only matcher** (Constitution I): the matcher extracts the candidate string
  from `Evidence.Output` and the expected meaning from the expectation, then passes *two
  strings* to the Judge. The Judge receives no `Evidence`, `TraceStore`, or `Driver`.
- **Hermetic by default** (Constitution V): unit/CI tests use a gomock Judge, no network.
- **`temperature` is model-dependent** (research finding): Opus 4.8/4.7/Fable 5 **reject**
  `temperature` (HTTP 400). FR-006's "temperature 0" therefore applies only when the user
  configures Sonnet 4.6 / Haiku 4.5; on the Opus-tier default, determinism leans on
  structured output + prompt (+ the vote). See research.md → Decision 4.

**Scale/Scope**: Small, additive. ~7 source files: `core` (interface+types), `registry`
(judge factory), new `internal/judge` package (claude backend + registration), new
`internal/comparator/semantic.go`, `config` (judge block), `steps` (one step), plus tests
and one `features/meta` file. No existing signature changes.

## Constitution Check

*GATE: re-checked after Phase 1 design — PASS (no violations).*

| Principle | Verdict | How this plan satisfies it |
|---|---|---|
| **I. Evidence-Only Comparators** | ✅ PASS | `semantic` reads only `Evidence.Output`; the Judge is the comparison *strategy*, fed plain strings — it never touches a store/driver and never receives `Evidence`. |
| **II. Trace forest / tag-first** | ✅ N/A | Semantic grades the boundary result, not trace structure; nothing about correlation changes. |
| **III. Seams are interfaces, wired once (manual DI)** | ✅ PASS | `core.Judge` is a new interface (Constitution III already names `Judge` a seam); a **factory-based** judge registry mirrors `StoreFactory`; wired once at `engine.Build`. `semantic` rides the existing matcher seam. No `wire`/`fx`. |
| **IV. No silent fallbacks** | ✅ PASS | FR-007/FR-013/FR-015: transport/auth/malformed/empty-expr/vote-tie/unknown-backend all hard-error with the concrete cause. |
| **V. Test-First & Hermetic (NON-NEGOTIABLE)** | ✅ PASS | go-test-writer owns red→green; gomock `Judge`; hermetic unit tests; live Claude `e2e`-gated; **L3 meta-test mandatory** (FR-011); ≥80% coverage on new packages. |

Engineering standards: new dep via go-coder + `go generate ./...` to add the `Judge` mock;
`gofmt`/`go vet` clean; Conventional Commits; no AI attribution.

## Project Structure

### Documentation (this feature)

```text
specs/001-semantic-judge/
├── plan.md              # This file
├── research.md          # Phase 0 — decisions (SDK, model, structured output, temp reality, US4)
├── data-model.md        # Phase 1 — Judge, JudgeRequest/Verdict, JudgeConfig, semantic matcher, registry
├── contracts/
│   ├── judge-seam.md     # core.Judge interface + judge registry contract
│   ├── gherkin-step.md   # `the result means "..."` grammar + behavior
│   └── config-judge.md   # mentat.yaml `judge:` block schema + defaults/validation
├── quickstart.md        # Phase 1 — how to validate (hermetic, e2e, L3)
└── checklists/requirements.md
```

### Source Code (repository root)

```text
internal/
├── core/
│   ├── core.go              # + Judge interface, JudgeRequest, JudgeVerdict (//go:generate already present)
│   └── mocks/mock_core.go   # regenerated to include MockJudge
├── registry/
│   └── registry.go          # + JudgeFactory, RegisterJudge, Judge(name) (factory-based, like StoreFactory)
├── judge/                   # NEW package — the backend(s)
│   ├── claude.go            # NewClaude(cfg) core.Judge — anthropic-sdk-go, structured output
│   ├── claude_test.go       # hermetic mapping tests (no live call); + claude_e2e_test.go (//go:build e2e)
│   ├── judge.go             # RegisterBuiltins() registers "claude"; backend-name resolution
│   └── judge_test.go
├── comparator/
│   ├── semantic.go          # NEW — NewSemantic(j core.Judge, votes int) core.Matcher (vote + tie policy)
│   └── semantic_test.go     # table-driven, gomock Judge (verdict, failure, malformed, vote, tie)
├── config/
│   ├── config.go            # + Judge JudgeConfig (backend default "claude", votes default 1) + validation
│   └── config_test.go
├── engine/
│   └── build.go             # resolve judge factory → construct judge → RegisterMatcher("semantic", ...)
└── steps/
    ├── steps.go             # + `the result means "..."` (inline + docstring) → ResultExpectation{Matcher:"semantic"}
    └── steps_test.go

features/meta/
└── bad_meaning.feature      # L3 — answer does NOT mean expected → Mentat goes RED (+ green companion)

e2e/                         # wire the bad_meaning meta-scenario with a deterministic fake judge
```

**Structure Decision**: Single Go module; additive. The Judge backend gets its **own
package** (`internal/judge`) so the `comparator` package keeps importing only
`core`/`registry` and never the Anthropic SDK — the SDK dependency is isolated to
`internal/judge`, preserving the Evidence-only/transport-free comparator boundary.

## Complexity Tracking

No constitution violations. This section records two **scope decisions surfaced during
planning** that need Q's awareness (the first needs a decision).

| Item | Why it arises | Options / recommendation |
|---|---|---|
| **US4 deferral** (FR-012 / SC-007 — statistical semantic over `@runs(N)`) | The `@runs(N)` aggregate path is **CEL-only** (`the runs satisfy "<cel>"`) and has **no judge hook**; per-run records expose `r.answer`/`r.tools` but no semantic verdict. `the result means` under `@runs(N>1)` hard-errors (existing single-run guard). So US4 does **not** compose for free. | **(A, recommended)** Defer US4 to a fast-follow plan; ship US1–3 (single-run semantic) now. **(B)** Expand this plan to add a CEL `means(candidate, expected)` function to the aggregate env — but that puts LLM network I/O inside CEL evaluation (N calls per aggregate, error/cancellation semantics, per-run memoization, failed-run interaction) — materially more scope for a P3 story. → **Needs Q decision.** |
| **FR-006 "temperature 0" is model-dependent** | Opus 4.8/4.7/Fable 5 reject `temperature` (400). The default model is Opus-tier. | Refine FR-006: structured output is always on; `temperature: 0` is sent only for models that accept it (Sonnet 4.6 / Haiku 4.5). On Opus-tier, determinism leans on structured output + prompt + the vote. No user-facing behavior change; documented in research.md. **(No decision needed — flagging.)** |
