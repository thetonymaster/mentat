# Quickstart: validating 013

**Feature**: 013-builtin-pattern-disjointness | **Date**: 2026-09-11

How to prove this feature works, per user story. Details of the types are in
[data-model.md](./data-model.md); the decider's guarantees and non-guarantees are in
[contracts/decider.md](./contracts/decider.md).

## Prerequisites

```bash
go version                          # expect go1.25.x
git branch --show-current           # expect 013-builtin-pattern-disjointness
git log --oneline -1                # expect the branch to descend from f6bb402 (the 012 squash)
git status --short                  # expect clean apart from this feature's work
```

This branch is cut directly from `f6bb402`, so **no rebase is required** — an earlier draft of this
section called for one. It also has **no upstream set**, deliberately: the worktree was created from
`origin/main`, which left `git push` pointing at `main` until it was unset. Set an upstream
explicitly when you first push (`git push -u origin 013-builtin-pattern-disjointness`).

`make ci` is `lint test cover example`. **It does not compile the `e2e` lane**, so a green `make
ci` is not evidence the stdout goldens are current — see the last section.

---

## US1 — no code path resolves by position

The change is in `internal/steps/stepargs.go`: `matchBuiltin` and `matchPhrase` stop returning
their first match as though it were their only one, and `stepProblem` decides from the total count.

```bash
go test ./internal/steps/ -run 'TestStepArgument|TestCollidingPatterns' -v
```

**Expect**: passing tests covering all four multiplicity cases — two built-ins, two contributed
phrases, one of each, and exactly one. The single-match rows must be unchanged from `main`.

**The rehearsals are the real check** (SC-002). Removing the deferral must redden a test for
*both* source combinations, and each mutation must be confirmed to have landed first:

```bash
# For each mutation: apply it, confirm the edit is present, then run.
# "The mutation didn't fire" and "the guard is real" are indistinguishable from
# test output alone — 012 hit this exact trap.
go test ./internal/steps/ -run 'TestStepArgumentDefersWhenTwo'
```

**Expect**: RED, naming the deferral case. A green run here means the mutation did not apply —
diagnose that before trusting anything.

---

## US2 — disjointness is decided

```bash
# The gate over the built-in set.
go test ./internal/steps/ -run 'TestBuiltinStepPatternsAreDecidedDisjoint' -v
```

**Expect**: all pairs decided, zero intersecting, and the pair count plus wall clock **reported**
(FR-016 — reported, not asserted as a ceiling; a slow CI runner must not turn this red).
Prototype baseline for comparison: 780 pairs, 0 intersecting, max 74 product states, 0.36s
including compilation.

```bash
# Prove the gate bites. Add a deliberately overlapping row to stepDefs, e.g.
#   ^the result contains "revenue"$     against the existing
#   ^the result contains "([^"]*)"$
go test ./internal/steps/ -run 'TestDecidedDisjointnessGateFiresOnACollision' -v
```

**Expect**: RED, naming **both** patterns and a witness string — and the witness must verify
against both patterns (`MatchString` true on each). This must fail **with no feature file
present**: the point of a decider is that it needs no corpus (SC-003).

```bash
# The contributed side: overlap is reported, composition still succeeds.
go test ./ -run 'TestValidateReportsPatternOverlap' -v   # ROOT package: SC-009 names mentat.Validate
```

**Expect**: composition succeeds, exactly one `pattern-overlap` finding per overlapping pair,
carrying a verified witness and naming the contributing comparator (SC-009).

```bash
# Refusal, not guessing.
go test ./internal/steps/ -run 'TestIntersectsRefuses|TestClosureRefuses' -v
```

**Expect**: `\b`, `\B`, and multi-line `^`/`$` each produce an error naming the pattern and the
construct. **No case for "disjoint"** on these (FR-012, SC-010). Note `(?i)` is *not* here — it is
handled, per R3.

---

## US3 — the decider is falsifiable

This is the story that catches defects in US2, and it has already earned its place: the prototype
decided 780 pairs correctly and called `^(?i)abc$` and `^abc$` disjoint. See R3.

```bash
go test ./internal/steps/ -run 'TestDecider|TestIntersectsKnownVerdicts' -v
```

**Expect**: the known-verdict corpus passes, every positive verdict's witness re-verifies, and the
differential rows find no string shared by a pair called disjoint.

```bash
# The fuzz target's SEED CORPUS runs under plain `go test` — that is what make ci
# exercises. Fuzzing proper needs the flag and a time bound.
go test ./internal/steps/ -run FuzzDecider          # seeds only, as in CI
go test ./internal/steps/ -fuzz FuzzDecider -fuzztime 60s
```

**Expect**: no failures. A failure is reported as a **decider defect**, never as a disjointness
defect (D4) — the sampler's job here is to falsify the mechanism, not the claim.

**Mutations to rehearse** (SC-008), each confirmed landed before its red is trusted:

| Mutation | Result (measured 2026-09-11) |
|---|---|
| Drop the rune-class **upper** boundaries (`cuts[rg[1]+1]`) | **GREEN — and correctly so.** See below. |
| Collapse the alphabet to a **single class** | RED, 4 rows — this is the false-disjoint direction |
| Treat an unmodelled `EmptyOp` as satisfiable | RED on both refusal tests |
| Ignore `FoldCase` in the single-rune path | RED — reproduces **the R3 defect** exactly |
| Collapse the two closures into one (`atEnd` fixed) | RED broadly — `$`-anchored patterns become unsatisfiable |

The first row is the one to understand before adding a test for it. The partition keeps every
range's **lower** bound, so if a class's representative falls outside a range, that range's own
lower bound is a kept cut above it and the range cannot intersect the class at all. The partition
can therefore only **over**-approximate, never under — so it cannot produce a false "disjoint". It
can produce a false "intersect", and witness re-verification catches that loudly as a decider
defect. The upper cuts buy precision, not correctness.

The obvious reading of that green — "the alphabet is unguarded" — is wrong, and acting on it means
writing a test that cannot distinguish the two cases. The single-class mutation is the one that
probes the direction that matters.

---

## US4 — records state their basis

No command; read and confirm. Each site asserting built-in disjointness must say it is **decided**,
and the generated-sentence test must be described as an independent cross-check rather than as the
evidence (FR-004, SC-004).

```bash
# Find every remaining claim that still hedges.
grep -rn "as measured\|nine fillers\|evidence, not proof" \
  CHANGELOG.md CLAUDE.md docs/ internal/steps/ specs/012-*/contracts/
```

**Expect**: no hit that presents sampling as the basis for built-in disjointness. Hits that
describe the *generator* accurately, or that record history explicitly as history, are fine —
the goal is that no reader mistakes a sample for a decision.

---

## Whole-feature gate

```bash
gofmt -l .                    # expect no output
go vet ./...
golangci-lint run ./...
make ci                       # lint test cover example
```

Coverage floor is 80% per touched package (FR-009):

```bash
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out
```

**Then the lane `make ci` does not compile** — required, because 013 adds a finding class and
therefore touches documented surface and the stdout goldens (R8):

```bash
make harness-up
go test -tags e2e ./...
make harness-down
```

**Expect**: green, with any golden churn understood rather than blanket-refreshed. A green `make
ci` alone is **not** sufficient evidence for this feature.

Finally, the one thing that should *not* move:

```bash
go test ./ -run TestFacadeNameabilitySweep -v    # public-surface golden
```

**Expect**: unchanged. The decider is `internal/`-only. If the golden moves, stop — something
leaked onto the facade (010 D5).
