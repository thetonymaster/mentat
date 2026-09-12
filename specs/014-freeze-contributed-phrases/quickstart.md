# Quickstart: validating the phrase snapshot

**Feature**: `specs/014-freeze-contributed-phrases`
**Date**: 2026-09-11

How to prove this feature works, and how to prove the proof is real. Contract details
live in [`contracts/phrase-snapshot.md`](contracts/phrase-snapshot.md); entity shapes in
[`data-model.md`](data-model.md); decisions in [`research.md`](research.md).

## Prerequisites

- Go 1.25 (module `github.com/thetonymaster/mentat`)
- No network, no Tempo, no `make harness-up`. Everything here is hermetic — the feature
  touches engine composition, not trace resolution.

## The one-command gate

```sh
make ci          # lint + test + cover + example — the source of truth
```

Race detection matters for this feature specifically (SC-004), and the isolation test is
in the **root** package:

```sh
go test -race -run 'Phrase' ./... 
```

## Baseline first

Establish green *before* changing anything, so a later red is attributable:

```sh
make ci
go test -race ./internal/engine/... ./internal/steps/... .
```

Record the result. "It was already failing" is the cheapest explanation to rule out and
the most expensive to discover late.

**Expect exactly one test to go red by design.**
`TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine`
(`internal/steps/phrase_test.go:820`) asserts `after == 2` — the behaviour this feature
removes. It is inverted deliberately under spec **D1** (task T015a), and it is the *only*
assertion this feature authorises changing. Any other red is a real failure.

---

## Validation scenarios

Each maps to a success criterion in [`spec.md`](spec.md). A scenario is validated only
when it has been observed **failing** against unfrozen code and **passing** against
frozen code — see *Mutation rehearsal*.

### 1. One vocabulary across all three surfaces — SC-001, FR-005

The feature's reason for existing.

**Setup**: one engine, one comparator whose `ContributedPhrases()` returns pattern A on
its first call and pattern B on every call after.

**Exercise**: `Validate`, then `StepReference`, then `Run` — and again in other orders.

**Expect**: all three report the same vocabulary. A feature file written against the
validated sentence binds and executes; no step comes back unbound.

**Pre-fix behaviour** (what you should see if you revert the freeze): the surfaces
disagree, and which one gets A depends purely on call order.

### 2. Invoked exactly once per engine — SC-002, FR-001

**Setup**: a comparator that increments a counter inside `ContributedPhrases()`.

**Exercise**: build one engine, then call every surface several times.

**Expect**: counter reads exactly `1`. Not "small", not "stable" — `1`.

### 3. Mutation cannot reach across the boundary — SC-003, FR-002/FR-004

Two directions, both required — but they are proven in **different phases**, because they
fail at different times (research R8, corrected):

| Direction | Action | Expect | Red when? |
| --- | --- | --- | --- |
| comparator → engine | comparator mutates the slice it returned at build | engine's answer unchanged | **Before the freeze** (Phase 3, T008a) — today the engine re-invokes and sees the mutation |
| caller → engine | mutate the slice a call returned | next call returns the original | **After the freeze** (Phase 5, T021) — the snapshot is still handed out by reference |

Only the second needs a copy. The first is delivered by the capture transform, which
builds `[]PhraseBinding` element-wise and therefore never retains the comparator's slice.
Testing the first *after* the freeze would show green with no implementation behind it.

### 4. Two engines stay disjoint — SC-004, FR-007

**Do not write a new test for this.** `TestContributedPhrasesAreScopedToTheirEngine`
(`custom_phrase_isolation_test.go:121`) already encodes it, in **both** construction
orders, precisely because a first-writer-wins cache passes one order and fails the other.

```sh
go test -race -run 'TestContributedPhrasesAreScoped|TestAForeignPhraseIsUnbound' .
```

Include the second pattern. `TestAForeignPhraseIsUnboundUnderAnotherEngine`
(`custom_phrase_isolation_test.go:159`) is, by its own comment, "the other half, and **the
one that actually detects a leak**" — the two `…AreScoped…` tests "would still pass if
every engine saw every phrase", because each engine's own sentence binds either way. A
regex matching only `TestContributedPhrasesAreScoped` gives false confidence.

All three must pass **unchanged**. Editing it to accommodate the implementation would destroy
the regression oracle for the one way this feature can go badly wrong (research R6).

### 5. The silent majority is untouched — SC-005, FR-009

Engines with no contributing comparators are the overwhelmingly common case.

**Expect**: deep-equal step reference, deep-equal validation findings,
identical run results. The goldens are the check — if any golden moves, the change
leaked past its blast radius.

Three oracles, each covering a different clause:

| Clause | Oracle | Lane |
| --- | --- | --- |
| step reference | deep-equal against `StepDocs()` (T020) | hermetic, `make ci` |
| validation findings | deep-equal against the built-in-only result (T020b) | hermetic, `make ci` |
| run output bytes | `TestGoldenHermeticStdout` (`mentat_golden_test.go:70`) | hermetic, **already in `make ci`** |

```sh
go test ./internal/steps/... .
```

There is no stdout rendering of an *engine* step reference to check: the only stdout
renderer (`cmd/mentat/steps_cmd.go:66`) reads built-in `steps.StepDocs()`, never an
engine. A compiled binary cannot see a consumer's registrations — that is structural.

Additionally run the live-harness golden, which pins the *same* stdout transform against
real Tempo and which `make ci` does **not** compile (the trap 012 hit):

```sh
go test -tags e2e ./e2e/...   # needs make harness-up
```

### 6. Malformed phrases still rejected at composition — SC-006, FR-008

V1–V5 stay in `internal/steps` and keep their messages. Every existing rejection case —
uncompilable, unanchored, duplicate, built-in collision, missing documentation field —
must still fail at composition, before any SUT is driven, with the **same text**.

Freezing changes when phrases are read, never what makes them invalid.

### 7. Coverage floor — SC-007

```sh
make cover
```

or the `/coverage` skill. Every touched package stays ≥80%. Watch `internal/engine` and
`internal/steps`.

---

## Mutation rehearsal (required)

House convention from 011 and 012: a guard is only proven when it has been seen RED.

For each of scenarios 1–4:

1. Revert the freeze (restore per-call invocation, or drop the return-side `slices.Clone`).
2. **Assert the edit actually applied** before trusting the result.
3. Confirm RED, and confirm the failure message names the real problem.
4. Restore; confirm green, including under `-race`.

Step 2 is not ceremony. 012 recorded a rehearsal that initially failed to go red because
the *mutation had not applied* — and noted that "the mutation didn't fire" and "the guard
is real" are indistinguishable from test output alone. Record each rehearsal in the test
file itself, as 011 and 012 did.

---

## What you cannot conclude from a green run

- **That no comparator anywhere is stateful.** These tests prove Mentat no longer
  *amplifies* statefulness into cross-surface drift. They say nothing about what a given
  comparator does internally.
- **That a shallow copy will stay sufficient.** It is sufficient because every field of
  `ContributedPhrase` and `PhraseBinding` is a `string` today. Add a slice, map or
  pointer field and FR-002/FR-004 break with no test failing (research R7 — the
  highest-value cheap guard in the feature).
- **That the e2e lane is green.** `make ci` does not compile it.
