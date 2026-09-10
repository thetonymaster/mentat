# Feature Specification: Seam-Type Nameability

**Feature Branch**: `010-seam-type-nameability`

**Created**: 2026-09-09

**Status**: Ready for planning — both open questions resolved 2026-09-09 (see Decisions)

**Input**: User description: "Seam-type nameability gap (docs/extending/stability.md boundary 4, specs/009-extension-surface-integrity/contracts/facade-nameability.md:39-60) — the 009 nameability sweep only walks outward from Config/Results, not through seam-interface parameter/result types. Four types are frozen on the public-surface golden but can't be named from outside the module: RunReport, AggregateDetail, ExtractPolicy, HTTPSpec. Sharpest consequence: Reporter is an aliased seam interface an external module cannot implement, because it can't write its own method's parameter type (Report(rep RunReport, ...)). Also blocks a Comparator author from constructing Verdict.Detail and a Driver author from setting RunSpec.Extract/RunSpec.HTTP. Fix = widen the facade (alias those types) — deliberately deferred rather than done in 009 because widening the public surface is a one-way door."

## Problem

Mentat publishes six seams as part of its public surface — `Driver`, `TraceStore`,
`Comparator`, `Judge`, `Correlator`, `Reporter`. The promise attached to that surface
is that an outside module can implement any of them using the published facade alone.

For one seam that promise is currently false, and for three more the published data
types cannot be fully populated. Feature 009 verified the gap and deliberately deferred
the fix, because closing it widens the public surface — a one-way door under the
project's stability policy.

### Verified current state (at `f2afdda`)

Four types appear on the frozen public-surface golden but have no facade name, so no
external module can write them:

| Type | Frozen at | Declared in | Blocks |
|---|---|---|---|
| `RunReport` | `public-surface.golden:138` | `internal/core/core.go:334` | implementing `Reporter` at all |
| `AggregateDetail` | `public-surface.golden:115` | `internal/core/core.go:90` | setting `Verdict.Detail` |
| `ExtractPolicy` | `public-surface.golden:87` | `internal/core/core.go:292` | setting `RunSpec.Extract` |
| `HTTPSpec` | `public-surface.golden:83` | `internal/core/core.go:140` | setting `RunSpec.HTTP` |

### Two findings beyond the original report

Investigation while writing this spec surfaced two facts that materially change the
shape of the fix. Both are recorded here because they move work out of "mechanical
aliasing" and into "decision required".

**Finding A — nameability alone would not make `Reporter` usable.** There is no
`WithReporter` registration option. Options exist for `Driver`, `Store`, `Comparator`
and `Judge` (`run.go:189-210`); reporters are a post-run rendering concern emitted by
the CLI (`internal/engine/build.go:53`), and `WithReports` takes `map[string]string` — a
built-in format name mapped to an output path, with no slot for a caller-supplied
implementation.

This absence is **deliberate and documented**, not an oversight: `mentat.go:59-62`
records that `Reporter` is "exposed as a type because contracts reference it; it
deliberately has no registration hook yet, for the same reason as `Correlator`" — and
`Correlator`'s reason is "because no concrete external demand for one has appeared."

That rationale cuts both ways, which is what makes this a decision rather than a task.
If no concrete demand has appeared for *registering* a reporter, the same YAGNI argument
applies to *naming* its parameter type: making `RunReport` nameable would let an author
declare a `Reporter` that still cannot be plugged in — widening the surface through a
one-way door for no gained capability. Conversely, if demand has now appeared, the honest
fix is both halves, not one.

**Finding B — `RunReport` cannot be aliased the way the other three can.**
`RunReport.Scenarios` is `[]core.ScenarioResult`, and the facade already declares its
own, different `ScenarioResult` struct (`run.go:247`) — so the identifier is taken and
the two types have different field sets. That facade struct exists *deliberately*: its
doc comment states it is "a facade-owned struct (not an alias) so the internal report
record types (`RunRecord` etc.) never leak through the public surface"
(`run.go:243-246`). Exposing `RunReport` as-is therefore contradicts a standing feature-007
decision and drags `core.ScenarioResult` and `RunRecord` onto the frozen surface with it.

The other three types are closed: `AggregateDetail`, `HTTPSpec` and `ExtractPolicy`
reference only builtins and `*regexp.Regexp` (standard library), so naming them adds no
further internal type to the surface.

## Decisions

Both open questions were resolved on 2026-09-09. They are recorded here, not merely
folded into the requirements, because each extends or supersedes a previously recorded
decision — and a reversal should be as visible as the thing it reverses.

### D1 — `Reporter` is widened and made registrable *(resolves Finding A, closes FR-004)*

The seam stays published and gains a registration option. "Widen only" was rejected on
Finding A's own logic: a nameable seam with nowhere to plug in freezes a type for no
gained capability. "Narrow by removal" was rejected because rendering a suite's results
is the natural integration point for CI systems and dashboards — the concrete external
demand that `mentat.go:59-62` was waiting for is this feature.

This supersedes the "no concrete external demand for one has appeared" note at
`mentat.go:59-62` **for `Reporter` only**. `Correlator`'s identically-worded note stands
(see Out of Scope), and the `Reporter` half of that note must be rewritten rather than
left contradicting the code.

### D2 — the published seam renders `Results`, not `RunReport` *(resolves Finding B, closes FR-005)*

`RunReport` is **not** aliased and does not join the public surface. The published seam
becomes `Report(res Results, w io.Writer) error`, built on the facade-owned `Results`
that `Run` already returns (`run.go:217`, produced by `toResults` at `run.go:483`).

This dissolves Finding B rather than negotiating with it: `mentat.ScenarioResult` keeps
its identity and its meaning for existing `Run` callers, and `core.ScenarioResult` and
`RunRecord` stay off the surface — upholding the feature-007 decision at `run.go:243-246`
instead of reversing it.

### D3 — `Results` gains full reporting fidelity, so there is one seam and not two *(new)*

Verified while resolving D2: `Results` is **lossy** against `RunReport` beyond the suite
aggregates, so a seam built on it as it stands would hand an external author strictly
less than the built-in reporters receive.

| Carried by `core` | Absent from the facade | Rendered today by |
|---|---|---|
| `RunReport.Total` | `Results` | `junit.go:51`, `html.go:18-19` |
| `RunReport.StartedAt`, `.Duration` | `Results` | nothing yet; available to every internal reporter |
| `ScenarioResult.Runs []RunRecord` | facade carries `RunIDs []string` only | `html.go:30-31` (per-run table) |
| `ScenarioResult.Sequence` | facade `ScenarioResult` | `html.go:24` |
| `ScenarioResult.Qualifiers` | facade `ScenarioResult` | `html.go:27`, `junit.go:64-65` |
| `ScenarioResult.Aggregate` | facade `ScenarioResult` | `html.go:29` |
| `ScenarioResult.Tags` | facade `ScenarioResult` | nothing |

The facade therefore widens to carry all of it, so **one** `Reporter` interface serves
built-in and custom alike.

> **Mechanism superseded by D5.** As first written, this decision had per-run records arrive
> as a *facade-owned mirror struct, not an alias* — the device `ScenarioResult` already uses.
> That is an import cycle: a type internal packages consume cannot be declared at the facade.
> D5 collapses the mirrors into one type in `internal/result`, aliased on the facade. The
> conclusion of D3 — one seam, full fidelity — stands unchanged and is now satisfied by
> construction rather than by parity.

Rejected alternative: keep two interfaces (public on `Results`, internal on `RunReport`)
with an adapter between. Smaller diff, but it makes custom reporters permanently
second-class — a custom HTML reporter could never render the per-run table the built-in
one does — and guarantees the two seams drift.

### D4 — reporters move into the per-engine registry *(consequence of D1)*

Reporters are the only seam held **outside** the per-engine `Registry`:
`registry.go:190-193` keeps them in a package-global map under their own mutex,
documented as "never sealed", while every other seam lives on a `*Registry` instance
that `Build` seals. Instance-versus-factory is *not* the distinction — `RegisterDriver`
and `RegisterComparator` also take instances, while `RegisterJudge` and `RegisterStore`
take factories. Global-versus-per-engine is.

A `WithReporter` writing into that global map would re-open the registry reentrancy
defect that feature 007 (T010/T011) closed: two concurrent `Run`s registering different
reporters would collide, and no seal would catch it.

`WithReporter` is therefore a per-`Run` registration like `WithDriver`, and reporters
join the per-engine registry with the same collision and sealing discipline. This
follows from D1 rather than being independently chosen; it is called out because it
changes a seam's registration model, which the new-seam guide names as one of the three
formerly-tribal decisions. It also invalidates the rationale comment at
`registry.go:186-188`, which justifies the global by a call path that no longer exists.

### D5 — the result types live in `internal/result` and are aliased *(corrects D2/D3, recorded 2026-09-09)*

**D2 and D3 as first written were undeliverable.** They specify `Results`,
`ScenarioResult`, `RunRecord` and `Reporter` as facade-**declared** types in package
`mentat`. But `internal/report` must name `Results` to render it, `internal/registry` must
hold `Reporter` to resolve it, and `internal/engine` must take a `ReporterFactory` to wire
it — while root already imports all three (`run.go:11-17`). Every one of those is an import
cycle.

The reason the five working seams avoid this is that **every public type on the facade is
an alias** (`Driver = core.Driver` … `Config = config.Config`), so internal packages name
the internal type and never the facade. `Results` and `ScenarioResult` get away with being
facade-declared only because they are **terminal**: `toResults` produces them in the root
package and nothing internal consumes them. A seam is not terminal. Terminal types may be
facade-declared; consumed types may not.

**Decision**: the result types move to a new leaf package `internal/result` and are aliased
on the facade — and, since D3 already requires `Results` to carry everything `RunReport`
carries, the two collapse into **one** type rather than two that must be kept in parity.

```
internal/result   Results (was core.RunReport), ScenarioResult, RunRecord, Reporter
mentat.go         type Results = result.Results     …and the rest, as aliases
```

`internal/result` imports `internal/core` for `AggregateDetail` and `JudgeUsage`; `core`
imports nothing back, so there is no cycle. `Results.ExitCode()` moves with the type.

**What this changes about D2/D3**:

- The *mechanism* changes from "facade-owned mirror struct, not an alias" to "one type in
  an internal package, aliased". R4's wording is superseded.
- The *promise* is unchanged: `mentat.ScenarioResult` keeps its identity and gains fields;
  a caller still writes `mentat.X` for everything. What was internal is now deliberately
  public **with a justification**, which is the standing rule (SC-007) — and D3 had already
  committed to freezing every one of those fields on the surface anyway. The number of
  struct definitions changed; the surface promise did not.
- `RunIDs` survives on the collapsed type, tagged `json:"-"` and derived from `Runs`, so
  existing `Run` callers keep compiling and the wire format gains no key.

**The risk this removes**: with one struct there is nothing to keep in tag-and-field-order
parity. The marshalled struct is the same struct, relocated; Go does not serialize type
names, so **the emitted JSON is unchanged by construction**. FR-013's golden becomes a
plain regression guard rather than the parity guard the whole feature was balanced on.

**Alternatives considered**:
- *Two types — public in `internal/result`, internal records left in `core`.* Rejected: it
  fixes the cycle but keeps the mirrors, and with them the tag/order parity requirement and
  the fixture port — retaining the feature's largest risk to preserve the letter of a 007
  decision whose substance survives either way.
- *Invert the dependency so root no longer imports `internal/report`.* Rejected: emission
  would move back out of `Run` to the CLI, reverting the 007 recompose and making the
  `engine/build.go:53` comment true again — which is the comment FR-015 exists to correct.
  `internal/engine` imports `report` too, so that edge would need breaking as well.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Implement and register a custom Reporter (Priority: P1)

An extension author wants their suite results rendered into their own format — a
team dashboard payload, an internal CI annotation format. They depend on the Mentat
module, write a type satisfying the published `Reporter` seam, hand it to `Run`, and
get their renderer invoked with the run's results.

Today they cannot even declare the type: the method's parameter type has no name they
can write. And if they could, there would be nowhere to register it.

**Why this priority**: This is the only seam on the public surface that an external
module cannot implement at all, so it carries the most severe version of the defect. It
is also by far the largest slice — D1–D4 turn it into a seam re-shape, a registration-model
change, and a rewrite of all three built-in reporters, where P2 and P3 are three type
aliases. Sequencing should put it **after** P2/P3, which are independently shippable and
carry none of its risk; priority here reflects importance, not execution order.

**Independent Test**: An external-module test (facade imports only) declares a type
satisfying `Reporter`, registers it with a `Run`, executes a suite, and asserts the
renderer received the run's outcome. Delivers a genuinely pluggable reporting seam.

**Acceptance Scenarios**:

1. **Given** a module that imports only the Mentat facade, **When** it declares a type
   whose method set matches the published `Reporter` seam, **Then** the module compiles
   and the type satisfies `Reporter`.
2. **Given** such a reporter, **When** the author supplies it to a run through the
   published registration surface, **Then** the run invokes it with that run's results
   and the author's output is produced.
3. **Given** a reporter registered under a name already taken, **When** the run starts,
   **Then** it fails loudly with a collision error naming the conflicting name — matching
   the discipline of the existing registration options, not a silent last-wins.
4. **Given** a registered reporter that returns an error while rendering, **When** the
   run completes, **Then** the failure is surfaced with the reporter named, and the
   suite's own results are still returned.
5. **Given** a custom reporter, **When** it renders a run, **Then** every datum the
   built-in HTML reporter renders is reachable from its input — suite totals and timing,
   and per scenario the tags, sequence, qualifiers, aggregate detail and per-run records
   (D3).
6. **Given** the three built-in reporters re-expressed on the published seam, **When**
   they render any run, **Then** the emitted bytes are identical to those emitted before
   this feature — including field names, serialization tags and omission behaviour.
7. **Given** two runs executing concurrently, each registering a different reporter under
   the same name, **When** both complete, **Then** each run used its own reporter and
   neither observed the other's registration (D4).

---

### User Story 2 - Build a complete RunSpec from a custom Driver (Priority: P2)

An extension author writing a `Driver` for a SUT that Mentat does not ship an adapter
for needs to construct or forward a `RunSpec` with its answer-extraction policy and its
HTTP request configuration set. Both fields are published and frozen; neither can be
populated from outside the module.

**Why this priority**: It blocks a seam that authors *do* reach for first — the driver is
the usual reason to extend Mentat. Unlike P1 it needs no new decision: both types are
closed, so naming them adds nothing else to the surface.

**Independent Test**: An external-module compile test constructs a `RunSpec` with both
`Extract` and `HTTP` set to non-zero values, using facade names only. Compiling is the
proof.

**Acceptance Scenarios**:

1. **Given** a facade-only module, **When** it constructs a run specification with an
   extraction policy set, **Then** the module compiles and the policy's mode, marker and
   pattern are all settable.
2. **Given** a facade-only module, **When** it constructs a run specification with HTTP
   request configuration set, **Then** the module compiles and URL, method and headers
   are all settable.
3. **Given** a driver that returns such a specification's results, **When** a run uses
   it, **Then** extraction behaves identically to the same policy configured through
   `mentat.yaml`.

---

### User Story 3 - Return a structured Detail from a custom Comparator (Priority: P3)

A comparator author asserting a numeric or aggregate property wants to attach the
structured detail behind the verdict — the expression, the computed and expected values,
the per-run figures — so reports show *why* a verdict landed, not just that it did. The
`Verdict.Detail` field is published and frozen; its type cannot be named.

**Why this priority**: Lowest user-facing severity of the three — a comparator still
works without `Detail`, returning reasons instead. It is included because the type is
already frozen on the surface, so the asymmetry (frozen but unwritable) is the same
defect class.

**Independent Test**: An external-module compile test returns a verdict with `Detail`
populated, using facade names only.

**Acceptance Scenarios**:

1. **Given** a facade-only module, **When** a comparator returns a verdict with
   structured detail attached, **Then** the module compiles and every exported member of
   that detail is settable.
2. **Given** a run whose comparator attached such detail, **When** results are rendered,
   **Then** the detail appears in the output rather than being dropped.

---

### User Story 4 - The gate catches the next unnameable seam type (Priority: P4)

A Mentat maintainer adds a parameter or result type to a seam signature. If that type
has no facade name, they learn it from a failing check in their own PR — not from an
extension author months later.

**Why this priority**: This is the root-cause fix. The four gaps are symptoms of a
reachability definition that walks outward from `Config` and `Results` but never through
seam signatures. Patching the four without widening the definition leaves the class
open. It is P4 only because it must land green — after, or alongside, the gaps it
polices.

**Independent Test**: Introduce a deliberately unnameable seam parameter type on a
scratch branch and confirm the check fails and names it; remove it and confirm green.

**Acceptance Scenarios**:

1. **Given** the reachability definition covers seam parameter and result types, **When**
   the check runs against the current surface, **Then** it reports zero unnameable types.
2. **Given** a maintainer adds a seam method whose parameter type has no facade name,
   **When** the standard check runs, **Then** it fails and names the offending type and
   the seam method that reaches it.
3. **Given** a maintainer names that type on the facade, **When** the check runs,
   **Then** it passes — and the stability documentation no longer lists boundary 4 as an
   accepted, open gap.

---

### Edge Cases

- **Naming a type freezes more than its name.** Under the existing stability policy, a
  re-exported struct is expanded in the golden to one line per exported field, including
  declaration order. Naming these types therefore freezes their full field sets and
  ordering — the true cost of the one-way door, larger than four added lines.
- **Identifier collision** — *resolved by D2.* `ScenarioResult` is already taken on the
  facade by a different struct with a different field set (Finding B). D2 avoids the
  collision entirely by not publishing the run report; `mentat.ScenarioResult` keeps its
  identity and **gains** fields rather than being redefined.
- **Transitive exposure** — *reframed by D5.* D2/D3 planned to keep `core.ScenarioResult`
  and `RunRecord` off the surface behind facade mirrors. D5 makes them public *directly*,
  under `internal/result`, aliased and justified. The promise is unchanged — D3 had already
  committed to freezing every one of those fields — but the exposure is now explicit rather
  than mirrored, so each type needs its SC-007 justification written, not inherited.
- **The report format was the sharpest risk, and D5 largely disarms it.** `jsonReporter`
  marshals the report struct wholesale (`json.go:13-17`), so the emitted JSON is whatever
  that struct's field names, tags and order say. Under D2/D3's mirrors, the reporters would
  have been re-pointed at a *different* struct that had to replicate six `omitempty` tags
  and an exact field order — and nothing in the repo would have caught a slip: the surface
  golden does not render tags (boundary 2), no golden covers emitted report files, and
  `json_test.go` round-trips through the same struct, a symmetric test structurally
  incapable of detecting a tag rename. Under D5 the marshalled struct **is** the same
  struct, relocated and renamed; Go does not serialize type names, so the bytes are
  unchanged by construction. FR-013's golden stays — as a regression guard against the
  relocation being done carelessly — but it is no longer the thing the feature balances on.
- **Struct tags remain ungoverned.** Serialization tags are a real compatibility promise to
  anyone consuming report JSON, and the golden gate does not render them (accepted boundary
  2). This feature brings tagged types onto the surface, widening that ungoverned area, and
  leans on FR-013 rather than the surface gate to hold it.
- **A shared mutable field.** The extraction policy carries a compiled pattern — a
  pointer. Two runs given the same policy value share it; the spec assumes this is safe
  for concurrent use but it should be confirmed rather than presumed.
- **Reporters that are not registrable are worse than absent** — *closed by D1.* Recorded
  because it is the argument that decided FR-004: exposing a report type without a
  registration hook would have frozen a type serving a seam nobody can use.
- **A package-global registry cannot serve a per-run option** — *addressed by D4.* Naive
  `WithReporter` implementations that write to `registry.go:192` would reintroduce the
  reentrancy defect closed by 007 T010/T011. Concurrency, not just collision, is the test.
- **Adding fields to `Results` is not free either.** `Results` and `ScenarioResult` are
  returned to every existing `Run` caller. Additive fields are source-compatible except for
  **unkeyed composite literals**, and D5 also *reorders* the fields — so an unkeyed literal
  that still compiled would silently mean something different. Keyed literals, the only
  form used in this repo, are unaffected. The migration note must say both.
- **D5 changes how these types render in the golden.** They stop being facade-*declared*
  structs (one inline line each, at `public-surface.golden:169` and `:173`) and become
  *aliases*, which expand to one `field (X)[nn]` line per field. Expect the diff to grow by
  roughly twenty lines rather than rewriting two. That is finer-grained freezing, not a
  regression — but it invalidates any line-count expectation set before D5.
- **A seam cannot be a facade-declared type.** The general rule D5 discovered, worth
  keeping: a type internal packages must *consume* has to live in an internal package and
  be aliased, because root already imports them. Only **terminal** types — produced at the
  facade and never passed back down — can be declared there. Any future seam work hits this
  the moment it forgets.

## Requirements *(mandatory)*

### Functional Requirements

Requirements state the capability, not the mechanism, and are satisfied by any resolution
that makes the statement true. Two mechanisms are nonetheless settled and cited where they
bind: facade aliases for the three closed types (see Assumptions), and D2/D3 for the
`Reporter` seam — the latter because it was chosen over alternatives that would also have
satisfied the bare capability, and the reasoning must not be lost.

- **FR-001**: Every type appearing in a published seam's method signature MUST be
  writable by a module importing only the public facade.
- **FR-002**: Every exported field of a published data type MUST be settable by a module
  importing only the public facade. This covers `Verdict.Detail`, `RunSpec.Extract` and
  `RunSpec.HTTP`.
- **FR-003**: An external module MUST be able to declare a type satisfying the published
  `Reporter` seam using facade names alone.
- **FR-004**: A caller-supplied `Reporter` MUST be registrable for a run through the
  published registration surface, and MUST be invoked with that run's outcome (D1).
- **FR-005**: The existing `mentat.ScenarioResult` that `Run` callers already consume MUST
  keep its identity and meaning — no published name may silently change what it refers to
  (D2). Types that reach the public surface do so **deliberately and with a justification**
  (D5, SC-007), never by accidental transitive exposure.
- **FR-006**: The reachability definition used to check nameability MUST include seam
  method parameter and result types, in addition to types reachable from `Config` and
  `Results`.
- **FR-007**: A newly introduced unnameable type reachable from the public surface MUST
  fail the standard check, and the failure MUST name both the offending type and the
  position that reaches it.
- **FR-008**: Both external-module witnesses MUST continue to compile. The **facade-only
  test** MUST additionally be extended to exercise each newly-nameable type and to declare
  a type satisfying each of the six seams. The **example module** carries a compile-only
  obligation and MUST NOT require a source edit — that is what makes it evidence for
  SC-006 rather than a second thing this feature maintains.
- **FR-009**: Every surface change in this feature MUST carry the three acts the
  stability policy requires — regenerated golden, changelog entry, and a migration note
  for any breaking change.
- **FR-010**: The stability documentation MUST be updated so boundary 4 either no longer
  exists or is restated to describe only what genuinely remains outside the guarantee.
  No accepted-gap text may survive that the code no longer matches.
- **FR-011**: Every datum a built-in reporter renders MUST be reachable from the
  published seam's parameter, so a custom reporter can produce output equivalent to any
  built-in one (D3). This covers the suite totals and timing, and per scenario its tags,
  sequence, qualifiers, aggregate detail and per-run records.
- **FR-012**: Exactly one `Reporter` interface MUST exist. Built-in reporters MUST be
  re-expressed on it, so no built-in renders from a richer, privileged input than a
  custom one (D3).
- **FR-013**: The bytes each built-in reporter emits MUST be unchanged by this feature —
  field names, serialization tags, ordering and omission behaviour included. The
  protecting check MUST exist and be green **before** the re-expression lands, not after.
- **FR-014**: Reporter registration MUST be scoped to a single run and MUST NOT mutate
  package-global state, so concurrent runs registering different reporters cannot collide
  (D4). Collisions within one run MUST fail loudly, matching the existing options.
- **FR-015**: Every comment or document this feature falsifies MUST be corrected in the
  same change — at minimum the `Reporter` half of `mentat.go:59-62`, and the
  "the CLI emits reports after `Run` returns" claim at `engine/build.go:53` and
  `registry.go:187`, which the 007 recompose already made untrue (emission is at
  `run.go:457`, inside `Run`).

### Key Entities

- **Public surface**: the set of symbols an external module may use — the seams, the
  registration options, the run entry point, and the configuration and result types.
- **Reachable set**: the types an external author must be able to write in order to use
  the public surface. Today defined as the closure from configuration and results; this
  feature widens it to include seam signatures.
- **Nameable type**: a type an external module can write in its own source using only the
  facade import.
- **Golden surface record**: the reviewed text file recording the frozen surface; its
  diff is how a surface change is deliberately acknowledged.
- **Seam**: a published interface an external module may implement — `Driver`,
  `TraceStore`, `Comparator`, `Judge`, `Correlator`, `Reporter`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An external module importing only the public facade can implement **6 of 6**
  published seams, up from 5 of 6 today.
- **SC-002**: The count of types frozen on the public surface but unwritable from outside
  the module is **0**, down from 4 today. Three reach zero by being named
  (`AggregateDetail`, `ExtractPolicy`, `HTTPSpec`); `RunReport` reaches it by leaving the
  surface — under D2 no published signature references it any more.
- **SC-003**: An extension author can produce their own rendering of a suite's results
  end to end — declare, register, run, receive output — without importing anything but
  the facade, in a single file with no forked build.
- **SC-004**: A deliberately-introduced unnameable seam type is caught by the standard
  check, and the failure message names the type and the reaching position, so the author
  needs no external guidance to locate it.
- **SC-005**: Every previously-documented accepted gap that this feature closes is
  removed from the stability documentation in the same change; no documentation states a
  boundary the code no longer has.
- **SC-006**: The existing external-module example continues to compile untouched —
  no consumer is forced into a source edit by a non-breaking part of this change.
- **SC-007**: Every type newly added to the frozen surface is recorded with a
  justification, per the standing rule that a symbol appears on the surface with a
  justification or is not exported.
- **SC-008**: A custom reporter and a built-in reporter render from the same input — the
  number of data points available to a built-in but not to a custom reporter is **0**, down
  from **8** today: suite total, started-at, duration, and per scenario its tags, sequence,
  qualifiers, aggregate detail and per-run records. Under D5 this reaches zero by
  construction — both render the same type — so the criterion is verified by the reporters
  compiling against the published seam, not by counting fields.
- **SC-009**: The bytes emitted by each of the three built-in reporters are unchanged
  across this feature, demonstrated by a check that was green *before* the reporters were
  rewritten. It reuses the repo's established golden convention — `MENTAT_UPDATE_GOLDEN`
  plus normalization of nondeterministic tokens, as `normalizeGoldenStdout`
  (`mentat_golden_test.go:44-46`) already does for godog's duration line — so report
  timestamps do not make it flaky.
- **SC-010**: Two concurrent runs registering different reporters under the same name
  both succeed, each using its own — the reentrancy property 007 established for the other
  seams now also holds for reporters.

## Assumptions

- **Naming via facade aliases is the mechanism throughout** — for the three closed types
  as stated in the feature description, and, after D5, for the result types and the
  `Reporter` seam as well. An earlier version of this bullet said the `Reporter` case
  involved "no alias at all"; that was the D2/D3 facade-declared model, which is an import
  cycle. **Everything public is an alias.** Declaring a seam's parameter types at the
  facade is the one thing this feature proves you cannot do.
- **The three closed types are safe to expose now.** `AggregateDetail`, `HTTPSpec` and
  `ExtractPolicy` reference only builtins and a standard-library pointer; exposing them
  adds no further internal type to the surface. Verified while writing this spec.
- **Widening is acceptable under v0.** The module is pre-1.0 and the policy permits
  surface change provided it is deliberate and carries the three acts. This feature is
  the deliberate act for these types.
- **`AggregateComparator` is out of scope.** The sibling aggregate seam
  (`internal/core/core.go:110`) is not published at all. That is a missing-symbol gap, a
  different defect from unnameability, and is not addressed here — though it is the main
  consumer of `AggregateDetail`, which makes that type's standalone value modest.
- **The `Correlator` registration gap is out of scope.** `Correlator` has no
  registration option either, and for the same documented reason as `Reporter`. Unlike
  `Reporter` it is fully nameable and therefore implementable, so it exhibits no
  nameability defect. Only the nameability class is in scope; registration is raised for
  `Reporter` alone, and only because nameability without it delivers nothing.
- **Coverage, test-first, and the review gate apply unchanged.** This feature follows the
  project's standing quality gates; they are not restated as requirements here.

## Dependencies

- Builds directly on feature 009's verified sweep and its recorded deferral.
- Touches the golden surface record owned by feature 007 and the stability policy
  documentation; both must move in the same change.
- The `Reporter` resolution interacts with a standing feature-007 design decision
  (`run.go:243-246`), which kept the facade's result types separate so internal report
  records would not leak. D5 supersedes the *mechanism*: the types are unified in
  `internal/result` and aliased, so what a reporter renders is exactly what a `Run` caller
  receives. The 007 concern — that the surface should not silently acquire internal types
  — is honoured by every newly public type carrying its SC-007 justification, not by
  keeping two structs.
- D4 depends on the registry reentrancy property established by 007 (T010/T011) and
  extends it to a seam that predates it. Moving reporters off the package-global map at
  `registry.go:192` touches `report.RegisterBuiltins` (`engine/build.go:56`) and the
  emission call site at `run.go:457`.

## Out of Scope

- Tagging v1.0 or otherwise freezing the surface permanently.
- Publishing the aggregate comparator seam.
- Adding a registration option for `Correlator`.
- Closing stability boundaries 1–3 (single-line aliases, struct tags, one-level
  expansion). Only boundary 4 is in scope.
- Changing what the built-in reporters emit. D3 rewrites how they are fed, not what they
  produce; FR-013 makes that a hard constraint rather than an aspiration.
- Re-shaping any seam other than `Reporter`. The other five are already implementable
  once P2/P3 land, and no evidence was found that their signatures need to change.
