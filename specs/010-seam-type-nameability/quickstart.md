# Quickstart: validating Seam-Type Nameability (010)

**Spec**: [spec.md](./spec.md) | **Plan**: [plan.md](./plan.md) |
**Contracts**: [nameability-v2](./contracts/facade-nameability-v2.md) ·
[reporter-seam](./contracts/reporter-seam.md) ·
[report-format-golden](./contracts/report-format-golden.md)

Six checks. Each maps to a success criterion and is runnable without a live Tempo — this
feature is entirely hermetic. `make harness-up` is not required.

## Prerequisites

```bash
go version          # 1.25
make ci             # baseline: must be green BEFORE starting
```

`make ci` runs `lint test cover example` (`Makefile:17`). The `example` target is the
external-module witness — it is what proves SC-006.

## 0. The dependency direction still holds

**Criterion**: the constraint behind D5. Cheap, and the first thing to break if a seam type
drifts back to the facade.

```bash
go build ./...                                        # an import cycle fails here, loudly
grep -rn "thetonymaster/mentat\"" internal/           # EXPECT: no match
```

No package under `internal/` may import the root. Root imports `internal/report`,
`internal/engine` and `internal/registry`, so anything they consume lives beneath them and is
aliased upward — see [research R8](./research.md).

---

## 1. The report format is unchanged — run this FIRST

**Criterion**: SC-009. **Contract**:
[report-format-golden](./contracts/report-format-golden.md).

This is the gate that must be green *before* any reporter is rewritten, so it is also the
first thing to run at any later point.

```bash
go test ./internal/report/ -run TestReportFormatGolden -v
```

**Expected**: PASS, against goldens committed while the reporters still rendered from
`core.RunReport`.

Prove it can fail — this is the check, not a formality:

```bash
# temporarily change one json tag on a facade mirror, then:
go test ./internal/report/ -run TestReportFormatGolden
# EXPECT: FAIL, naming the format whose bytes moved. Revert.
```

Regenerate deliberately (only when a format change is *intended*):

```bash
MENTAT_UPDATE_GOLDEN=1 go test ./internal/report/ -run TestReportFormatGolden
```

---

## 2. An external module can implement all six seams

**Criterion**: SC-001. **Contract**:
[nameability-v2 §Proof obligation](./contracts/facade-nameability-v2.md).

```bash
go test . -run TestExternalFacadeOnly -v
```

**Expected**: PASS. The test declares a type per seam using facade imports only; compiling
is the proof. `Reporter` is the sixth — before this feature it could not be declared at all.

---

## 3. Every frozen type can be written from outside

**Criterion**: SC-002 (target: 0, from 4).

```bash
go test . -run 'TestFacadeNameability|TestPublicSurfaceGolden' -v
```

**Expected**: PASS, reporting **zero** unnameable reachable types under the v2 definition.

Review the surface diff by hand — it is the one-way door:

```bash
git diff specs/007-public-extension-api/contracts/public-surface.golden
```

**Expected**: the `Reporter` method line now reads `Report(res Results, w io.Writer) error`;
`RunReport` appears nowhere; `Results` and `ScenarioResult` lines carry their new fields;
new alias lines for `ExtractPolicy`, `HTTPSpec`, `AggregateDetail` and their expanded
fields; new `RunRecord` and `WithReporter` declarations. Nothing else.

---

## 4. The gate catches the *next* unnameable seam type

**Criterion**: SC-004. This is the root-cause fix; verify it by breaking it.

```bash
# On a scratch branch, add a method to a published seam whose parameter type
# has no facade name, e.g.:
#   Report(res Results, w io.Writer) error
#   Summarize(rep core.RunReport) string      // deliberately unnameable
go test . -run TestFacadeNameability
```

**Expected**: FAIL, naming **both** the offending type and the reaching position — e.g.
`method (Reporter) Summarize(rep RunReport)`. A failure that names only the type does not
satisfy the contract: the author has to go source-spelunking.

```bash
git checkout .   # discard the scratch change
```

---

## 5. A custom reporter works end to end

**Criterion**: SC-003, SC-008.

```bash
go test . -run TestCustomReporter -v
```

**Expected**: PASS. A facade-only reporter is declared, registered with `WithReporter`,
selected through `WithReports`, and its output written.

Equivalence (SC-008) is verified by construction under D5 rather than by inventory — the
built-ins render the *same* type a custom reporter receives, so the check is that they compile
against the published seam with their field reads unmodified:

```bash
go build ./internal/report/ && go vet ./internal/report/
```

**Expected**: clean. If a built-in needs a datum the published `Results` does not carry, it
will not compile.

## 6. Concurrent runs do not collide

**Criterion**: SC-010. **Requires `-race`** — without it this test can pass while broken.

```bash
go test . -race -run TestConcurrentReporterRegistration -v
```

**Expected**: PASS. Two genuinely concurrent `Run` calls register *different* reporters
under the *same* name; each uses its own, neither observes the other's. This is the
property the package-global map could not provide.

---

## Full gate

```bash
make ci
```

**Expected**: green, including `example` — `examples/kafkaecho` must still compile
**untouched** (SC-006). If that target needed a source edit, a change intended to be
additive was not.

```bash
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out
```

**Expected**: every touched package at or above the 80% floor. `/coverage` does this and
reports the lowest-covered functions.

---

## Documentation truth-check

**Criterion**: SC-005, FR-010, FR-015. Not automated — read them.

```bash
grep -n "boundary 4" docs/extending/stability.md          # EXPECT: no match
grep -rn "cmd/mentat calls report.EmitReports" internal/  # EXPECT: no match
grep -n "no concrete external demand" mentat.go           # EXPECT: Correlator only
```

Boundary 4 is **deleted**, not reworded — the gap it describes no longer exists. The
`Correlator` rationale stays; only the `Reporter` half is rewritten.
