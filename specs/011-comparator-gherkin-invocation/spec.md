# Feature Specification: Custom-Comparator Gherkin Invocation

**Feature Branch**: `011-comparator-gherkin-invocation`

**Created**: 2026-09-10

**Status**: Planned — spec, plan, research, contracts and tasks complete; implementation not
started. Analyzed 2026-09-10 (`/speckit-analyze`); FR-017, FR-018 and SC-011 added from its
findings.

**Input**: Deferred from 009 (`specs/009-extension-surface-integrity/spec.md:143`) as
"custom-comparator Gherkin invocation (011)". Scope narrowed to Option A by decision
D1 below; Option B deferred to 012.

---

## Problem

A user can register a custom comparator today — `mentat.WithComparator("revenue-shape", f)` —
and it is genuinely wired into the engine. But there is **no way to invoke it from a
`.feature` file.** Every `Then` step that runs a comparator hard-codes both the comparator
name *and* the concrete expectation type at compile time. So a registered custom comparator
is reachable only by a Go caller that never exists: `mentat.Run` drives Gherkin, and Gherkin
cannot name it.

The consequence is that the extension seam most likely to be used — "assert something about
my agent that Mentat's built-ins don't cover" — is the one seam whose output can't reach the
test author. `WithComparator` is, in practice, write-only.

### Verified current state (at `1206a56`)

Read this before assuming the gap is bigger than it is. Three of the four things this
feature would seem to need already exist.

1. **Name resolution is done.** `internal/steps/steps.go:258` calls
   `w.eng.Compare(w.ctx, w.target, name, w.ev, exp, sensitive)` with `name` as a plain
   string, resolved through the per-engine registry. A comparator registered as
   `"revenue-shape"` is already resolvable — nothing ever passes that name.

2. **Instance resolution is done.** `Engine.Comparator(name) (core.Comparator, bool)`
   exists (`internal/engine/engine.go:200`), and `world.eng` is a concrete
   `*engine.Engine` (`internal/steps/steps.go:34`). A step handler can already fetch the
   comparator instance itself. **No new engine plumbing is required by this feature.**

3. **Text → typed expectation is not a new idea.** Six comparators already build their
   expectation from feature-file text, one hard-coded path per step:

   | Comparator | Example call site | Text source |
   |---|---|---|
   | `cel` | `steps.go:555` `w.check("cel", comparator.CELExpectation{Expr: expr})` | regex capture |
   | `cel` | `steps.go:562` `{Expr: doc.Content}` | docstring |
   | `result` | `steps.go:498` `{Matcher: "regex", Want: re}` | regex capture |
   | `sequence` | `steps.go:320` `{Order: order}` | table/capture |
   | `retries` | `steps.go:333` `{Tool: name, Max: n}` | regex captures |
   | `budgets` | `steps.go:337` `{MaxTokens: &n}` | regex capture |

   So **`type Expectation = any` (`internal/core/core.go:99`) is not the blocker.** The
   type assertion inside each comparator works fine. The blocker is that *which* concrete
   expectation type to construct is chosen at compile time by the step author, and there is
   no way for a comparator to make that choice for itself at run time.

4. **What is genuinely missing** is therefore narrow: a Gherkin phrase that carries a
   comparator *name*, and a defined way for that named comparator to turn the accompanying
   text into its own expectation type.

### The real design constraint

`internal/steps/metadata.go` holds `stepDefs`, a **compile-time** table that is the single
source of truth for three consumers: godog registration (`registerSteps`), the `mentat steps`
CLI, and `docs/steps.md`. `metadata_test.go` and `docs_test.go` fail loudly if any of the
three diverge.

That table's compile-time nature — not `Expectation = any` — is what shapes this feature.
A design that lets comparators contribute their own Gherkin phrases must make registration a
*superset* of the table, which inverts the invariant all three consumers rest on. A design
that adds one generic row does not. That is the fork D1 settles.

---

## Decisions

### D1 — one generic step row plus an optional parser seam (Option A); phrase contribution deferred to 012

**Decision.** This feature adds **exactly one** row to `stepDefs`, whose pattern captures a
comparator name and takes a docstring as the raw expectation text, plus one optional
interface a comparator may implement to parse that text into its own expectation type.

```gherkin
Then the "revenue-shape" comparator is satisfied by:
  """
  { "min": 4, "currency": "USD" }
  """
```

**The alternative (Option B), recorded deliberately.** Comparators contribute their *own*
Gherkin phrases at registration, giving markedly better syntax:

```gherkin
Then the revenue is shaped like a quarterly report
```

B is deferred to **012** because it requires runtime step registration, which inverts the
`stepDefs` single-source-of-truth invariant: registration becomes a superset of the table,
so the drift test, the `docs/steps.md` generator and `mentat steps` all need redesign. It
also needs a pattern-collision policy for two comparators contributing overlapping regexes
(godog reports an ambiguous match; Constitution IV requires a loud failure, never last-wins).

**B is a superset of A, not a competing design.** A contributed step under B must still turn
its regex captures into a typed expectation — that is the same parser seam A introduces.
Nothing built in 011 is discarded when 012 lands; 012 builds on it. This relationship is
recorded here so 012's spec can start from it rather than re-deriving it.

**Renumbering.** CLI/`mentatctl` UX, listed as 012 at
`specs/009-extension-surface-integrity/spec.md:143`, moves to **013**. Recorded explicitly
here the way 009 recorded its own renumbering, so the roadmap line and the spec directories
never silently disagree.

### D2 — `ParseExpectation` receives the text and nothing else

```go
// package core
type ExpectationParser interface {
	ParseExpectation(text string) (Expectation, error)
}
```

**Decision.** The method takes the docstring content only — no `context.Context`, no target
name, no `Evidence`.

**Rationale.** Parsing declarative text into a typed value is a pure function, and keeping it
pure makes it trivially testable off the I/O path (composition rule: IO at the edges; a unit
knows its input type, its output type, and nothing about who calls it). A comparator that
needs run context already has it at `Compare` time, where `Evidence` is the designated
channel. Widening the parser signature would give comparators a second, weaker route to run
data and blur Constitution I (Evidence-only comparators).

**Placement.** Declared in `internal/core`, aliased on the facade as
`mentat.ExpectationParser`. It cannot be declared at the facade: `internal/steps` consumes it
and root imports `internal/steps`, which is 010's D5 rule ("only terminal types may be
facade-declared"). `TestFacadeNameabilitySweep` (`surface_test.go:1172`) will demand the alias
automatically, since the type appears in a published seam's method set — 010 paying for itself
without anyone having to remember.

### D3 — the step is always completeness-sensitive; no sensitivity seam yet

**Decision.** The new step routes through `checkSensitive`, not `check`. A custom comparator's
verdict is treated as falsifiable by a span exported after the ingestion window, and the engine
attaches the completeness qualifier when — and only when — the target's contract is bounded
(request-scoped, non-strict).

**Rationale.** The step cannot know whether an arbitrary comparator is completeness-sensitive,
and the two errors are not symmetric. Over-qualifying adds a visible, conservative caveat to a
verdict that did not need one. Under-qualifying produces an **unsound green** — exactly the
property 008 exists to protect. Defaulting to the sound side is the only defensible choice, and
because the engine suppresses the qualifier for strict targets, the noise is bounded.

**The rejected alternative.** An optional `CompletenessSensitive() bool` interface letting a
comparator opt out is the "correct" long-term shape, and it is deliberately **not** added here:
it would ship with **zero** implementations, since every built-in passes the flag as a literal
at its own call site. That is speculative generality, which the composition rule forbids ("an
abstraction needs a second implementation that exists now"). It becomes justified the first
time a real custom comparator needs to opt out, and is recorded in Out of Scope for that
moment.

### D4 — aggregate comparators are out of scope, because they cannot be registered at all

**Decision.** No parallel step row for `Engine.Aggregate` (`steps.go:582`). The `@runs(n)`
multi-run path keeps its hard-coded `aggregate-cel` binding.

**Rationale — this is a capability gap, not a symmetry choice.** `Registry` has
`RegisterAggregateComparator` (`registry.go:136`) and `Engine` has `AggregateComparator(name)`
(`engine.go:205`), but the facade exposes **no `WithAggregateComparator` option**: `run.go`
publishes `WithDriver`, `WithStore`, `WithComparator`, `WithJudge` and `WithReporter`, and
nothing else. An external module therefore cannot register a custom aggregate comparator in the
first place, so a Gherkin phrase naming one could only ever reach built-ins — a step with no
users.

This is the same missing-symbol defect class 010 recorded for `AggregateComparator` and
deliberately did not fix. Closing it means publishing the registration option, the factory
type and the aggregate seam together, which is its own feature. Named in Out of Scope as a
candidate spec rather than absorbed here.

### D5 — every failure path is loud and names the thing that failed

**Decision.** Three distinct failure modes, three distinct errors, no fallbacks
(Constitution IV):

| Situation | Required behaviour |
|---|---|
| Named comparator is not registered | Error naming the unknown name **and listing the registered comparator names**, mirroring 010's `WithReports` unknown-name behaviour. |
| Named comparator is registered but does not implement `ExpectationParser` | Error naming the comparator and stating that it cannot be driven from Gherkin because it does not parse expectations. Never a nil or zero-value expectation. |
| `ParseExpectation` returns an error | Surfaced wrapped with `%w`, naming the comparator, so the test author sees which comparator rejected their docstring and why. |

The registered-names listing needs an `Engine.Comparators() []string` accessor. `Registry`
already has `Comparators()` (`registry.go:125`); `Engine` exposes `Reporters()`
(`engine.go:218`) but not the comparator equivalent. Adding it mirrors an existing accessor
exactly.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Invoke a registered custom comparator from a feature file (Priority: P1)

An extension author has written a comparator that asserts something Mentat's built-ins do not
cover — say, that the agent's output matches their internal revenue-report shape. They register
it with `mentat.WithComparator("revenue-shape", newRevenueShape)`, implement
`ParseExpectation` on it, and then write a `.feature` file that names it directly. The scenario
runs, the comparator receives its own typed expectation, and the verdict lands in the report
next to the built-in ones.

**Why this priority**: This is the entire feature. Without it, `WithComparator` remains
write-only from Gherkin.

**Independent Test**: Register a comparator in a test binary, run a feature file naming it,
assert the scenario passes and the verdict appears in the collected results. Fully testable
with the hermetic in-memory store and no network.

**Acceptance Scenarios**:

1. **Given** a comparator registered as `"revenue-shape"` that implements `ExpectationParser`,
   **When** a feature file contains `Then the "revenue-shape" comparator is satisfied by:` with
   a docstring, **Then** the comparator receives the expectation its own `ParseExpectation`
   produced from that docstring, and the scenario passes when the comparator returns a passing
   verdict.
2. **Given** the same registration, **When** the comparator returns a failing verdict,
   **Then** the scenario fails and the failure message carries the comparator's own reasons.
3. **Given** the same registration, **When** the scenario completes, **Then** the verdict's
   qualifiers and judge usage are recorded on the world exactly as they are for built-in
   comparator steps.

---

### User Story 2 - Every failure names what went wrong (Priority: P2)

A test author mistypes a comparator name, or points the step at a comparator that was never
taught to parse expectations. They get an error that says which name was unknown and what
*was* registered, or which comparator lacks the capability — not a nil dereference, a silent
skip, or "invalid input".

**Why this priority**: This is the difference between an extension seam people can use and one
they abandon. It is also Constitution IV applied directly, and it is cheap once US1 exists.

**Independent Test**: Table-driven test over the three D5 failure modes, asserting on the
error text — that it contains the offending name, and for the unknown-name case, the
registered names.

**Acceptance Scenarios**:

1. **Given** no comparator registered as `"typo-name"`, **When** a step names it, **Then** the
   step fails with an error naming `"typo-name"` and listing the registered comparator names.
2. **Given** a comparator registered as `"plain"` that does not implement `ExpectationParser`,
   **When** a step names it, **Then** the step fails with an error naming `"plain"` and stating
   it cannot be driven from Gherkin.
3. **Given** a comparator whose `ParseExpectation` rejects the docstring, **When** a step
   names it, **Then** the step fails with an error naming the comparator and wrapping the
   parser's own error.

---

### User Story 3 - The new step is a first-class documented step (Priority: P3)

The new phrase appears in `mentat steps` and in `docs/steps.md` alongside every built-in step,
with a summary and a working example, because it went into the same metadata table as
everything else.

**Why this priority**: The documentation and the drift tests come for free if the row is added
correctly, and are silently broken if it is not. Low effort, and it is the check that D1's
"one row, nothing else changes" claim is actually true.

**Independent Test**: Run the existing `metadata_test.go` and `docs_test.go` drift tests, plus
`mentat steps`, and confirm the new row appears with non-blank group, summary and example, and
that the committed `docs/steps.md` matches.

**Acceptance Scenarios**:

1. **Given** the new row in `stepDefs`, **When** the drift tests run, **Then** registration,
   `StepDocs()` and the committed `docs/steps.md` all agree.
2. **Given** the new row, **When** `mentat steps` runs, **Then** the new phrase is listed under
   a group with a non-blank summary and a valid example.

---

### User Story 4 - Mentat proves it goes red on a bad custom comparator (Priority: P4)

The L3 meta-test drives a scenario whose custom comparator *should* fail, and asserts that
Mentat reports it as a failure. A test framework that cannot prove it fails correctly is not
a test framework.

**Why this priority**: Mandatory by the constitution (Principle V) and by
`CLAUDE.md`'s testing rules. Sequenced last only because it needs US1 and US2 in place.

**Independent Test**: A meta-scenario in the existing L3 suite registering a deliberately
failing custom comparator, asserting a non-zero exit and a failing scenario in the report.

**Acceptance Scenarios**:

1. **Given** a registered custom comparator that always returns a failing verdict, **When**
   the L3 meta-suite runs a feature naming it, **Then** Mentat reports the scenario as failed
   and exits non-zero.
2. **Given** a registered custom comparator whose `ParseExpectation` errors, **When** the L3
   meta-suite runs, **Then** the run fails loudly rather than skipping the assertion.

---

### Edge Cases

- **Comparator name contains a quote or regex metacharacter.** ~~The pattern captures
  `"([^"]+)"`, so an embedded quote truncates the name and yields an unknown-name error rather
  than a confusing match. Acceptable, but the unknown-name error must show the captured name
  verbatim so the truncation is visible.~~

  > **Corrected 2026-09-10 during implementation (T014).** There is no truncation. The
  > pattern is anchored at both ends and `([^"]+)` cannot cross a quote, so
  > `the "rev"enue" comparator is satisfied by:` matches **nothing** — the step is
  > UNDEFINED, not mis-captured. Verified against the compiled pattern and pinned by
  > `TestCustomComparatorPatternRejectsEmbeddedQuote`.
  >
  > The real consequence is worse than truncation would have been, and is **not fixed
  > here**: godog is non-strict by default and `run.go:411` sets no `Strict`, so an
  > undefined step is reported and the run still **exits 0**. A mistyped `Then` step
  > passes silently. That is pre-existing and applies to all 40 rows equally, so
  > widening 011 to fix it would blur what this branch's diff is accountable for — but
  > it is a live Constitution IV gap and deserves its own spec.
  >
  > Verbatim echo is still required and still implemented, because it earns its keep on
  > the cases that DO reach the handler: a trailing space or a homoglyph in the name is
  > invisible without it. `%q` in the unknown-name error makes it visible.
- **Empty docstring.** `ParseExpectation("")` is the comparator's decision, not the step's.
  The step passes it through; a comparator that requires content returns its own error, which
  D5 wraps. The step must not pre-validate emptiness — that would be the step guessing on the
  comparator's behalf.
- **Step used inside a `@runs(n>1)` scenario.** The existing `checkExp` guard already rejects
  single-run steps in multi-run scenarios with a message pointing at "the runs satisfy"
  (`steps.go:256`). The new step inherits that guard by routing through `checkExp`, and must
  not bypass it.
- **A custom comparator registered under a name that collides with a built-in.** Already
  handled at registration by the sealed registry's collision discipline; this feature must not
  introduce a second, weaker path. The step resolves whatever the registry holds.
- **A comparator implements `ExpectationParser` but returns a type its own `Compare` does not
  accept.** That is the comparator's bug, surfaced as its own type-assertion error at
  `Compare` time. The step does not police the round trip.
- **Docstring containing the `"""` delimiter or trailing whitespace.** Godog's own docstring
  parsing governs; the step receives `doc.Content` as given and must not trim or normalize it,
  since whitespace may be significant to the comparator's own format.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The framework MUST publish an optional `ExpectationParser` interface with the
  single method `ParseExpectation(text string) (Expectation, error)`, declared in
  `internal/core` and aliased on the facade as `mentat.ExpectationParser`.
- **FR-002**: The framework MUST NOT change the published `Comparator` interface.
  `ExpectationParser` is a separate interface, discovered by type assertion, so every existing
  comparator continues to compile and run unchanged.
- **FR-003**: `stepDefs` MUST gain **exactly one** new row, whose pattern captures a comparator
  name and accepts a docstring argument.
- **FR-004**: The new step's handler MUST resolve the captured name through the existing
  per-engine registry via `Engine.Comparator(name)`, introducing no new resolution path.
- **FR-005**: The handler MUST type-assert the resolved comparator to `ExpectationParser` and
  call `ParseExpectation` with the docstring content, unmodified.
- **FR-006**: The handler MUST route the resulting expectation through the existing
  `checkExp` path, so the multi-run guard, qualifier recording and judge-ledger accounting
  behave identically to built-in comparator steps.
- **FR-007**: When the named comparator is not registered, the step MUST fail with an error
  naming the captured name verbatim and listing the registered comparator names.
- **FR-008**: When the named comparator does not implement `ExpectationParser`, the step MUST
  fail with an error naming the comparator and stating it cannot be driven from Gherkin.
- **FR-009**: When `ParseExpectation` returns an error, the step MUST surface it wrapped with
  `%w` and named by comparator.
- **FR-010**: The step MUST be treated as completeness-sensitive, routing through
  `checkSensitive` semantics (D3).
- **FR-011**: `Engine` MUST expose a `Comparators() []string` accessor to satisfy FR-007,
  mirroring the existing `Reporters()` accessor.
- **FR-012**: The `stepDefs` drift invariant MUST hold unchanged: registration, `StepDocs()`,
  `mentat steps` and the committed `docs/steps.md` all agree, with `metadata_test.go` and
  `docs_test.go` untouched in their premises.
- **FR-013**: `docs/steps.md` MUST be regenerated so the committed reference includes the new
  step.
- **FR-014**: `docs/extending/comparator.md` MUST document the new phrase and the
  `ExpectationParser` seam, including a complete worked example of a custom comparator that
  implements it and the feature-file snippet that drives it.
- **FR-015**: The suite MUST prove the new step goes **red** on a failing custom comparator
  and on a parser error, not merely green on a passing one. The proof lands in
  `internal/steps` as an **in-process godog suite**, following `TestFeatureGoesRedOnBadScenario`
  (`internal/steps/steps_test.go:103`) and its four siblings.

  *Placement corrected 2026-09-10 by [research R6](./research.md).* This requirement
  originally assumed the proof belonged in the `e2e/` L3 suite. It cannot: `e2e/main_test.go:29`
  builds `mentatBin` from `./cmd/mentat` and drives that prebuilt binary, so a comparator
  registered in Go via `WithComparator` is structurally unreachable there. The in-process
  vehicle is also strictly better — it is hermetic, and it runs under `make ci`, whereas the
  `e2e/` placement would have hidden this feature's red-on-bad proof behind `//go:build e2e`,
  which `make ci` never compiles. That is the same hole that left the e2e package unbuildable
  for six commits during 010. The obligation is unchanged; only its location moved.
- **FR-016**: `CHANGELOG.md` MUST record the new step and the new interface as additive
  changes, with no breaking-change entry, since nothing existing changes shape.
- **FR-017**: The handler MUST reject a **nil** docstring with a descriptive error naming the
  step, before touching `doc.Content`. Seven of the eight existing docstring handlers already
  do this (`steps.go:169, 434, 475, 511, 548, 559, 570`), four with dedicated tests.
  Dereferencing a nil docstring panics, and Constitution IV forbids `panic` in library code.
  This is distinct from FR-005's pass-through: an **empty** docstring is content the comparator
  judges; a **nil** one is a malformed step Mentat rejects itself.
  *(Added 2026-09-10 — gap found by `/speckit-analyze`; see [research R2](./research.md).)*
- **FR-018**: SC-001 MUST be proven through the **facade**, not through internal packages. At
  least one test or example MUST reach the new step via `mentat.WithComparator` — the path an
  external module actually uses — rather than only via `engine.WithExtraComparator`, which no
  external module can call. A compile-only witness does not satisfy this.
  *(Added 2026-09-10 — gap found by `/speckit-analyze`: every planned test used internal
  packages, leaving the feature's headline claim unverified.)*

### Key Entities

- **`ExpectationParser`**: An optional capability a comparator may declare. Turns declarative
  feature-file text into the comparator's own expectation type. Pure — it receives text and
  returns a value or an error, and knows nothing about the run, the target, or the caller.
- **Expectation text**: The docstring body of the new step, passed through verbatim. Its format
  is entirely the comparator's business; Mentat neither parses nor validates it.
- **Comparator name**: The registry key, captured from the Gherkin phrase. Already the unit of
  resolution everywhere else in the engine; this feature adds no new namespace.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An external module can drive a comparator it wrote from a `.feature` file
  importing only `github.com/thetonymaster/mentat`, with no changes to Mentat.
- **SC-002**: The number of `stepDefs` rows added by this feature is exactly **1**.
- **SC-003**: `metadata_test.go` and `docs_test.go` pass with their assertions unmodified —
  the drift invariant is preserved, not relaxed.
- **SC-004**: All three D5 failure modes produce an error containing the offending comparator
  name; the unknown-name error additionally contains at least one registered name.
- **SC-005**: Zero changes to the published `Comparator` interface; every existing comparator
  compiles untouched.
- **SC-006**: `TestFacadeNameabilitySweep` passes with `ExpectationParser` on the facade, and
  fails if the alias is removed — proving 010's gate covers the new seam without anyone
  adding a special case.
- **SC-007**: The L3 meta-suite contains at least one scenario that fails *because* a custom
  comparator failed, and the suite proves it.
- **SC-008**: `examples/kafkaecho` continues to compile untouched.
- **SC-009**: Every touched package stays at or above the 80% coverage floor.
- **SC-010**: `make ci` is green, and `go vet -tags e2e ./...` compiles the e2e package.
- **SC-011**: A custom comparator driven from Gherkin against a **bounded** (request-scoped,
  non-strict) target carries the completeness qualifier on its verdict, and against a strict
  target does not — proving FR-010's `sensitive=true` actually reaches `Engine.Compare` rather
  than being an untested literal that could be flipped with every gate staying green.
  *(Added 2026-09-10 — gap found by `/speckit-analyze`.)*

---

## Assumptions

- **Docstring, not table or inline string, is the expectation carrier.** A docstring is the
  only Gherkin argument that carries arbitrary multi-line text without imposing structure,
  and `steps.go:562` already establishes the precedent for a comparator consuming one. A
  table would force Mentat to impose a shape on the comparator's private format.
- **The comparator, not Mentat, owns the expectation format.** Mentat never inspects the text.
  This keeps the seam narrow and means adding a comparator never requires a Mentat change.
- **Sensitivity defaults to the sound side** (D3) and the resulting extra qualifier on
  insensitive custom comparators against bounded targets is acceptable noise, because an
  unqualified unsound green is not.
- **One phrase is enough for v1.** The generic phrasing is deliberately plain rather than
  natural, because Option B (012) is where good syntax gets solved.
- **No new registration mechanism.** Comparators are registered exactly as they are today, via
  `WithComparator`. This feature adds an invocation path, nothing else.

---

## Dependencies

- **010 (seam-type nameability), merged `1206a56`.** Supplies the D5 rule that decides where
  `ExpectationParser` is declared, and the widened nameability sweep that enforces its facade
  alias automatically (SC-006).
- **009 (extension-surface integrity).** Supplies the public-surface golden gate that will
  record the new interface and its method set.
- **007 (public extension API).** Supplies `WithComparator` and the per-engine sealed registry
  this feature resolves through.
- **008 (trace completeness).** Supplies the completeness qualifier machinery D3 relies on.
- **The `stepDefs` metadata table and its drift tests** (`internal/steps/metadata.go`,
  `metadata_test.go`, `docs_test.go`) — a hard constraint, not a dependency to modify.

---

## Out of Scope

- **Option B — comparator-contributed Gherkin phrases.** Deferred to **012**, with the full
  rationale and the A-is-a-subset-of-B relationship recorded in D1. CLI/`mentatctl` UX moves
  to **013**.
- **Custom aggregate comparators.** No `WithAggregateComparator` facade option exists, so
  there is nothing to invoke (D4). Publishing the aggregate registration path — the option,
  the factory type and the seam — is a candidate for its own spec, in the same
  missing-symbol class 010 recorded for `AggregateComparator`.
- **A `CompletenessSensitive` opt-out interface.** Justified only when a real custom
  comparator needs it; would ship with zero implementations today (D3).
- **Changing `type Expectation = any`.** Verified not to be the blocker. Replacing it with a
  constrained type or a generic is a separate, larger question about the comparator seam.
- **Custom matchers, drivers, stores or judges reached from Gherkin.** Same class of gap,
  different seams; this feature deliberately does one.
- **Any change to the six built-in comparators' existing steps.** They keep their hard-coded
  expectation construction; nothing about their behaviour or phrasing changes.
