# Feature Specification: Comparator-Contributed Gherkin Phrases

**Feature Branch**: `012-comparator-gherkin-phrases`

**Created**: 2026-09-10

**Status**: Draft — complete, no open clarifications. All three questions resolved 2026-09-10
(D6–D8); godog `v0.15.1` behaviour verified from source the same day, which **refuted** 011's
D1 claim about ambiguous-match reporting and reshaped D8. Ready for `/speckit-plan`.

**Input**: Deferred from 011 by decision D1
(`specs/011-comparator-gherkin-invocation/spec.md:82-116`) as "Option B — comparator-contributed
Gherkin phrases". The 009 roadmap line
(`specs/009-extension-surface-integrity/spec.md:143`) was renumbered to match on 2026-09-10:
012 is this feature, CLI/`mentatctl` UX moves to 013.

---

## Problem

011 gave a registered custom comparator exactly one way into a feature file:

```gherkin
Then the "revenue-shape" comparator is satisfied by:
  """
  {"min": 4, "currency": "USD"}
  """
```

This works, and it was the right first step. But the sentence names an implementation
artifact — a registry key — and hands it a payload. The one thing Gherkin exists to
produce is an executable sentence a domain expert can read, and that is precisely what a
generic row cannot deliver. What the author wants to write is:

```gherkin
Then the revenue is shaped like a quarterly report
```

The comparator knows its own domain language. Only the comparator can supply that sentence.

### Verified current state (at `0f9dcea`)

`stepDefs` (`internal/steps/metadata.go:82`) is the single source of truth for step
registration. Read this section before sizing the feature: 011's D1 named **three**
consumers of that invariant. There are **five**, and the two it did not name are the ones
that shape this work.

1. **Registration.** `registerSteps` (`metadata.go:73`) iterates the table and calls
   `reg.Step(pattern, handler)`. It is the sole path, and
   `TestNoDirectStepRegistration` (`metadata_test.go:129`) keeps it sole by failing if
   `steps.go` ever calls `sc.Step(` directly.

2. **The drift gate.** `TestStepMetadataMatchesRegistration` (`metadata_test.go:56`)
   asserts **bidirectional set equality plus count equality** between what registration
   emits and what the table holds — "registered pattern has no metadata entry" and
   "metadata entry is never registered" are both failures. This is the exact assertion
   Option B inverts.

3. **The docs mirror.** `StepDocs()` (`metadata.go:48`) is the handler-free view;
   `TestStepDocsMirrorsTable` (`docs_test.go:12`) proves it lossless and
   `TestStepDocsGroupsAreContiguous` (`docs_test.go:43`) proves every group forms one
   contiguous block, which the markdown generator depends on to emit one heading per group.

4. **`mentat steps` and `docs/steps.md`.** `renderStepsMarkdown` and `renderStepsText`
   (`cmd/mentat/steps_cmd.go:56,84`) both iterate `steps.StepDocs()`; the `go:generate`
   directive (`steps_cmd.go:3`) writes the committed `docs/steps.md`, and a regeneration
   test asserts byte-identity.

5. **The step-binding precheck — not named in 011's D1.** `compiledStepPatterns()`
   (`internal/steps/precheck.go:84`) compiles every `StepDocs()` pattern, and
   `StepBindingFindings` (`precheck.go:95`) reports any pickle step matching none of them as
   an `unbound-step` finding. It has two consumers: the scenario-init prechecks, and
   `mentat validate` (`cmd/mentat/validate.go:185`).

Two consequences fall out of #5 that 011's D1 did not record. Together they matter more
than the drift test does.

**(a) The pattern cache is package-level mutable state.** `compiledStepPatterns` guards
`stepPatterns` with a package-level `sync.Once` (`precheck.go:76-91`). The first engine to
trigger compilation fixes the pattern set for the entire process. Once phrases vary per
engine, a second `mentat.Run` in the same process — a different comparator set, a different
phrase set — would be checked against the first run's patterns. This is the same defect
class 007 closed for the registry by making it per-engine and sealed
(`internal/registry/registry.go:30-35`), and the composition rule names it directly: no
package-level mutable state.

**(b) `mentat validate` structurally cannot see a contributed phrase.** It does not build
an engine. It constructs a `checker` by hand (`validate.go:117`) with a `cel` and an
`aggregate-cel` comparator and nothing else, deliberately — no registry, no store, no
driver. A Go consumer's `WithComparator` calls live in *their* module and cannot reach a
compiled `mentat` binary. So today's behaviour, applied unchanged to a suite that uses
contributed phrases, is one `unbound-step` finding per phrase: a **false red on a valid
feature file**, emitted by the one command whose entire job is to certify that a suite is
well-formed.

The honest scope of Option B is therefore not "invert one drift test". It is: make step
registration per-engine and runtime-resolved, then re-establish each of five consumers'
guarantees over a set that is no longer knowable at compile time — and decide what the two
CLI commands that cannot see a consumer's engine are permitted to say.

### Verified godog behaviour (`v0.15.1`, the pinned version)

Two properties of godog decide more of this feature's shape than anything in Mentat does.
Both were read from the module source on 2026-09-10; neither was previously recorded, and
the first **refutes** an assumption 011's D1 inherited.

**(c) Ambiguity detection is gated on `Strict`, and Mentat's run path is not strict.**

```go
// suite.go:547-553
if s.strict {
    if len(matchingExpressions) > 1 {
        return nil, fmt.Errorf("%w, step text: %s\n    matches:%s", ErrAmbiguous, text, errs)
    }
}
return first, nil
```

`mentat.Run` builds its godog options at `run.go:411-419` with `Format`, `Paths`, `Output`,
`DefaultContext`, `Concurrency`, `Tags` and `StopOnFailure` — and **no `Strict`**. The only
`Strict: true` in the module is `internal/ctl/replay.go:36`, added by 011 for a different
reason. So on the primary run path `s.strict` is false, the ambiguity branch never executes,
and godog silently returns the **first-registered** match.

011's D1 asserted "godog reports an ambiguous match; Constitution IV requires a loud failure,
never last-wins". That is true only under `Strict`. Unqualified, it is false for the path that
matters — and the failure is not last-wins but **first-wins**, which is worse here: built-in
steps register from `stepDefs` before any contributed phrase, so a contributed phrase that
collides with a built-in is **silently shadowed**. The author's sentence never runs, the
built-in runs in its place against whatever captures happen to line up, and the scenario goes
green. A test framework reporting a passing assertion nobody wrote is the exact failure class
Constitution IV and the L3 gate exist to prevent.

This is the same shape as the four corrections 011 made to its own artifacts: an inherited
claim about godog that held only under a condition nobody restated.

**(d) godog cannot accept a `[]string` or variadic handler, and silently drops extra captures.**

`reflect.Slice` accepts `[]byte` and nothing else (`internal/models/stepdef.go:222-233`);
`[]string` and any variadic parameter fall through to the unsupported-type default. And the
arity check is one-sided (`stepdef.go:58`):

```go
if len(sd.Args) < numIn { return ctx, ErrUnmatchedStepArgumentNumber }
```

Too *few* arguments is an error; too *many* are silently ignored, because the conversion loop
runs `i < numIn`. A two-parameter handler bound to a three-capture pattern discards the third
with no diagnostic.

A contributed pattern's capture count is not known until runtime, so Mentat MUST synthesize
each phrase's handler — `reflect.FuncOf` over N `string` parameters returning `error`, with N
taken from the compiled pattern's `NumSubexp()`. This is a small amount of reflection in the
registration path, and it *closes* the dropped-capture hazard by construction rather than
inheriting it: Mentat derives the arity from the same regex godog matches against, so the two
cannot disagree.

Crucially, this constrains only the **bridge**, not the seam. Because Mentat synthesizes the
handler, the interface it offers a comparator may take any shape it likes — a `[]string`
included. godog's restriction is invisible to extension authors.

### 011's D2 seam does not carry over unchanged

011 declared `ExpectationParser` (`internal/core/core.go:130`):

```go
type ExpectationParser interface {
	ParseExpectation(text string) (Expectation, error)
}
```

D1 recorded B as a strict superset of A on the grounds that "a contributed step under B must
still turn its regex captures into a typed expectation — that is the same parser seam A
introduces." That claim is true about the seam's **role** and not about its **signature**.

A contributed phrase carries its information in regex captures, and a pattern may have zero,
one, or many:

```gherkin
Then the revenue is shaped like a quarterly report        # 0 captures
Then the revenue is shaped like a "quarterly" report      # 1 capture
Then revenue between "4" and "9" USD is reported          # 3 captures
```

`ParseExpectation(text string)` serves exactly the one-capture case. Serving the other two
requires either a second parse entry point that receives the capture list, or joining
captures into a single string — which is lossy, because a capture may itself contain the
delimiter, and it forces the comparator to re-split what Mentat just concatenated for no
reason. Settled by **D6**: a sibling seam, leaving 011's interface untouched.

---

## Decisions

### D1 — 011's generic row survives verbatim; B is an additional path, not a replacement

The `Extend`-group row (`metadata.go:367`) and `comparatorSatisfiedByDoc`
(`internal/steps/steps.go:615`) stay exactly as they are. A comparator with a private,
structured expectation format is well served by a docstring and badly served by a regex, so
the generic path keeps real users after 012 lands. This also keeps every existing feature
file green with no edit, and makes the feature additive at the gate level.

### D2 — only comparators may contribute phrases

Not matchers, drivers, stores, judges or reporters. Aggregate comparators are excluded for
011's D4 reason, unchanged and re-verified: the facade publishes `WithDriver`, `WithStore`,
`WithComparator`, `WithJudge` and `WithReporter` (`run.go:200-231`) and **no
`WithAggregateComparator`**, so an external module cannot register one, and a phrase naming
one could only ever reach built-ins. Doing one seam properly is the whole shape of this
feature.

### D3 — the contributed phrase set is per-engine, never package-global

`InitializerWithCollector(eng, col)` (`steps.go:83`) already receives the engine, so the
registration closure can resolve the contributed set at suite-init time for that engine
specifically. Everything downstream must follow: the step-binding pattern set becomes a
value derived from an engine, and the `sync.Once` cache at `precheck.go:76` is deleted
rather than adapted. Two engines in one process must be unable to observe each other's
phrases — the property 007 established for registries and this feature must not re-open.

### D4 — the single-source-of-truth invariant is re-expressed, not weakened

The drift gate does not get relaxed to "registration is a superset of the table". It becomes
a partition: everything registered is *either* a built-in row from `stepDefs` *or* a phrase
some named comparator contributed to this engine, and an unaccounted pattern is still a hard
failure. The gate loses no strength; it gains a second accounted source.

### D5 — every contributed phrase carries the documentation the table demands

`TestStepMetadataFieldsPresent` (`metadata_test.go:95`) requires a non-empty group, summary
and example on every row. Contributed phrases meet the same bar, checked at registration
rather than by a test, and a blank field is a loud rejection. 009 and 010 spent two features
establishing that the public surface is documented or the gate fails; a phrase an author can
type but cannot look up would give that back.

### D6 — captures reach the comparator through a sibling seam; 011's interface is untouched

**Decision.** Add a second, optional interface that receives the phrase's captures. 011's
`ExpectationParser` (`internal/core/core.go:130`) keeps its exact signature and its exact
role: the docstring path. A phrase whose pattern declares capture groups routes to the new
seam; a phrase with no capture groups that carries a docstring routes to 011's. A phrase
with **both** passes the docstring as a final argument to the captures seam, so exactly one
seam serves any given phrase and the choice is a property of the pattern, not a runtime
guess.

**Rationale.** Widening 011's interface would break a public seam shipped three commits ago
(`ec4efbc`) for zero capability gain — Mentat synthesizes the godog bridge either way (finding
(d)), so nothing about the comparator-facing signature is forced. Joining captures into one
string is lossy at the delimiter and makes `[]` and `[""]` indistinguishable, which is
Constitution IV's silent-corruption shape.

The two seams are also not redundant: they correspond to two genuine authoring modes — a
structured private payload, and a readable sentence. Collapsing them into one signature where
most calls pass a nil docstring would hide that distinction rather than serve it.

### D7 — validation happens through a library entry point that takes a built engine

**Decision.** Publish a validate entry point on the facade that accepts the same registration
options `mentat.Run` does, so a consumer validates their suite against an engine that actually
contains their comparators. The `mentat` binary's `validate` keeps its current strictness for
built-in steps and **documents** that contributed phrases are out of its reach.

**Rationale.** `validate` builds a hand-made `checker` (`cmd/mentat/validate.go:117`) with no
registry and no engine, and a consumer's `WithComparator` calls live in another module. No
flag closes that gap; only running the consumer's code does.

**The rejected alternative, recorded because it is the intuitive one.** Having the consumer
emit a manifest of their phrases for the binary to read looks like it preserves a single
command. It does not pay: *generating* the manifest already requires running the consumer's Go
code, so the standalone-lint benefit only materialises if the file is committed — at which
point it can drift from the engine that produced it, and closing that drift means rebuilding
the `stepDefs` drift-test machinery for a second artifact. A feature whose purpose is
protecting a single-source-of-truth invariant should not open with a second source.

**The path that stays available.** If a standalone lint job is later wanted, the manifest
becomes a *generated* artifact rendered by this entry point and guarded by a byte-identity
regeneration test — the same `go:generate` + golden pattern `docs/steps.md` already uses
(`cmd/mentat/steps_cmd.go:3`). The engine stays authoritative and the drift gate is
structural. Out of scope for 012; recorded so it is a layer, never a fork.

### D8 — collisions are caught by enabling `Strict` and by build-time checks, not by heuristics

**Decision.** Three mechanisms, in order of when they fire:

| When | Mechanism |
|---|---|
| Engine build | Exact duplicate pattern, or a pattern identical to a built-in's, is a hard error naming both contributors. |
| Engine build | A contributed pattern not anchored `^…$` is rejected, naming the contributor and the pattern. |
| Step match | `Strict: true` on the run path, so godog's own matcher raises `ErrAmbiguous` naming every matching expression. |

**Rationale.** Delegating overlap detection to godog was the cheap option and finding (c)
refutes it: the check is gated on `Strict`, which `mentat.Run` does not set, so today two
colliding patterns resolve to first-wins in silence. Enabling `Strict` turns godog's existing,
*exact* matcher into the detector — no heuristic, no false collisions — and `ErrAmbiguous`
reaches the After hook as a step error, so it lands in the collector as a FAILED scenario by
the same path 011 established for undefined steps.

Anchoring is required because all 40 built-in patterns are already `^…$` and an unanchored
contributed pattern is the realistic way to swallow one. Together the two build-time checks
catch the authoring mistakes before a suite runs, and `Strict` catches genuine overlap between
two well-formed anchored patterns precisely.

**Rejected: cross-checking each contributed pattern against every registered example.** A
bounded, decidable heuristic, and unnecessary once `Strict` provides exact detection. It can
also false-collide when two examples legitimately overlap, which would block a valid phrase.

**The risk this carries, stated so planning does not discover it.** `Strict: true` also makes
godog treat undefined and pending steps as suite failures. Mentat has no pending steps and
already treats undefined steps as failures through the collector, so the expected golden churn
is zero — but that is a prediction, not a result. It MUST be proven against the committed
goldens **including the `//go:build e2e` stdout goldens, which `make ci` does not compile**, so
a green `make ci` is not evidence here. Enabling `Strict` and running the e2e goldens is an
explicit task with a recorded before/after, not an assumption.

**Bonus, worth stating: this fixes a live defect.** First-wins shadowing is reachable today
between two built-in patterns; enabling `Strict` closes it for built-ins as well as for
contributed phrases.

### D9 — Mentat synthesizes each phrase's godog handler and derives its arity from the pattern

**Decision.** The handler bound for a contributed phrase is built with `reflect.MakeFunc` over
`reflect.FuncOf(N × string, error)`, where N is the compiled pattern's `NumSubexp()`.

**Rationale.** godog's arity check is one-sided (finding (d)): too few arguments error, too
many are silently discarded. Deriving N from the same compiled regex godog matches against
makes the two structurally incapable of disagreeing, converting a silent-drop hazard into an
impossibility. Reflection is confined to this one bridge; nothing above it sees godog's
signature rules, which is what lets D6 choose the comparator-facing seam freely.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Write a scenario in the comparator's own language (Priority: P1)

An extension author has a `revenue-shape` comparator. Instead of naming it and feeding it a
JSON docstring, they declare the phrase their domain experts actually say, register the
comparator as they do today, and write a feature file containing that sentence and nothing
about Mentat. The scenario runs, the comparator receives its own typed expectation built
from the phrase's captures, and the verdict lands in the report exactly as a built-in's does.

**Why this priority**: It is the feature. Without it there is nothing else to test.

**Independent Test**: Register one comparator that contributes one phrase, run a feature file
written only in that phrase, and assert the resulting verdict equals the verdict the
equivalent 011-style generic step produces for the same expectation.

**Acceptance Scenarios**:

1. **Given** a comparator registered with one contributed phrase carrying two captures,
   **When** a scenario uses that phrase, **Then** the comparator's parse entry point receives
   both captures and the run reports the comparator's verdict.
2. **Given** the same comparator and a scenario that violates its assertion, **When** the
   suite runs, **Then** the scenario is reported FAILED with the comparator's own reason text.
3. **Given** a comparator that contributes no phrases, **When** the suite runs, **Then**
   nothing about its behaviour or its 011 generic-step reachability changes.
4. **Given** a contributed phrase whose parse entry point returns an error, **When** the
   scenario runs, **Then** the error surfaces wrapped and names the comparator, never a
   passing or nil-expectation verdict.

---

### User Story 2 - Two engines in one process stay isolated (Priority: P1)

A consumer runs two suites in the same process against two differently-composed engines —
sequentially or concurrently — with different custom comparators and therefore different
contributed phrases. Each suite sees exactly its own engine's phrases.

**Why this priority**: P1 alongside US1, not below it. This is the property that decides
whether the feature is sound or is a latent cross-run contamination bug. The package-level
`sync.Once` at `precheck.go:76` means the naive implementation is wrong by default, and
`mentat_run_reentrancy_test.go` exists because this exact class already bit once.

**Independent Test**: Build two engines with disjoint contributed phrase sets, run both in
one process, and assert a phrase from engine A is unbound under engine B and vice versa —
in both orders, to prove no first-writer-wins cache remains.

**Acceptance Scenarios**:

1. **Given** engines A and B with disjoint contributed phrases, **When** A runs first and
   then B, **Then** B's suite binds only B's phrases.
2. **Given** the same two engines, **When** the order is reversed, **Then** the result is
   symmetric — proving the outcome does not depend on which ran first.
3. **Given** a phrase belonging to engine A, **When** a scenario under engine B uses it,
   **Then** it is reported unbound with the same wording any unknown step gets.

---

### User Story 3 - A colliding phrase fails loudly at composition, not at match time (Priority: P2)

Two comparators contribute patterns that can match the same sentence — or one collides with a
built-in. The author is told, by name, which two contributors collided and on what, before
any scenario runs.

**Why this priority**: P2 because it is a guardrail rather than the capability, but it is
non-negotiable in kind: Constitution IV forbids last-wins resolution. The scope of what is
detection is layered per **D8**: exact-duplicate and anchoring checks at engine build, and
godog's own strict matcher for genuine overlap between two well-formed patterns.

**Independent Test**: Register two comparators contributing the same pattern; assert the
engine build fails with an error naming both, and that no scenario executes.

**Acceptance Scenarios**:

1. **Given** two comparators contributing an identical pattern, **When** the engine is built,
   **Then** it fails with an error naming both contributors and the pattern.
2. **Given** a contributed pattern identical to a built-in's, **When** the engine is built,
   **Then** it fails with an error naming the contributor and the built-in step.
3. **Given** a contributed pattern that is not a compilable regular expression, **When** the
   engine is built, **Then** it fails naming the contributor, the pattern and the compile
   error.
4. **Given** a contributed phrase with a blank group, summary or example, **When** the engine
   is built, **Then** it fails naming the contributor and the missing field (D5).
5. **Given** a contributed pattern not anchored `^…$`, **When** the engine is built, **Then**
   it fails naming the contributor and the pattern (FR-007a).
6. **Given** two well-formed anchored patterns that both match one step's text, **When** the
   scenario runs, **Then** it is reported FAILED naming every matching expression — never
   resolved silently to the first-registered one (FR-007b).
7. **Given** `0f9dcea`'s non-strict configuration, **When** a scenario whose text matches two
   patterns runs, **Then** it is reported PASSED against the first-registered match — the live
   defect. Scenario 6's test MUST be observed failing against this configuration before the fix
   lands, so it is a proven regression test and not only a new-feature test (SC-011).

---

### User Story 4 - The step reference and the validator tell the truth (Priority: P2)

A consumer can render the complete step reference for *their* engine — built-ins plus their
own phrases — and the static validator does not report their valid feature files as broken.

**Why this priority**: P2 because a suite still runs correctly without it, but it is what
keeps the feature from degrading two existing commands. `mentat validate`'s treatment of
contributed phrases is settled by **D7**: a library entry point taking a built engine, with the
binary documenting what it cannot see.

**Independent Test**: Build an engine with contributed phrases, render its reference, and
assert it contains both the built-in rows and the contributed ones with their documentation.
Separately, run the step-binding precheck over a suite using contributed phrases and assert
zero `unbound-step` findings on the engine-aware path.

**Acceptance Scenarios**:

1. **Given** an engine with two contributed phrases, **When** its step reference is rendered,
   **Then** it contains every built-in row plus both contributed phrases with their group,
   summary and example.
2. **Given** a suite written in contributed phrases, **When** the engine-aware step-binding
   precheck runs, **Then** it reports no `unbound-step` findings.
3. **Given** the `mentat` binary with no consumer registrations, **When** `mentat steps` runs,
   **Then** its output is byte-identical to today's for the built-in rows, and the generated
   `docs/steps.md` states that contributed phrases are engine-scoped and not listed here.

---

### Edge Cases

- A contributed pattern that is unanchored, so it matches far more sentences than its author
  intended — including built-in steps. Anchoring is **required** and an unanchored pattern is
  rejected at engine build (D8, FR-007a).
- A contributed phrase that takes both regex captures *and* a docstring.
- A contributed phrase with zero captures and no docstring — a pure sentence, where the
  expectation is a constant the comparator supplies itself.
- The same comparator instance registered under two names, each contributing the same phrase:
  a collision by identity rather than by authoring mistake.
- A contributed phrase declaring a group name equal to a built-in group ("Shape", "Result"),
  which breaks the contiguity invariant `TestStepDocsGroupsAreContiguous` (`docs_test.go:43`)
  depends on and would emit a duplicated heading.
- A phrase contributed after the registry is sealed (`internal/registry/registry.go:71-79`,
  where `register` panics on a sealed registry).
- A contributed pattern matching a step in a feature file that this engine's tag expression
  will never select.
- A comparator that contributes phrases but implements no parse entry point at all.
- Zero contributed phrases anywhere — the overwhelmingly common case, which must cost nothing
  and change no existing output byte.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A comparator MUST be able to declare one or more Gherkin phrases that invoke it,
  supplied through the existing `WithComparator` registration path without a second
  registration mechanism.
- **FR-002**: Declared phrases MUST be registered with the godog suite for the engine that
  resolved them, and MUST NOT be observable from any other engine built in the same process
  (D3).
- **FR-003**: A contributed phrase's regex captures MUST reach the comparator as its own typed
  expectation value, through a seam separate from 011's `ExpectationParser`, accepting a
  pattern with zero, one, or many captures without lossy encoding (D6). A phrase declaring both
  captures and a docstring MUST deliver the docstring as a final argument to that seam, so
  exactly one seam serves any given phrase.
- **FR-004**: 011's `ExpectationParser` seam, its generic `stepDefs` row, and every feature
  file using it MUST keep working with no change to their behaviour or phrasing (D1).
- **FR-005**: The step-registration drift gate MUST continue to fail when a built-in
  registration and its `stepDefs` row diverge, and MUST additionally fail when any registered
  pattern is neither a built-in row nor an accounted contributed phrase for that engine (D4).
  Registration MUST NOT become an unchecked superset of the table.
- **FR-006**: Every contributed phrase MUST carry a non-empty group, summary and example, and a
  blank field MUST be a loud rejection naming the contributor and the field (D5).
- **FR-007**: Two contributors declaring an identical pattern, or a contributor declaring a
  pattern identical to a built-in's, MUST fail the engine build with an error naming both
  contributors and the pattern, before any scenario executes (D8).
- **FR-007a**: A contributed pattern that is not anchored `^…$` MUST be rejected at engine
  build, naming the contributor and the pattern (D8).
- **FR-007b**: The run path MUST enable godog's strict mode so that two well-formed patterns
  matching one step raise an ambiguity error naming every matching expression, and that error
  MUST reach the report as a failed scenario. Silent first-wins resolution — today's behaviour
  at `suite.go:547-553` — is forbidden (D8, finding (c)).
- **FR-007c**: Enabling strict mode MUST NOT change any committed golden. Verification MUST
  include the `//go:build e2e` stdout goldens, which `make ci` does not compile; a green
  `make ci` alone is insufficient evidence (D8).
- **FR-008**: A contributed pattern that is not a compilable regular expression MUST fail
  loudly, naming the contributor, the pattern and the compile error.
- **FR-009**: The step-binding precheck MUST evaluate a feature file against the pattern set of
  the engine that will run it, contributed phrases included, so a valid scenario written in a
  contributed phrase produces no `unbound-step` finding on the engine-aware path.
- **FR-010**: The package-level `sync.Once` pattern cache (`internal/steps/precheck.go:76-91`)
  MUST be removed rather than adapted; no process-wide mutable step state may survive this
  feature.
- **FR-011**: A validate entry point MUST be published on the facade, accepting the same
  registration options `mentat.Run` accepts, so a consumer validates their suite against an
  engine containing their own comparators and their contributed phrases are checked strictly
  (D7).
- **FR-011a**: The `mentat` binary's `validate` MUST keep its current strictness for built-in
  steps, and its documentation MUST state that contributed phrases are outside what a compiled
  binary can see. It MUST NOT gain a manifest flag or any second source of phrase truth (D7).
- **FR-019**: The godog handler for a contributed phrase MUST be synthesized with an arity
  derived from the compiled pattern's capture count, so godog's one-sided arity check
  (`internal/models/stepdef.go:58`) can never silently discard a capture (D9).
- **FR-012**: A consumer MUST be able to obtain the complete step reference for their own
  engine — built-in rows plus their contributed phrases, each with its documentation fields —
  through the public surface.
- **FR-013**: `mentat steps` and the generated `docs/steps.md` MUST continue to render the
  built-in rows byte-identically, and the generated page MUST state explicitly that contributed
  phrases are engine-scoped and therefore not listed there.
- **FR-014**: The docs-mirror and group-contiguity guarantees (`docs_test.go`) MUST hold over
  whatever rendering path serves an engine's full reference, not only over the built-in table.
- **FR-015**: Any new public interface or type introduced by this feature MUST be reachable by
  name from the facade and MUST appear in the public-surface golden with its full method set.
- **FR-016**: An L3 meta-test MUST prove Mentat goes RED on a contributed phrase whose
  assertion is violated — a green suite for a false claim is the failure this gate exists to
  catch (Constitution V).
- **FR-017**: `docs/extending/` MUST gain the contributed-phrase authoring path, and the seam
  taxonomy (`specs/009-.../contracts/seam-taxonomy.md`) MUST be reconciled if this feature
  changes any seam's shape.
- **FR-018**: Every touched package MUST stay at or above the 80% coverage floor.

### Key Entities

- **Contributed phrase**: a pattern, its documentation fields (group, summary, example), and
  the comparator name it invokes. Declared by the comparator, resolved per engine.
- **Engine step set**: the union of the built-in `stepDefs` rows and the contributed phrases of
  one built engine. It is a value derived from an engine, never a package-level singleton — and
  it is what registration, the drift gate, the step-binding precheck, and the reference renderer
  all consume.
- **Collision**: two entries in one engine step set whose patterns can match the same sentence.
  Exact duplicates and unanchored patterns are caught at engine build; genuine overlap between
  two well-formed anchored patterns is caught by godog's strict matcher at step-match time (D8).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A feature file written entirely in a comparator's contributed phrases — naming no
  comparator and carrying no payload docstring — runs and produces a verdict identical (pass/fail
  and reason text) to the equivalent 011-style generic step for the same expectation.
- **SC-002**: Two engines with disjoint contributed phrase sets, run in one process in both
  orders, each bind only their own phrases; a phrase from one is unbound under the other in
  every ordering tested.
- **SC-003**: Every build-time rejection path (FR-006, FR-007, FR-007a, FR-008) produces an
  error naming the contributor and the offending value, and no scenario executes; each is proven
  by a mutation rehearsal recorded in the test file showing the gate red and then green again.
- **SC-004**: The drift gate still goes red when a built-in registration and its table row
  diverge, and goes red on an unaccounted registered pattern; both rehearsals recorded.
- **SC-005**: A suite written in contributed phrases yields zero `unbound-step` findings from
  the engine-aware validate entry point, and the `mentat` binary reports no finding it cannot
  substantiate. No valid feature file is reported broken by either path.
- **SC-006**: `mentat steps` output and the committed `docs/steps.md` change only by
  deliberate, PR-called-out diffs; the built-in rows are byte-identical.
- **SC-007**: A scenario using a contributed phrase whose assertion is false makes the run RED
  and the reported reason is the comparator's own (L3, FR-016).
- **SC-008**: The public-surface golden records every new type with its full method set, and the
  facade nameability sweep demands the alias without anyone having to remember it.
- **SC-009**: A build with zero contributed phrases produces byte-identical output to `0f9dcea`
  across the existing golden tests, proving the feature costs nothing when unused.
- **SC-010**: Every touched package remains at or above 80% coverage.
- **SC-011**: Two patterns matching one step produce a failed scenario naming every matching
  expression, on the `mentat.Run` path — proven by a test that fails against `0f9dcea`'s
  non-strict configuration and passes after. This is the live first-wins-shadowing defect
  (finding (c)), so the proof is a regression test, not only a new-feature test.
- **SC-012**: Enabling strict mode leaves every committed golden byte-identical, evidenced by a
  recorded before/after that includes the `//go:build e2e` stdout goldens. A green `make ci` is
  explicitly not accepted as evidence for this criterion.
- **SC-013**: A contributed pattern with N capture groups delivers exactly N captures to the
  comparator for every N exercised (0, 1, and ≥3), with no capture silently discarded.

## Assumptions

- File and line evidence in this spec was read at `0f9dcea` on 2026-09-10; planning re-verifies
  every reference before relying on it.
- **godog's ambiguity and arity behaviour is verified, not assumed** — read from
  `godog@v0.15.1` source on 2026-09-10 and recorded as findings (c) and (d). The verification
  **refuted** 011's D1 claim that godog reports an ambiguous match: it does so only under
  `Strict`, which `mentat.Run` does not set. Planning inherits the findings, not the D1
  sentence. It should still re-read these files if the godog version moves, since both are
  properties of a pinned dependency rather than of Mentat.
- **The godog version stays pinned at `v0.15.1` for this feature.** A bump would need findings
  (c) and (d) re-established, since D8 and D9 rest on both.
- 011's D3 stands: a step invoking a custom comparator is always completeness-sensitive. The
  `CompletenessSensitive` opt-out remains deferred until a real comparator needs it; contributing
  a phrase is not by itself that need.
- Contributed phrases are `Then` steps. `Given`/`When` contribution — a comparator cannot drive
  anything — is not in question here.
- The overwhelming majority of engines contribute zero phrases, so the design is judged partly
  on costing nothing in that case (SC-009).
- TDD routing per the repo constitution: behaviour changes through go-test-writer; the
  docs/generator and taxonomy work through go-coder.

## Dependencies

- **011 (custom-comparator Gherkin invocation), merged `ec4efbc`.** Supplies `ExpectationParser`,
  the generic `Extend` row, and `Engine.Comparators()`. B builds on all three (D1).
- **010 (seam-type nameability), merged `1206a56`.** Supplies the "only terminal types may be
  facade-declared" placement rule and the nameability sweep that enforces SC-008 automatically.
- **009 (extension-surface integrity), merged `fc6455e`.** Supplies the public-surface golden
  and the seam taxonomy this feature must keep consistent.
- **007 (public extension API).** Supplies the per-engine sealed registry — the pattern D3
  extends from seams to steps, and the reentrancy property US2 must not re-open.
- **The five `stepDefs` consumers** enumerated under *Verified current state*. These are hard
  constraints to satisfy, not code to redesign at will.

## Out of Scope

- **CLI / `mentatctl` UX — 013.** Renumbered from 012 by 011's D1 and corrected in the 009
  roadmap line on 2026-09-10.
- **Custom aggregate comparators.** No `WithAggregateComparator` facade option exists, so there
  is nothing to contribute a phrase for (D2, inheriting 011's D4). Publishing the aggregate
  registration path is its own feature.
- **Phrase contribution from any other seam** — matchers, drivers, stores, judges, reporters.
- **A `CompletenessSensitive` opt-out interface.** Still zero implementations; still deferred.
- **Changing `type Expectation = any`.** Verified in 011's research not to be the blocker here
  either.
- **Any change to the six built-in comparators' existing steps** or to 011's generic row.
- **`Given` / `When` phrase contribution.**
- **A committed phrase manifest for standalone linting.** Deferred, not rejected: if the need
  appears it becomes a *generated* artifact rendered by D7's entry point and guarded by a
  byte-identity regeneration test, so the engine stays the only source of truth (D7).

---

## Resolved Questions

All three questions this spec opened with are settled; each is recorded as a decision above.
Kept here as a short record of what was chosen and why, so 013 does not re-derive it.

| # | Question | Resolution | Decision |
|---|---|---|---|
| Q1 | How do a phrase's captures reach the comparator? | Sibling seam; 011's `ExpectationParser` untouched | D6 |
| Q2 | What does `mentat validate` do with a phrase it cannot see? | Library entry point taking a built engine; no manifest, no second source of truth | D7 |
| Q3 | How much collision detection must Mentat do itself? | Exact-duplicate + anchoring checks at build, plus `Strict: true` so godog's own matcher detects overlap | D8 |

Q3's answer changed as a result of verification. The originally-favoured option — delegate
overlap detection to godog — was refuted by finding (c): the check is gated on `Strict`, which
`mentat.Run` does not set, so today two colliding patterns resolve first-wins in silence.
Enabling `Strict` converts godog's existing exact matcher into the detector and closes a live
defect at the same time (SC-011).
