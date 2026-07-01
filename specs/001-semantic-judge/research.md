# Phase 0 Research — Semantic (LLM-Judge) Result Matcher

All "NEEDS CLARIFICATION" from the spec were resolved in `/speckit-clarify` (vote, egress,
verdict shape). The decisions below resolve the **implementation unknowns** for the Judge
backend and the seam wiring, grounded in the existing code and the `claude-api` skill.

---

## Decision 1 — Judge backend SDK

**Decision**: Use the **official Go SDK** `github.com/anthropics/anthropic-sdk-go`
(`anthropic.NewClient()`, key from `ANTHROPIC_API_KEY`; `option.WithAPIKey(...)` override).

**Rationale**: The `claude-api` skill mandates the official SDK when one exists for the
language (Go does). It gives typed errors, automatic 429/5xx retry (max_retries=2), and the
structured-output surface — robustness we'd otherwise hand-roll. The dependency is isolated
to `internal/judge`, so the comparator layer stays SDK-free.

**Alternatives considered**: Hand-rolled `net/http` client to `/v1/messages` — rejected
(re-implements retry/typed-errors/structured-output; skill discourages raw HTTP when an SDK
exists). It remains a fallback if the SDK proves too heavy, behind the same `core.Judge`.

---

## Decision 2 — Default model

**Decision**: Default `model: claude-opus-4-8`; fully configurable via the `judge:` block.
Document `claude-haiku-4-5` (cheapest, $1/$5 per MTok) and `claude-sonnet-4-6` ($3/$15) as
cost-optimized options.

**Rationale**: The skill is explicit — default to `claude-opus-4-8` and never downgrade for
cost on the user's behalf; cost is the user's decision (FR-004 makes the model
configurable). A semantic match verdict is a small task where Haiku is attractive, so we
surface it as a documented config option rather than choosing it for Q.

**Alternatives**: Defaulting to Haiku for cost — rejected per the skill's no-downgrade rule.

---

## Decision 3 — Structured verdict output

**Decision**: Request a structured JSON verdict via **`output_config.format`** (JSON-schema
structured outputs) with schema `{ match: boolean, reason: string }`,
`additionalProperties:false`, both required. Supported on the default and all configurable
models (Opus 4.8, Sonnet 4.6, Haiku 4.5). Parse into `core.JudgeVerdict`.

**Rationale**: One-shot verdict, no agentic loop — `output_config.format` is the cleanest
fit and guarantees a parseable shape, directly serving FR-008 (reason) and the locked
verdict shape (match+reason, no confidence). The Go binding
(`OutputConfig`/`JSONOutputFormatParam`) is verified against the SDK at implementation per
the skill's write-then-compile-fix guidance.

**Alternatives**: **Strict tool use** (`record_verdict` tool, `strict:true`,
`tool_choice:{type:"tool"}`) — equivalent and well-documented for Go; kept as the fallback
if `output_config.format` Go ergonomics disappoint. **Free-text + parse** — rejected (no
guarantee; violates no-silent-fallback when parsing fails).

---

## Decision 4 — Determinism settings (refines FR-006)

**Decision**: Always send structured output and disable thinking
(`thinking:{type:"disabled"}`, accepted on Opus 4.8). Send `temperature: 0` **only** when
the configured model accepts it (Sonnet 4.6 / Haiku 4.5). On Opus 4.8/4.7/Fable 5, **omit
`temperature`** — it returns HTTP 400.

**Rationale**: The `claude-api` skill is unambiguous: `temperature`/`top_p`/`top_k` are
**removed on Opus 4.8/4.7/Fable 5** and 400. FR-006's literal "temperature 0" is therefore
model-dependent. With structured output the response is the JSON verdict (no rambling), so
thinking-disabled is safe and lowers latency. Determinism on Opus-tier leans on structured
output + a tight prompt + (optionally) the vote. **Interplay to document**: with Opus-tier
(no temperature knob) the model is inherently non-deterministic, so a vote N>1 genuinely
varies (useful); with Sonnet/Haiku at temperature 0 a vote N>1 returns near-identical
verdicts (little benefit). The default N=1 sidesteps this.

**Flag to Q**: FR-006 wording ("temperature 0") should be read as "deterministic-leaning;
temperature 0 where the model supports it." No user-facing behavior change.

---

## Decision 5 — Vote (best-of-N majority) placement

**Decision**: Implement the vote **in the `semantic` matcher**, not the Claude backend.
`core.Judge.Judge()` renders **one** verdict; the matcher calls it N times and takes the
strict majority. A tie (possible only at even N) is a hard, descriptive error (FR-015).

**Rationale**: Keeps `core.Judge` a minimal single-shot contract, so the vote is
backend-agnostic (any future judge benefits) and unit-testable with a gomock Judge whose
verdicts vary across N calls. Vote count comes from `cfg.Judge.Votes` (default 1), passed to
`NewSemantic(judge, votes)` at the composition root.

**Alternatives**: Vote inside the Claude backend — rejected (couples voting to one backend;
harder to test; bloats the Judge interface).

---

## Decision 6 — Judge seam shape & registry pattern

**Decision**: `core.Judge` is a single-method interface taking plain strings (not
`Evidence`). The judge registry is **factory-based** (`JudgeFactory func(config.Config)
(core.Judge, error)`), mirroring `StoreFactory`, because a Judge is **stateful**
(HTTP client, model, key). The matcher seam stays **instance-based** — the `semantic`
instance is constructed with its Judge at `engine.Build` and registered like the other
matchers (just not inside the dependency-free `RegisterBuiltinMatchers()`).

**Rationale**: Resolves the "stateless shared-instance matcher vs stateful Judge" tension
noted in the gap analysis: the matcher remains a shared instance (concurrency-safe, holds an
immutable Judge reference); the *Judge* is what the factory builds. This matches the
documented two-pattern rule in `registry.go` (stateless→instance, stateful→factory).

---

## Decision 7 — Credentials & failure mapping (FR-007, US2)

**Decision**: Credentials via `ANTHROPIC_API_KEY` (SDK default). The Claude judge checks the
key is present and errors descriptively **before** issuing a request (US2-AC3). At call time,
unwrap SDK errors with `errors.As(&apierr *anthropic.Error)` and branch on
`apierr.StatusCode`: 401→auth, 429→rate-limit (after SDK retries), 5xx→backend; a
`stop_reason == "refusal"` or a verdict that fails schema parse → "judge could not render a
verdict" error. Every path returns a `%w`-wrapped error naming the cause; **no path returns
a verdict on failure** (FR-007). Context cancellation/timeout propagates as an error.

**Rationale**: Direct application of Constitution IV and the skill's Go error-handling
guidance. Classification is by status/stop-reason, not string-matching.

---

## Decision 8 — Hermetic testing & the L3 meta-test

**Decision**: Unit tests construct `comparator.NewSemantic(mockJudge, votes)` with a gomock
`core.Judge` — no network. The **L3 meta-test** (`features/meta/bad_meaning.feature`) runs
the godog suite hermetically with a **deterministic fake Judge** registered as `semantic`
(returns no-match for a wrong-meaning answer) and asserts Mentat goes RED; a green companion
proves the pass path. The live Claude backend is exercised only behind `//go:build e2e`
with a real key.

**Rationale**: Constitution V — hermetic by default, live behind `e2e`, L3 mandatory. The
fake Judge keeps the red/green proof fast and network-free.

---

## Decision 9 — Gherkin surface

**Decision**: `the result means "<expected meaning>"` (inline) and `the result means:`
(docstring), mirroring `the result contains/equals/matches regex`. The step builds
`comparator.ResultExpectation{Matcher:"semantic", Want:<meaning>}` and runs through the
existing `world.check` → result comparator → matcher-registry dispatch. **Zero change** to
`result.go`. Under `@runs(N>1)` the existing `world.check` guard hard-errors (US4 boundary).

**Rationale**: Smallest, most consistent surface; reuses the entire result-comparator path.

---

## Resolved conflicts / open decision

- **FR-006 temperature** → refined (Decision 4): model-dependent. *Flag, no decision needed.*
- **US4 / FR-012 / SC-007** → **deferred, needs Q decision** (plan.md Complexity Tracking).
  The aggregate path is CEL-only with no judge hook; composing semantic with `@runs(N)`
  requires either a CEL `means()` function (LLM I/O inside CEL eval) or a pre-compute pass —
  real scope for a P3 story. This plan ships US1–3 + vote.
