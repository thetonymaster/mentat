# Quickstart / Validation — Semantic (LLM-Judge) Result Matcher

How to prove the feature works end-to-end. References the contracts and data model rather
than restating them. No implementation code here.

## Prerequisites
- Go 1.25, repo at `…/phase4`.
- `go install go.uber.org/mock/mockgen@v0.6.0` (mocks), then `go generate ./...` to produce
  `MockJudge`.
- For the **e2e/live** path only: `export ANTHROPIC_API_KEY=…` and `make harness-up`.

## 1. Hermetic unit validation (no network) — US1, US2, vote
Run the comparator unit tests; they construct `comparator.NewSemantic(mockJudge, votes)`:

```
go test ./internal/comparator/ -run Semantic -v
```

Expected — a gomock `core.Judge` drives each case:
- verdict `match=true` → `Verdict{Pass:true}` (US1-AC1)
- verdict `match=false` → `Verdict{Pass:false, Reasons:[reason]}` (US1-AC2)
- judge returns an error → `Match` returns a hard error, **no Verdict** (US2-AC1/AC2)
- `votes=3`, two `true`/one `false` → pass; even-N tie → hard error (FR-015)

## 2. Config validation — US3, contracts/config-judge.md
```
go test ./internal/config/ -run Judge -v
```
Expected: defaults applied (`backend=claude`, `model=claude-opus-4-8`, `votes=1`);
`votes:0`→default 1; `votes:-1` and even `votes>1` → descriptive errors.

## 3. Backend resolution & wiring — US3
```
go test ./internal/engine/ -run Build -v        # unknown judge backend → descriptive error (US3-AC2)
go test ./internal/judge/ -v                     # claude factory registration + verdict mapping (hermetic)
```

## 4. L3 meta-test (MANDATORY, hermetic) — FR-011, SC-002
A godog meta-feature with a **deterministic fake Judge** registered as `semantic`:

```
go test ./internal/steps/ -run Meta -v           # or the e2e meta wiring, per repo convention
# drives features/meta/bad_meaning.feature
```
Expected: a wrong-meaning answer makes the scenario **RED** (non-zero, expected reason); the
green companion (correct meaning) **passes**. This is the "framework goes red on bad
behaviour" proof.

## 5. Live Claude path (e2e-gated) — real backend
```
ANTHROPIC_API_KEY=… go test -tags e2e ./internal/judge/ -run Claude -v
```
Expected: a paraphrased-but-correct answer → `match=true`; a contradictory answer →
`match=false` with a reason. (One real call per case; small token cost.)

## 6. End-to-end author experience (manual smoke)
With a `mentat.yaml` containing a `judge:` block and a target, a feature:

```gherkin
Feature: semantic answers
  Scenario: agent explains RAG
    Given the agent target "researchbot"
    When I run the agent with prompt "what is retrieval-augmented generation?"
    Then the result means "RAG augments an LLM with retrieved external documents"
```

Run via the normal runner; expect PASS when the answer is semantically correct, and on
failure a reason explaining the judge's decision (FR-008, SC-005).

## Coverage gate
```
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out   # (or /coverage)
```
Expected: `internal/judge`, `internal/comparator` (semantic), `internal/config`,
`internal/registry` all ≥ 80% (SC-008).

## Out of scope to validate here (deferred)
- Statistical semantic over `@runs(N)` (US4/FR-012/SC-007) — deferred pending Q decision;
  `the result means` under `@runs(N>1)` currently hard-errors by design.
