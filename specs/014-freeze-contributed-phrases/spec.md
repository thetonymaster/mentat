# Feature Specification: Freeze Contributed Phrases During Engine Composition

**Feature Branch**: `014-freeze-contributed-phrases` (created 2026-09-11 from `main` at `f6bb402`; matches the spec directory name)

**Created**: 2026-09-11

**Note on numbering**: `specs/` jumps 012 → 014. That is intentional. **013 is built-in
step-pattern disjointness**, opened by 012's convergence and living on branch
`013-builtin-pattern-disjointness`; its spec directory has not been created yet. See the
roadmap block in `CLAUDE.md`, which is authoritative.

**Status**: Draft

**Input**: User description: "Freeze contributed phrases during engine composition. ContributedPhrases invokes comparator code every time a downstream surface resolves phrases. A stateful contributor can therefore produce documentation and validation for one pattern, then register a different pattern when Run initializes. This violates the engine-scoped sealed phrase-set contract and contradicts the API documentation that post-build mutations have no effect. Capture one immutable binding snapshot during engine construction. Return copies of that snapshot from ContributedPhrases. Validate and register the same snapshot."

## Context: the gap between the contract and the code

Feature 012 published an optional seam that lets a comparator declare the Gherkin
sentences that invoke it. Three artifacts state the same contract about when that seam
is consulted:

| Where | What it says |
| --- | --- |
| `mentat.go:66` (public facade doc) | "Phrases are resolved **once per engine build** and are scoped to…" |
| `internal/core/core.go:168` (seam doc) | "It is consulted **ONCE PER ENGINE BUILD**, never per scenario. The returned slice is treated as immutable; a comparator that mutates it afterwards **has no effect** on the built engine…" |
| `specs/012-comparator-gherkin-phrases/contracts/phrase-seam.md:24` | "Called **once per engine build**, never per scenario." |

The implementation does not do this. `Engine.ContributedPhrases`
(`internal/engine/engine.go:241`) re-invokes every contributing comparator's
`ContributedPhrases()` method on **each call**, and three independent production
surfaces each call it through `resolvePhrases`:

| Surface | Path | What it decides |
| --- | --- | --- |
| `mentat.Run` | `InitializerWithBudget` → `internal/steps/steps.go:101` | Which patterns are **registered** — what actually executes |
| `mentat.Validate` | `EngineStepChecks` → `internal/steps/phrase.go:612` | What findings the author is **told about** |
| `mentat.StepReference` | `EngineStepDocs` → `internal/steps/phrase.go:519` | What the step reference **documents** |

Line numbers in that table point at the **`resolvePhrases(eng)` call inside each
function**, not the function declaration — that call is the thing this feature is about.
The declarations are one line earlier (`:290`, `:518`, `:611`).

So the phrase set is a fresh answer from comparator-owned code at each surface. A
contributor that is not a pure function of itself — a counter, a clock, a flag flipped
between calls, a slice it keeps and mutates — is validated on one vocabulary,
documented on a second, and executed on a third. Nothing in the system notices.

### D2 — the defect is LATENT, not live (measured 2026-09-11)

**This spec's first draft implied a live, user-facing bug. That is false, and the
correction matters more than the wording.**

Measured: each facade entry point **builds its own engine** and resolves phrases exactly
once against it.

| Entry point | Engine built at | Resolves via |
| --- | --- | --- |
| `mentat.Run` | `run.go:344` | `InitializerWithBudget` (`run.go:381`) |
| `mentat.Validate` | `buildEngineForInspection` → `run.go:682` | `EngineStepChecks` (`run.go:596`) |
| `mentat.StepReference` | `buildEngineForInspection` → `run.go:682` | `EngineStepDocs` (`run.go:544`) |

**No production path shares one engine across two phrase-resolving surfaces.** So today
every contributor is consulted exactly once per engine *by coincidence of there being one
surface per engine* — not because anything enforces it. A user calling `Validate` then
`Run` gets two engines, and a stateful contributor answering them differently is not this
defect; no freeze could fix that, and re-consulting a **new** engine's comparators is
correct behaviour.

**What is genuinely broken**, and why this is still worth building:

1. **The contract is unenforced.** `internal/core/core.go:168` promises "consulted ONCE
   PER ENGINE BUILD" and that post-build mutation "has no effect on the built engine".
   The code delivers "once per surface resolution". These coincide only while no engine
   serves two surfaces — an accident of current call sites, not a property.
2. **It is live the moment that accident ends** — a second surface, a shared engine, a
   consumer handed an engine handle, or any caching of `buildEngineForInspection`.
3. **It is observable today one layer down.** With one `*engine.Engine` driving all three
   surfaces, they disagree: documented `A`, validated `B`, registered `B`. That is
   SC-001 failing, and it is where the tests for this feature live.

**Precedent**: 012 shipped `Strict: true` for a defect it measured as "latent, not live"
on exactly this reasoning, and its roadmap entry records the correction of an earlier
overclaim ("evidence, not proof"). This entry follows that convention rather than leaving
the stronger first framing in place.

**Consequence for testing**: the cross-surface acceptance scenarios below are verified in
`internal/steps`, where one engine serves three surfaces. A facade-level version of the
same test **cannot go red** and must not be written — see tasks.md T008.

Two scoping facts, both measured against the merged code at `f6bb402`:

- **Within a single run the set is already consistent.** `InitializerWithBudget`
  resolves once and threads the result into both the argument checks and registration
  (`steps.go:101`, `:107`). The drift is *across* surfaces and *across* runs, not
  between scenarios of one run. This feature closes the former; the latter is already
  closed and must stay closed.
- **A per-engine snapshot is the fix; a package-level cache is not.** 012 deleted a
  package-level, first-writer-wins `sync.Once` phrase cache precisely because it broke
  per-engine isolation — engine A's phrases answering engine B. That regression is
  guarded by `TestContributedPhrasesAreScopedToTheirEngine`, which runs two engines in
  both construction orders. The existing precedent for doing this correctly is in the
  same codebase: `Engine.resolveOnce` is a **field on the Engine**, which
  `internal/steps/phrase.go:37` calls out as "per-engine, so it carries the isolation
  property rather than breaking it."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Validation describes the run that will actually happen (Priority: P1)

A suite author runs `mentat.Validate` against their engine, sees no findings, and
concludes their feature files are sound. They then run the suite. Today, if any
comparator in that engine declares its phrases from mutable state, the vocabulary
`Validate` inspected is not necessarily the vocabulary `Run` registers — so a clean
validation is not evidence about the run. The author's steps can come back unbound, or
bind to a different comparator than the one the validation report named.

**Why this priority**: `Validate` exists to be trusted before a run costs time and
tokens. A validator that inspects a different artifact than the one that executes is
not merely incomplete — it is actively misleading, which is worse than having no
validator at all. This is the reason the feature exists.

**Independent Test**: Build **one** engine containing a comparator that returns a
different pattern on every call to its phrase-declaration method, and drive all three
surfaces from that single engine. Assert they agree — the sentence documented is the
sentence validated is the sentence bound. Per **D2** this must be exercised at the
`internal/steps` layer; the facade builds a fresh engine per entry point and so cannot
observe the defect.

**Acceptance Scenarios**:

1. **Given** one engine whose comparator returns pattern A on its first phrase-declaration call and pattern B on every later call, **When** that engine's step reference, validation and suite registration are each resolved, **Then** all three report pattern A — rather than documenting A while registering B.
2. **Given** the same engine, **When** any number of surfaces resolve phrases in any order, **Then** every surface observes one identical phrase set.
3. **Given** an engine whose comparator declares a malformed phrase, **When** the author validates or runs, **Then** the defect is still reported before any system under test is driven, with the same message it produces today.

---

### User Story 2 - The step reference documents the phrases that bind (Priority: P2)

An extension author renders the engine's step reference to learn which sentences their
suite can use, and to publish that reference to their team. Today that document is
generated from its own independent interrogation of the comparators, so it can describe
a vocabulary the runner will not accept.

**Why this priority**: it is the same defect as US1 with a lower blast radius —
documentation that lies wastes an author's time, where validation that lies produces a
false verdict about the system under test. It rides along on the same fix and needs its
own test rather than its own mechanism.

**Independent Test**: With a phrase set that changes between calls, render the step
reference and register the suite; assert the documented patterns and the bound patterns
are the same set, in the same order.

**Acceptance Scenarios**:

1. **Given** an engine with a state-dependent contributor, **When** the author renders the step reference and then runs the suite, **Then** every documented contributed phrase is bound and every bound contributed phrase is documented.
2. **Given** an engine with no contributing comparators, **When** the author renders the step reference, **Then** it is deep-equal to the built-in-only reference produced today.

---

### User Story 3 - A returned phrase set cannot be used to reach back into the engine (Priority: P3)

A caller receives the engine's contributed phrases and keeps, sorts, truncates or
otherwise mutates the slice it was handed. That must not change what any later caller
observes.

**Why this priority**: without it the freeze is only half done — the engine would stop
re-asking the comparator but would still hand out a live reference to its own sealed
state, leaving a second route to the same class of drift. It is a small addition to the
same change, so it is bundled rather than deferred.

**Independent Test**: Resolve the phrase set twice; mutate the first result; assert the
second is unaffected and equals the original.

**Acceptance Scenarios**:

1. **Given** a built engine, **When** a caller mutates the slice returned to it, **Then** a later caller receives the original snapshot unchanged.
2. **Given** a built engine, **When** a comparator mutates the slice it returned during construction, **Then** the engine's answer is unchanged — which is what the seam documentation already promises.

---

### Edge Cases

- **A contributor that returns a different set on every call.** The primary case. The snapshot taken at construction is authoritative; every later answer is that snapshot.
- **A contributor that mutates the slice it already returned.** Covered by taking a copy at capture time, not only at return time — otherwise the engine's "snapshot" aliases memory the comparator still owns.
- **A contributor that returns `nil` or an empty slice.** Must remain indistinguishable from a comparator that does not implement the seam at all: nothing registered, nothing documented, no allocation on the common path.
- **A comparator that does not implement the seam.** Unchanged — discovery is by type assertion and non-implementers contribute nothing.
- **Two engines in one process.** Each must answer only with its own phrases, in both construction orders, including concurrently. This is an existing guaranteed property that this change must not regress.
- **A malformed phrase (uncompilable, unanchored, colliding, or missing documentation fields).** Still rejected at composition, before any system under test is driven. Freezing changes *when the phrases are read*, not *what makes them invalid*.
- **A contributor that panics or blocks when asked.** Out of scope; behaviour is whatever it is today, and is now confined to a single call during construction rather than repeated at every surface.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST invoke each contributing comparator's phrase-declaration seam exactly once per engine, during engine construction.
- **FR-002**: The system MUST capture the phrases returned by that single invocation as a snapshot owned by the engine, taking its own copy so that later mutation by the comparator cannot alter it.
- **FR-003**: Every request for an engine's contributed phrases MUST be answered from that snapshot, without invoking comparator code again.
- **FR-004**: Each answer MUST be a copy, such that a caller mutating what it received cannot affect the snapshot or any other caller's answer.
- **FR-005**: Validation, step-reference rendering, and suite registration MUST all operate on the same snapshot for a given engine.
- **FR-006**: The snapshot MUST preserve the existing deterministic order — sorted by contributing comparator name, preserving each comparator's own declaration order within its block — because that order decides which pattern wins a collision.
- **FR-007**: The snapshot MUST be scoped to a single engine. No phrase data may be shared across engines, and two engines constructed in one process MUST each observe only their own phrases, in either construction order and under concurrent use.
- **FR-008**: Existing rejection of malformed contributed phrases MUST continue to happen at composition, before any system under test is driven, with unchanged error messages.
- **FR-009**: An engine with no contributing comparators MUST behave exactly as it does today, including allocating and registering nothing on that path.
- **FR-010**: The published behaviour of the seam MUST NOT change for any comparator that already declares its phrases from immutable state — this is an alignment of code to its documented contract, not a contract change.
- **FR-011**: No package-level mutable state may be introduced to hold phrase data, preserving the audited property that anything varying by engine is threaded per engine.

### Key Entities

- **Contributed phrase**: one Gherkin sentence a comparator offers, carrying its pattern, its reference group, a one-line summary, and one valid example. All of its attributes are plain text; it holds no references to other structures.
- **Phrase binding**: one contributed phrase paired with the name of the comparator that declared it. The pairing is structural — a phrase is resolved through the comparator that offered it.
- **Phrase snapshot**: the ordered set of phrase bindings captured for one engine at construction. It is the engine's single authoritative answer to "which sentences do this engine's comparators contribute", for the whole life of the engine.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For **one engine** containing a comparator that returns a different phrase set on every call, validation, step-reference rendering, and suite registration yield identical vocabularies — verified by driving all three surfaces from that single engine. Per D2 this is measured in `internal/steps`; a facade-level equivalent is green before and after the fix and proves nothing.
- **SC-002**: Comparator phrase-declaration code is invoked exactly once per engine, measured by a counting contributor, regardless of how many surfaces resolve phrases or how many times each is called.
- **SC-003**: Mutating a returned phrase set leaves every later answer unchanged, and a comparator mutating the slice it returned during construction leaves the engine's answer unchanged.
- **SC-004**: Two engines with disjoint phrase vocabularies in one process each bind only their own phrases, in both construction orders and under concurrent execution with race detection enabled — the existing guarantee, re-verified.
- **SC-005**: An engine contributing no phrases produces a **deep-equal** step reference and **deep-equal** validation findings, and **byte-identical** run output, compared to before this change. (The first two are structured values, not bytes — `StepReference` returns step documents and `Validate` returns findings, so "byte-identical" was not a testable claim for them. Run output is genuinely byte-compared, by `TestGoldenHermeticStdout` in the hermetic lane and `e2e/golden_test.go` against the live harness.)
- **SC-006**: Every existing malformed-phrase rejection continues to fire at composition with an unchanged message, verified against the existing validation cases.
- **SC-007**: Every touched package remains at or above the 80% coverage floor, and the full gate passes.
- **SC-008**: The three prose artifacts stating the once-per-build contract need no correction, because the code now satisfies them as written. One **test** artifact does need correction — see D1.

## Decisions

### D1 — An existing test asserts the behaviour this feature removes, and it is inverted deliberately

`TestPhrasesAddedAfterResolutionDoNotAffectTheBuiltEngine`
(`internal/steps/phrase_test.go:820`) passes today and **will go red** under this feature.
It is the one place in the tree where the per-call contract is asserted rather than
merely implemented, and inverting it is a deliberate contract change recorded here — not
an implementer relaxing an inconvenient assertion.

The test is **internally contradictory**, which is what makes the direction obvious:

| Part | What it says | Under 014 |
| --- | --- | --- |
| Its name | "Phrases Added After Resolution **Do Not Affect** The Built Engine" | ✅ becomes *more* true |
| Its doc comment | "the resolved set is a snapshot"; a comparator mutating its list after build "has NO effect, and that is the correct outcome" | ✅ exactly this feature's thesis |
| First assertion (`before` stays 1) | an already-resolved slice does not grow | ✅ unchanged |
| **Final assertion (`after == 2`)** | "Re-resolving DOES see it — resolution is a function of the engine's comparators **at call time**" | ❌ **contradicts FR-003** |

So the header and the closing assertion already disagree with each other. This feature
resolves that tension in favour of the header, the test's own name, and the three prose
contracts — all four of which describe a snapshot.

**Resolution**: the final assertion becomes `after == 1`, and the comment explaining it is
rewritten to state that re-resolution returns the snapshot captured at build. The first
assertion and the test's name stay as they are.

**Why this is safe to change rather than a warning to heed**: the assertion encodes what
012 *built*, not what 012 *promised*. 012's own contract
(`contracts/phrase-seam.md:24`) says "called once per engine build", and its seam
documentation says post-build mutation "has no effect on the built engine". The assertion
is the odd one out among four statements of intent.

**This decision is why SC-008 distinguishes prose artifacts from test artifacts.** The
repo's standing rule is *do not edit a test to make an implementation pass*; this is the
narrow, recorded exception where the test encodes a contract the feature is chartered to
change.

## Assumptions

- **This is a defect fix, not a contract change.** The intended behaviour is already published in three places; the code is what disagrees. No public type, signature, or documented promise changes, so no consumer written against the documented contract is affected.
- **Comparators are already constructed by the time construction completes.** The registry holds built comparator values rather than deferred factories, so capturing the snapshot at construction does not change when any comparator factory runs.
- **A shallow copy is a complete copy.** A contributed phrase carries only plain text fields, and a binding adds only the contributor's name, so copying the slice fully isolates it. No deep-copy machinery is needed, and adding a reference-typed field to either structure in future would re-open this.
- **Phrase validity checks stay where they are.** The rules that reject a malformed phrase depend on the built-in step table and therefore live outside the engine; freezing changes only the input those rules read, not the rules or their location.
- **Intra-run consistency is already correct and is treated as a regression surface**, not as work: one run resolves once and shares the result across registration and argument checking.
- **Ordering is load-bearing, not cosmetic.** Registration order decides which pattern wins a collision, so the snapshot preserves order rather than treating the phrase set as unordered.
- **A stateful contributor is a realistic author error, not a hostile actor.** The motivating case is ordinary — a comparator that builds its phrase list from configuration it also mutates, or memoizes lazily — so the fix targets accidental drift. It is not a sandbox and does not defend against a comparator that deliberately misbehaves during construction.
