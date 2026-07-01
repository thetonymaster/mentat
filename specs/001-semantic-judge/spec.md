# Feature Specification: Semantic (LLM-Judge) Result Matcher

**Feature Branch**: `001-semantic-judge`

**Created**: 2026-06-29

**Status**: Draft

**Input**: User description: "Let's specify judge, read the docs and let's start"

## Overview

Mentat grades agent and microservice behaviour with deterministic result matchers
(`exact`, `contains`, `regex`, `json-subset`, `status`, `schema`). These cannot grade
the *meaning* of a free-form LLM answer: an agent that replies "I found three relevant
papers on RAG" and one that replies "Here are 3 retrieval-augmented-generation
references" are equivalent in intent but share almost no exact text.

This feature adds the **`semantic` result matcher** — the only non-deterministic matcher
in the framework — which asks an **LLM Judge** whether the run's result *means* what the
author expected. The Judge is a pluggable seam (the foundational design and the
constitution both name `Judge` as a first-class seam), so the backend is swappable and
the whole suite stays hermetically testable with a stand-in Judge. The default backend is
Claude (Anthropic API).

The `@runs(N)` multi-run aggregate path already exists and is reused as-is for
statistical semantic assertions; this feature does not rebuild it.

> Scope note: this feature is the **non-Judge-deferred** completion of Phase 4
> ("Semantic"). The `@runs(N)` half already shipped. The pluggable `Judge` interface +
> Claude backend + `semantic` matcher are what remain.

## Clarifications

### Session 2026-06-29

- Q: How should v1 handle Judge non-determinism (the judge can flip verdicts)? → A: Build a configurable best-of-N majority vote, defaulting to N=1 (a single call). The voting mechanism is in scope; the default behaviour is one call.
- Q: What is the v1 data-egress posture when sending the agent's (possibly sensitive) result to the external LLM judge? → A: Send result content by default and document the egress clearly; configuring the `semantic` matcher + `claude` backend is itself the opt-in. No redaction in v1.
- Q: What should the Judge's structured Semantic Verdict contain? → A: Exactly a match decision + a human-readable reason. No confidence score in v1.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Assert the meaning of a free-form agent answer (Priority: P1)

A test author writing a behaviour spec for an LLM agent wants to assert that the agent's
final answer *means* the expected thing, without brittle string matching. They write a
single Gherkin step expressing the expected meaning, run the spec, and get a PASS/FAIL
verdict with a human-readable reason explaining the Judge's decision.

**Why this priority**: This is the entire reason the feature exists — grading fuzzy LLM
output by intent. It is the MVP: with only this story, an author can assert semantic
correctness of agent answers, which no existing matcher can do.

**Independent Test**: Author a scenario with `the result means "<expected meaning>"`
against a SUT whose answer is paraphrased-but-correct; the scenario passes. Point it at a
SUT whose answer is wrong-in-meaning; the scenario fails with a reason. Both run against a
stand-in Judge with a fixed verdict — no live model needed for the test.

**Acceptance Scenarios**:

1. **Given** an agent whose answer is a correct paraphrase of the expected meaning,
   **When** the scenario asserts `the result means "<expected meaning>"`,
   **Then** the step passes.
2. **Given** an agent whose answer contradicts or omits the expected meaning,
   **When** the scenario asserts `the result means "<expected meaning>"`,
   **Then** the step fails and the failure reason includes the Judge's human-readable
   rationale.
3. **Given** an expected-meaning string that is empty or malformed,
   **When** the scenario is loaded,
   **Then** the framework fails fast at scenario start, naming the offending step.

---

### User Story 2 - Never trust a Judge that could not answer (Priority: P1)

A maintainer relies on Mentat's verdicts being trustworthy. When the Judge backend cannot
render a verdict — network failure, missing/invalid credentials, rate limit, timeout, or a
malformed/unparseable response — the framework must fail the run with a hard, descriptive
error that names the cause, and must **never** convert that into a guessed or zero-value
PASS/FAIL.

**Why this priority**: A behaviour-test framework's only asset is trust in PASS/FAIL. A
silent fallback on a Judge error would silently corrupt verdicts — the worst possible
outcome. This is co-P1 with US1: shipping semantic matching without this is shipping a
liability.

**Independent Test**: Inject a stand-in Judge that returns each failure mode (error,
malformed output). Assert the scenario errors with a message naming the cause, and that no
PASS or FAIL verdict is emitted from a failed Judge call.

**Acceptance Scenarios**:

1. **Given** the Judge backend returns a transport/auth/rate-limit error,
   **When** a `semantic` assertion runs,
   **Then** the run errors with a descriptive message naming the cause (not a verdict).
2. **Given** the Judge returns a response that does not conform to the expected verdict
   shape, **When** a `semantic` assertion runs, **Then** the run errors describing the
   unparseable response — never a guessed verdict.
3. **Given** judge credentials are absent in a live (non-hermetic) run,
   **When** a `semantic` assertion runs, **Then** the run errors naming the missing
   credential, before any model call is attempted.

---

### User Story 3 - Swap and test the Judge backend without touching comparators (Priority: P2)

A maintainer configures which Judge backend is used (defaulting to Claude) through
`mentat.yaml`, and wires it at the single composition root via a Judge registry — the same
pattern as every other seam. The full unit/CI suite runs hermetically against a stand-in
Judge with no network access; live-Claude judging is gated behind the e2e build tag.

**Why this priority**: Portability and hermetic testability are core constitution
principles (III, V). Without a pluggable Judge and a stand-in for tests, the suite could
not run deterministically offline and the backend could not be swapped — but US1 still
delivers value with the default backend, so this is P2.

**Independent Test**: Register a stand-in Judge under the registry and select it via
config; run a semantic scenario end-to-end with zero network calls. Select an unknown
backend name and confirm a descriptive "unknown judge" error. Confirm the `semantic`
matcher and the result step are unchanged when the backend changes.

**Acceptance Scenarios**:

1. **Given** `mentat.yaml` selects the default judge backend, **When** the engine builds,
   **Then** the Claude-backed Judge is wired at the composition root.
2. **Given** `mentat.yaml` selects an unregistered judge backend name, **When** the engine
   builds, **Then** it errors with a message naming the unknown backend.
3. **Given** a stand-in Judge registered for tests, **When** the hermetic suite runs,
   **Then** every semantic scenario resolves with zero network access.

---

### User Story 4 - Assert statistical semantic behaviour across runs (Priority: P3) — DEFERRED

> **Deferred to a fast-follow cycle** (decision recorded in `plan.md` → Complexity Tracking).
> The `@runs(N)` aggregate path is CEL-only and has no judge hook, so this does not compose
> for free; it needs a judge-backed `means()` CEL function. Not in this cycle's task list.

A test author wants to assert that the agent *usually* answers correctly — e.g. "in at
least 80% of runs the answer means X" — because a single LLM run proves nothing about
typical behaviour. They combine the semantic matcher with the existing `@runs(N)` tag.

**Why this priority**: High value for real LLM evaluation, but it composes two features
that each stand alone; the single-run semantic assertion (US1) is usable without it, so
this is P3.

**Independent Test**: Tag a scenario `@runs(N)` and express a semantic-over-runs assertion
through the existing aggregate path against a stand-in Judge whose verdict varies per run;
confirm the aggregate threshold passes/fails as expected.

**Acceptance Scenarios**:

1. **Given** a `@runs(N)` scenario, **When** the author asserts a rate-style semantic
   property over the runs, **Then** the existing aggregate path evaluates it using the
   semantic verdict per run.
2. **Given** a per-run Judge failure inside a `@runs(N)` scenario, **When** the aggregate
   runs, **Then** the failed run is recorded as a typed, visible failed sample (consistent
   with the existing failed-run policy), never silently dropped.

---

### Edge Cases

- **Judge non-determinism (accepted limit):** the Judge can flip its verdict between
  identical calls. The framework minimizes this with deterministic-leaning settings
  (structured output always on; `temperature: 0` where the model accepts it, otherwise
  structured output + a fixed prompt + the vote) but does not eliminate it. `@runs(N)` covers
  *SUT* variance, not *Judge* variance; this limitation is documented, not hidden.
- **Empty / whitespace result:** an agent that produced no answer is graded against the
  expected meaning like any other content; the author may pre-gate with a deterministic
  matcher.
- **Replay path:** running comparators against a stored/replayed trace re-evaluates the
  `semantic` matcher against the captured result, which issues a live Judge call (a cost,
  documented). Replay is otherwise unchanged.
- **Pinned + multi-run:** semantic obeys the existing pinned/`@runs(N)` rules; no new
  multi-run-against-pinned behaviour is introduced.
- **Ambiguous Judge output:** a verdict the framework cannot interpret as match/no-match
  is a hard error (US2), not a coin-flip.
- **Vote tie (even N):** when a best-of-N majority vote has no strict majority (possible
  only at even N), the framework errors descriptively rather than guessing a verdict; an
  odd N is recommended.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The framework MUST provide a `semantic` result matcher that decides whether
  the run's result content matches an author-supplied expected meaning, yielding a PASS or
  FAIL verdict.
- **FR-002**: Test authors MUST be able to author a semantic assertion with a Gherkin step
  (`the result means "<expected meaning>"`, available inline and as a docstring),
  consistent with the existing result-matcher steps.
- **FR-003**: The `semantic` matcher MUST delegate the meaning judgement to a pluggable
  `Judge` seam (an interface), so the judging backend is swappable and is never hard-coded
  into the matcher or the engine.
- **FR-004**: The default Judge backend MUST be Claude (Anthropic API); the backend and its
  parameters MUST be selectable through `mentat.yaml` (a `judge:` configuration block),
  with credentials supplied via the environment.
- **FR-005**: The Judge backend MUST be resolved by name from a Judge registry and wired at
  the single composition root, consistent with the other seams; an unknown backend name
  MUST produce a descriptive error.
- **FR-006**: Judge invocations MUST use deterministic-leaning settings to minimize verdict
  flips: structured output is always enabled, and `temperature: 0` is sent only on models
  that accept it (e.g. Sonnet 4.6 / Haiku 4.5). On models that reject a temperature parameter
  (the Opus-tier default), determinism leans on structured output + a fixed prompt + the
  best-of-N vote (FR-015).
- **FR-007**: On any Judge failure (transport, authentication, rate limit, timeout, or a
  malformed/unparseable response), the matcher MUST return a hard, descriptive error
  naming the cause — never a guessed, zero-value, or fallback verdict.
- **FR-008**: A failing semantic verdict MUST carry a human-readable reason (the Judge's
  rationale) in the verdict reasons, surfaced through the existing reporting so the author
  understands *why* without re-running.
- **FR-009**: The `semantic` matcher MUST consume only `Evidence` for its inputs (the
  captured result/`Output`); it MUST NOT read a `TraceStore` or `Driver`. The Judge is the
  comparison strategy, not a data source.
- **FR-010**: The framework MUST support running the full unit/CI suite hermetically using a
  stand-in Judge with no network access, and MUST gate live-backend judging behind the
  `e2e` build tag.
- **FR-011**: The mandatory L3 meta-test MUST prove Mentat goes RED when a result does NOT
  mean the expected thing (and a companion case proves the green path).
- **FR-012**: *(DEFERRED — see US4)* The `semantic` matcher MUST compose with the existing
  `@runs(N)` aggregate path so authors can express statistical semantic properties without
  new multi-run machinery.
- **FR-013**: An empty or malformed expected-meaning expression MUST fail fast at scenario
  start, naming the offending step (consistent with the other matchers).
- **FR-014**: *(DEFERRED — see US4)* A per-run Judge failure inside a `@runs(N)` scenario
  MUST be recorded as a typed, visible failed sample, consistent with the existing failed-run
  policy — never silently dropped from the sample.
- **FR-015**: The framework MUST support a configurable best-of-N majority vote for a
  semantic assertion (N Judge calls, the majority decision wins), configured through the
  `judge:` block and defaulting to **N=1** (a single call). A vote **tie** (possible only
  at even N) MUST be a hard, descriptive error — never a guessed verdict — and an odd N is
  recommended.
- **FR-016**: The framework MUST send the run's result content to the external Judge by
  default — selecting the `semantic` matcher with the `claude` backend is itself the
  opt-in — and MUST clearly document this data egress (the agent's output, which may
  contain sensitive data, leaves the machine for a third-party API). No content redaction
  is provided in v1.

### Key Entities

- **Judge**: a backend that, given a candidate result and an expected meaning, renders a
  semantic verdict. The swappable seam; default implementation is Claude-backed.
- **Semantic Verdict**: the Judge's structured answer — exactly a match/no-match decision
  plus a human-readable reason. No confidence score in v1.
- **Semantic Expectation**: the author-supplied expected-meaning text (and target
  selection) carried from the Gherkin step into the matcher.
- **Judge Configuration**: the `judge:` block — backend name (default `claude`), model,
  temperature (default 0), vote count N (default 1), and the environment-sourced
  credential reference.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A test author can assert the meaning of a free-form agent answer with a
  single Gherkin step, with no exact-string or regex workaround.
- **SC-002**: The L3 meta-test goes RED on a wrong-meaning answer and GREEN on a
  correct-meaning answer — 100% of the meta cases behave as asserted.
- **SC-003**: 0% of Judge-backend failures are reported as a PASS or FAIL verdict; every
  such failure surfaces as a hard error naming the cause.
- **SC-004**: The full unit/hermetic suite completes with zero live Judge calls and zero
  network access, and is deterministic across repeated runs.
- **SC-005**: Every failing semantic assertion includes a human-readable reason sufficient
  for the author to understand the failure without re-running the scenario.
- **SC-006**: Swapping the Judge backend requires changes only to configuration and
  registry wiring — zero changes to the `semantic` matcher, the result comparator, or the
  result Gherkin step (proven by a stand-in backend substituting for Claude in tests).
- **SC-007**: *(DEFERRED — see US4)* An author can express "at least N% of runs mean X" by
  combining the `semantic` matcher with `@runs(N)`, using the existing aggregate path.
- **SC-008**: Every touched package retains ≥80% test coverage.

## Out of Scope

- **The `Judge` backend's own development cycle beyond the default Claude backend.**
  Additional backends (e.g. a different model vendor, a self-hosted model) ride the same
  registry later; v1 ships the Claude backend plus a test stand-in.
- **Confidence-score / thresholded semantic matching.** The v1 verdict is exactly a match
  decision + reason. A numeric confidence score (informational or thresholded) is a
  deferred, additive extension to the verdict contract.
- **Semantic grading of intermediate / per-span results** (the Phase 3 span-attribute
  source). v1 grades the boundary result (`Evidence.Output`); per-span semantic grading is
  a later additive extension.
- **Caching / memoizing Judge verdicts** (e.g. on the replay path). v1 re-invokes the
  Judge; verdict caching to cut cost is a future optimization.
- **Rebuilding `@runs(N)` / aggregate assertions.** That shipped already; this feature
  consumes it unchanged.
- **Statistical semantic over `@runs(N)` (US4 / FR-012 / FR-014 / SC-007) — deferred this cycle.**
  Requires a judge-backed `means()` CEL function in the aggregate path (LLM I/O inside CEL
  evaluation); planned as a fast-follow. This cycle ships single-run semantic + the vote.

## Assumptions

- The default and only production Judge backend in v1 is **Claude (Anthropic API)**, with
  the credential supplied via an environment variable; hermetic tests substitute a
  stand-in Judge.
- The Judge returns a **structured verdict** — exactly a match decision + reason (no
  confidence score in v1); the matcher maps a failing decision's reason into the verdict
  reasons.
- v1 grades the **final boundary result** (`Evidence.Output`), mirroring the design's
  "fuzzy agent answers" use case.
- v1 supports a **configurable best-of-N majority vote** with structured output and
  deterministic-leaning settings (`temperature: 0` where the model accepts it),
  **defaulting to N=1** (a single call); an even-N tie is a hard error.
- The agent's **result content is sent to the external Judge by default**; choosing the
  `semantic` matcher + `claude` backend is the opt-in, and the egress is documented. No
  redaction is provided in v1.
- The **`@runs(N)` multi-run aggregate path already exists** and is reused, not rebuilt;
  the existing failed-run policy applies to per-run Judge failures.
- **Judge non-determinism is an accepted, documented limitation** — minimized, not
  eliminated, by structured output (always on), `temperature: 0` where the model accepts it,
  and the best-of-N vote.
- The Gherkin surface word is **`means`** (`the result means "..."`); this mirrors the
  existing `the result contains/equals/matches regex` family. (Subject to `/speckit-clarify`
  if a different phrasing is preferred.)
