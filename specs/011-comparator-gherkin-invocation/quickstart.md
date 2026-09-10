# Quickstart: Validating Custom-Comparator Gherkin Invocation (011)

**Date**: 2026-09-10 | **Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md)

How to prove this feature works, in the order the proofs become meaningful. Every command
here is runnable from the repo root. No live Tempo is needed for anything except the final
regression sweep.

---

## Prerequisites

```bash
go version          # expect go1.25.x
gofmt -l .          # expect no output
go vet ./...        # expect clean
```

No `make harness-up` required. Every check in sections 1–5 is hermetic — gomock `TraceStore`,
in-process godog, no network.

---

## 1. The seam is nameable from outside the module

The first thing to check, because it is the one 010 built machinery to enforce and the first
new seam since that machinery landed.

```bash
go test ./... -run TestFacadeNameabilitySweep -v
go test ./... -run TestPublicSurfaceGolden
```

**Expected**: both pass. The golden gains exactly two lines and no more:

```text
method (ExpectationParser) ParseExpectation(text string) (Expectation, error)
type ExpectationParser = core.ExpectationParser
```

**Then prove the gate is real** (SC-006) — by planting a probe, **not** by deleting the alias.

Deleting the alias does *not* fail the sweep: it seeds only from facade alias targets
(`surface_test.go:1191-1198`), so an unaliased interface is never walked and reports zero
offenders. It vanishes from the gate rather than tripping it.

Temporarily add to `internal/core`, with the alias left in place:

```go
type XProbeSpec struct{ Note string }   // deliberately no facade alias

type ExpectationParser interface {
	ParseExpectation(text string) (Expectation, error)
	XProbe() XProbeSpec                 // temporary
}
```

```bash
go test . -run TestFacadeNameabilitySweep
```

**Expected**: FAILS with

```text
internal/core.XProbeSpec — reached by method (ExpectationParser) XProbe
```

Revert both edits; green again. A gate not observed failing has not been tested — and per
010's recorded rehearsal (`surface_test.go:1141`), a probe that *fails* to go red has
historically meant a bug in the gate rather than a healthy surface.

---

## 2. The grammar changed by exactly one row

```bash
go test ./internal/steps/ -run 'TestStepDocs|TestStepMetadata|TestNoDirectStepRegistration' -v
go run ./cmd/mentat steps
```

That covers all five drift tests: `TestStepDocsMirrorsTable`,
`TestStepDocsGroupsAreContiguous`, `TestStepMetadataMatchesRegistration`,
`TestStepMetadataFieldsPresent` and `TestNoDirectStepRegistration`.

**Expected**: the drift tests pass **with their assertions unmodified** (SC-003), and
`mentat steps` lists a new `Extend` group containing one phrase with a non-blank summary and a
valid example.

```bash
git diff --stat docs/steps.md
git diff internal/steps/metadata.go | grep -c '^+.*pattern:'
```

**Expected**: `docs/steps.md` regenerated (FR-013), and the pattern count is **1** (SC-002).
More than one means the design drifted toward Option B and the plan needs re-reading.

---

## 3. A custom comparator runs from a feature file

The core capability (US1). The shape follows
`TestFeatureExercisesGrammarAgainstFakeEngine` (`internal/steps/steps_test.go:58`): build a
real engine over a gomock store, register a test comparator, run an inline feature.

```bash
go test ./internal/steps/ -run TestCustomComparatorFromGherkin -v
```

The test registers its comparator through the existing extras option — no new machinery:

```go
eng, err := engine.Build(cfg, st, cor,
    engine.WithExtraComparator("revenue-shape", newRevenueShape))
```

and drives it with an inline feature:

```gherkin
Then the "revenue-shape" comparator is satisfied by:
  """
  { "min": 4 }
  """
```

**Expected**: the suite exits 0, the comparator received the expectation *its own*
`ParseExpectation` produced from that docstring, and the verdict's qualifiers and judge usage
were recorded exactly as for a built-in step.

---

## 4. It goes red — the proof that actually matters

A test framework that cannot be shown to fail correctly is unfalsifiable (Constitution V).
This is the L3 obligation, and per [research R6](./research.md) it lives here rather than in
`e2e/`, because the `e2e/` suite drives a prebuilt `cmd/mentat` binary that cannot contain a
Go-registered comparator.

```bash
go test ./internal/steps/ -run 'TestCustomComparatorGoesRed|TestCustomComparatorParseError' -v
```

**Expected**:

| Case | Outcome |
|---|---|
| Registered comparator returns a failing verdict | Suite status non-zero; output carries the comparator's own reasons |
| `ParseExpectation` returns an error | Suite status non-zero; output names the comparator and wraps its error |

A green-only test does not satisfy FR-015 and does not close SC-007.

---

## 5. Every failure names what failed

```bash
go test ./internal/steps/ -run TestCustomComparatorErrors -v
```

**Expected** — a table with one row per D5 mode, asserting on error text (SC-004):

| Input | Error must contain |
|---|---|
| `"typo-name"`, unregistered | `typo-name`, **and** at least one registered name |
| `"plain"`, registered without `ExpectationParser` | `plain`, and that it cannot be driven from Gherkin |
| Parser returns an error | The comparator name, and the wrapped cause |

Check the captured-name echo explicitly: a name with an embedded quote truncates at the quote,
and the error must show the truncated capture verbatim so the author can see why.

---

## 6. Nothing else moved

```bash
make ci
go vet -tags e2e ./...
cd examples/kafkaecho && go build ./... && cd -
```

**Expected**: all clean.

`go vet -tags e2e ./...` is **not optional** (SC-010). `make ci` has no e2e target, so the
e2e package can stop compiling without a single gate going red — which is exactly what
happened for six commits during 010. This feature adds no e2e test, but it adds a `core`
interface, and `e2e/` imports `core`.

`examples/kafkaecho` is the external-module witness (SC-008): a separate module importing the
facade only. It must build **untouched** — if it needs an edit, this feature broke the public
surface.

---

## 7. Coverage floor

```bash
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
```

**Expected**: every touched package at or above 80% (SC-009). `internal/steps` and
`internal/engine` are the two that move.

The four error paths in section 5 are the ones most likely to be left uncovered — table rows
that assert the happy path and skip the rejections were a `BLOCK` finding twice during 010.
Check them specifically rather than trusting the package total.

---

## Done when

- [ ] Sweep and golden pass; sweep observed **failing** with the alias removed
- [ ] Exactly one `stepDefs` row added; drift tests pass unmodified; `docs/steps.md` regenerated
- [ ] A custom comparator runs from a feature file and its verdict lands like any other
- [ ] Red proven for both a failing verdict and a parser error
- [ ] All three failure modes name the offending value; unknown-name lists registered names
- [ ] `make ci` green, `go vet -tags e2e ./...` clean, `examples/kafkaecho` builds untouched
- [ ] Coverage floor held, error paths covered specifically
