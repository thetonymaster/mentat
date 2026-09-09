# Contract: Facade nameability v2 — the reachable set includes seam signatures

**Supersedes**:
[009 facade-nameability.md](../../009-extension-surface-integrity/contracts/facade-nameability.md).
That contract's definition and proof obligation stand; this one widens the definition it
sweeps over and closes the gap it recorded as deferred.

**Consumers**: external extension authors; `mentat_external_test.go`; `examples/kafkaecho`.
**Fulfils**: FR-001, FR-003, FR-006, FR-007, FR-008; SC-002, SC-004.
**Decision**: [research.md R2/R4](../research.md), spec D2.

## Definition

**Reachable set (v2)** — the transitive closure of exported types over *both* of:

1. **Data reachability** (v1, unchanged): exported fields starting from `mentat.Config`
   and `mentat.Results`, including slice/map/pointer element types and embedded types.
2. **Seam reachability** (new): every **parameter and result type** in the method set of
   every published seam — `Driver`, `TraceStore`, `Comparator`, `Judge`, `Correlator`,
   `Reporter` — and their transitive closure by rule 1.

**Nameability invariant** (unchanged): every member of the reachable set has a facade name
(`mentat.X`), so an external module can write it importing only
`github.com/thetonymaster/mentat`.

Standard-library and builtin types are terminals, not members: `io.Writer`,
`context.Context`, `time.Time`, `*regexp.Regexp` and friends are nameable by their own
import and are not the facade's to re-export.

## Why rule 2 exists

A seam an external module cannot *implement* is not a seam. Rule 1 alone permits the
asymmetry 009 verified and recorded: a type frozen on the public surface — so the project
promises not to change it — that no external module can write. Four such types existed at
`f2afdda`; `Reporter` was unimplementable in consequence.

Rule 2 makes the class impossible rather than the four instances absent. A maintainer who
adds a parameter type to a seam method learns from their own PR, not from an extension
author months later.

## Resolution of the four v1 gaps

| Type | v1 status | v2 resolution |
|---|---|---|
| `ExtractPolicy` | frozen, unnameable | **named** — `type ExtractPolicy = core.ExtractPolicy` |
| `HTTPSpec` | frozen, unnameable | **named** — `type HTTPSpec = core.HTTPSpec` |
| `AggregateDetail` | frozen, unnameable | **named** — `type AggregateDetail = core.AggregateDetail` |
| `RunReport` | frozen, unnameable | **removed from the surface** — the seam renders `Results` (D2); no published signature names it |

Three reach nameability by being named, one by leaving. Both satisfy the invariant; the
count of frozen-but-unwritable types reaches **0** either way (SC-002).

## Proof obligation

Two checks, both required.

1. **Compile-level (extends the v1 obligation).** `mentat_external_test.go` — facade
   imports only — writes one composite literal per reachable exported struct, each setting
   at least one field, plus a type declaration satisfying **each of the six seams**. The
   test compiling *is* the proof. `Reporter` joins the five that already compile.

2. **Mechanical sweep (new, this is the gate).** A test walks the reachable set under the
   v2 definition and fails on any member without a facade name. On failure it MUST name
   both the offending type **and the position that reaches it** (FR-007) — a field as
   `field (Verdict) Detail *AggregateDetail`, a seam method as
   `method (Reporter) Report(rep RunReport, …)`. "Unnameable type found" without the
   reaching position sends the author source-spelunking and does not satisfy this contract.

`examples/kafkaecho` (separate module, `replace` directive, facade-only imports) remains
the external-module witness and MUST keep compiling **untouched** (SC-006).

## Falsification

The gate is proven by making it fail, not by observing it pass:

- Introduce a seam method whose parameter type has no facade name → the check fails and
  names the type and the method.
- Name that type on the facade → the check passes.
- Recorded in the test file the way 009 recorded its mutation rehearsal (SC-001 precedent).

## Regression guarantee

After this contract holds, a type can reach the public surface — through a field *or* a
seam signature — only by being nameable, or by failing the build. The asymmetry class is
closed, and `docs/extending/stability.md` boundary 4 is deleted rather than reworded
(FR-010, SC-005).

## What remains outside the guarantee

Boundaries 1–3 of `stability.md` are untouched and stay accepted: single-line rendering of
map/func/`any` aliases, unrendered struct tags, and one-level expansion. Boundary 2 is
load-bearing for this feature — it is why FR-013 needs its own golden rather than relying
on the surface gate. See [report-format-golden.md](./report-format-golden.md).
