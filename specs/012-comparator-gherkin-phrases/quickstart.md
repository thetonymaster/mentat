# Quickstart: validating 012

**Date**: 2026-09-10, updated 2026-09-11 after implementation | **Baseline**: `0f9dcea`
| Plan: [plan.md](./plan.md)

How to prove this feature works end to end, and how to reproduce the experiments that shaped it.
The §3 sections now name the API that actually shipped, and the results recorded are the ones
measured at implementation time — see [baseline.txt](./baseline.txt) for the full evidence.

## Prerequisites

```bash
go version                 # 1.25+
docker info                # required for the //go:build e2e lane
make harness-up            # Tempo + collector + orderflow SUTs
```

`make ci` runs lint + tests + coverage + the example module. **It does not compile the
`//go:build e2e` lane**, so it is not sufficient evidence for SC-012 — see below.

---

## 1. Reproduce the collision defect (R1) — do this first

This is the live bug at `0f9dcea`, and SC-011's regression test must be seen failing before the
fix. The planning experiment registered a broad pattern first (standing in for a built-in
`stepDefs` row) and a specific one second (a contributed phrase), both matching one sentence,
with an `sc.After` hook mirroring Mentat's verdict-authoritative hook (`steps.go:126-129`).

Measured result:

```
strict=false -> suiteStatus=0 firstRan=true  secondRan=false afterHookStepErr=<nil>
                "1 scenarios (1 passed)"
strict=true  -> suiteStatus=1 firstRan=false secondRan=false
                afterHookStepErr=ambiguous step definition, step text: the widget is green
                    matches:
                        ^the widget is (\w+)$
                        ^the widget is green$
```

**Read the first line carefully**: the broad pattern ran, the specific one never did, and the
scenario **passed**. Because `Pass: stepErr == nil` (`steps.go:127`), Mentat records that as a
green verdict nobody wrote.

## 2. Prove `Strict` churns no golden (SC-012, FR-007c)

Both surfaces, because `make ci` cannot see the second one:

```bash
# non-e2e, including the hermetic stdout golden (mentat_golden_test.go)
go test ./...

# the e2e stdout golden (SC-005) and then the full live-Tempo suite
go test -tags e2e -run TestGolden ./e2e/
go test -tags e2e -timeout 25m ./e2e/
```

Planning-time measurement with `Strict: true` patched into `run.go:411-419`:

| Surface | Baseline | With `Strict: true` |
|---|---|---|
| `go test ./...` | all `ok` | all `ok`, **0 FAIL** |
| e2e golden | — | `PASS` (14.68s) |
| full e2e | `ok 50.7s` | `ok 42.1s` |

**Zero churn.** A green `make ci` alone does **not** discharge SC-012 — run the e2e lane.

---

## 3. End-to-end validation *(after implementation)*

### 3.1 The feature itself (US1, SC-001)

Register a comparator that contributes a phrase, then write a feature file that names no
comparator and carries no payload:

```gherkin
Then the revenue is shaped like a quarterly report
```

Assert the verdict — pass/fail **and reason text** — is identical to the equivalent 011-style
generic step for the same expectation:

```gherkin
Then the "revenue-shape" comparator is satisfied by:
  """
  {"min": 4, "currency": "USD"}
  """
```

Equivalence across the two paths is what proves 012 added an invocation route rather than a
second semantics. Exercise capture counts 0, 1 and ≥3 (SC-013) — surplus-capture loss is exactly
what godog does silently and D9 makes impossible.

### 3.2 Engine isolation (US2, SC-002)

Build two engines with disjoint contributed phrases; run both in one process **in both orders**;
assert each binds only its own. Both orders matter — a first-writer-wins cache passes one
ordering and fails the other, which is how the deleted `sync.Once` (`precheck.go:76-91`) would
have manifested.

### 3.3 The rejection paths (US3, SC-003)

Each must fail the **engine build**, name the contributor and the offending value, and run no
scenario: identical contributed patterns (naming both), a pattern identical to a built-in's, an
unanchored pattern, an uncompilable pattern, a blank group/summary/example, and a phrase whose
comparator implements no capture parser. Record a mutation rehearsal per path in the test file.

> 011 hit a rehearsal that failed to go red because the **mutation** had not applied. "The
> mutation didn't fire" and "the guard is real" are indistinguishable from test output alone —
> so record what was mutated, not just that red was observed.

### 3.4 Ambiguity is now loud (SC-011)

The regression test from §1: a scenario whose text matches two well-formed anchored patterns is
reported FAILED, naming every matching expression. Confirm it **fails** against the pre-change
non-strict configuration first.

### 3.5 Validation surfaces (US4, SC-005)

```bash
# built-ins only; must stay byte-identical, and its docs must state the limit
go run ./cmd/mentat validate features/
go run ./cmd/mentat steps            # built-in rows byte-identical (FR-013)
go generate ./...                    # docs/steps.md regenerates; built-in rows unchanged
```

Then, from a consumer test binary:

```go
findings, err := mentat.Validate(ctx, cfg,
	mentat.WithFeatures("features/"),
	mentat.WithComparator("revenue-shape", newRevenueShape),
)
```

Assert **zero** `unbound-step` findings for a suite written in contributed phrases, and that a
genuinely misspelled step still produces exactly one — an engine-aware validator reporting
nothing would pass the first assertion too.

`mentat validate --help` states the limit a compiled binary cannot escape; a test asserts the
help text actually says so.

#### The other two finding classes this feature added

`unbound-step` alone does not walk you past either check convergence added, so exercise both.

**`step-argument` — on BOTH surfaces.** Add a built-in step carrying an argument its handler
cannot receive, e.g. a docstring under a step that declares none:

```gherkin
    Then the result contains "hi"
      """
      this body is read by nobody
      """
```

```bash
go run ./cmd/mentat validate features/     # -> [step-argument] …, exit 1
```

The binary catches this one: each built-in row's expected argument is derived by reflection
from the handler it registers, and all 40 are compiled in. Before this check the scenario
reported **PASSED** with the docstring silently discarded, so confirm it fails now. The same
file through `mentat.Validate` must produce the same finding — that agreement is the point.

**`ambiguous-step` — library path only.** Register two comparators whose contributed phrases
both match one sentence (legal: only *identical* patterns are rejected at build):

```go
// ^the revenue floor is (\d+) (\w+)$   and   ^the revenue floor is 4 (\w+)$
findings, err := mentat.Validate(ctx, cfg,
	mentat.WithFeatures("features/"),
	mentat.WithComparator("revenue-shape", newRevenueShape),
	mentat.WithComparator("revenue-shape-exact", newExactFloor),
)
```

Assert exactly one `ambiguous-step` naming **both** patterns, in registration order. Then run
the same suite — it must fail under `Strict` for the same reason, which is what makes the
validator's answer worth trusting. The binary cannot reach this case: it sees built-in
patterns only, and no two of those are known to match one sentence.

### 3.6 L3 meta-test (FR-016, SC-007, Constitution V)

A scenario using a contributed phrase whose assertion is **false** must make the run RED with the
comparator's own reason. A framework not proven to fail on bad behaviour is unfalsifiable.

> Per 011's R6: the L3 proof for a Go-registered comparator belongs in `internal/steps` as an
> in-process godog suite, **not** in `e2e/` — that lane drives a prebuilt `cmd/mentat` binary,
> which cannot contain a consumer-registered comparator.

### 3.7 Gates

```bash
make ci                                          # lint + test + cover + example
golangci-lint run ./...                          # 0 issues
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out   # 80% floor (SC-010)
go test -run TestPublicSurfaceGolden ./...       # new seams + method sets (SC-008)
go test -tags e2e -timeout 25m ./e2e/            # SC-012 — NOT covered by make ci
```

The public-surface golden records the two new seams, `ContributedPhrase`'s exported fields,
`Validate` and the `Finding` alias — **15 insertions, 0 deletions** against `main`. Nothing
removed or changed, so **011's `ExpectationParser` line is untouched**, which is the operational
test of D6's claim that this is a sibling rather than a replacement.

> **Run the e2e lane.** It is not paranoia. `make ci` does not compile `//go:build e2e`, and the
> e2e stdout golden caught a churn nothing else did — it captures godog's step-definition SOURCE
> LINE (`# metadata.go:75 -> *world`), so merely documenting `registerSteps` broke it. Both
> golden normalizers now collapse that line number and keep the filename.

> **What the sweep does not do.** `TestFacadeNameabilitySweep` does NOT demand the new aliases:
> it is seeded from the aliases that already exist, so removing one removes its seed. The guards
> are the public-surface golden and the compile-time witnesses in an EXTERNAL test package
> (`var _ mentat.PhraseContributor = ...`). See R11.

---

## Teardown

```bash
make harness-down
```

## If something surprises you

Stop and re-read [research.md](./research.md) before changing code. Two of this feature's
premises came from 011's D1 and one of them was false (R1) — the useful habit here is to check
whether the surprising thing is a premise nobody re-verified, especially anything about godog,
whose behaviour is version-pinned to `v0.15.1`.
