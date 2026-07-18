# Spec 010 prompt — custom comparator Gherkin invocation

Input for `/speckit-specify`. Feature name suggestion: `010-custom-comparator-steps`.
Evidence from the 2026-07-18 audit at `b1aabb4`; re-verify file:line references
during planning.

**Dependency satisfied — spec 009 shipped 2026-07-18 (PR #36).** The surface gate
now freezes exported fields of aliased structs, so any public surface this feature
adds is machine-enforced. Two consequences for planning:

- Every facade alias added here produces an alias line AND its expanded field
  lines in `specs/007-public-extension-api/contracts/public-surface.golden`.
  Regenerate deliberately (`MENTAT_UPDATE_GOLDEN=1`) and itemize each diff in the
  PR body.
- The gate has four documented boundaries it does NOT catch
  (`docs/extending/stability.md` — authoritative, do not re-derive). The one that
  bites this feature: **struct tags are not rendered**, so if this work touches
  any `yaml:"…"` tag on a config-facing struct, that is a real break for every
  user's `mentat.yaml` with zero golden diff. Author discipline, not CI.

009 also deferred one item into this spec — see "Seam-type nameability" below.

## Feature description (pass this to speckit-specify)

Finish the half-open seams: an extension point that is publicly advertised but
cannot actually be used end-to-end is worse than a closed one — it invites
integration work that dead-ends at a documented cliff. There are two, and this
spec closes both.

**(A) Comparator — registerable but inert.** Custom comparators registered via
`mentat.WithComparator` are composable but no `.feature` step can ever invoke one
(`run.go:109-115`, `docs/extending/comparator.md:81`, pinned by
`mentat_run_test.go:467-472`). The 007 deferral was explicit and recorded
(`specs/007-public-extension-api/tasks.md:109-116`); this spec pays it off.

**(B) Reporter — aliased but unimplementable.** Discovered by 009's nameability
sweep. `mentat.Reporter` is an aliased seam interface whose method signature is
`Report(rep RunReport, w io.Writer) error`, and `RunReport` has no facade alias —
so an external module cannot write the type of its own method parameter and
therefore cannot implement the interface at all. Note this falsifies the older
framing that Comparator was "the only" half-open seam.

**Core: a generic invocation step.**
Add Gherkin grammar that resolves a registered comparator by name, e.g.:

    Then the run satisfies "kafka-echo-roundtrip"

with an optional docstring/table for comparator-specific expectation payload
(design decision for clarify: how args bind — the existing built-ins bind typed
expectations at precheck; a generic step needs either an opaque payload the
comparator parses, or no args in v1. Recommend: v1 = name-only + optional
docstring passed through verbatim; the comparator owns parsing and must return a
descriptive error on bad payload — no silent tolerance).

**Required behaviours:**

1. Step resolution is precheck-validated: `mentat validate` flags an unknown
   comparator name as a `[unknown-comparator]` finding with file:line, same UX as
   `[unbound-step]` (`internal/steps/precheck.go:154,170` is where built-in
   binding lives today). A typo cannot survive to run time as a pass.
2. Comparators consume `Evidence` only (architecture invariant #1) — the step
   wiring must not leak `TraceStore`/`Driver` into comparator reach.
3. Collision/registration semantics unchanged: per-engine registry, sealed after
   Build, collision at Build time names both registrants.
4. Failure rendering: a red custom comparator renders like built-ins in stdout,
   json, junit, html reports — with the comparator's name and its reasons.
5. **L3 meta-test (mandatory):** a deliberately-failing custom comparator wired
   through a `.feature` must turn the run RED — use the `new-e2e-scenario` skill
   conventions (`{feature, reason}` row, exec prebuilt mentatBin, t.Parallel).
6. **Consumer-in-anger:** extend `examples/kafkaecho` with a custom comparator
   invoked from a real `.feature` via the generic step, so the extension path has
   a CI-gated external consumer (today kafkaecho uses only WithDriver/WithStore,
   `examples/kafkaecho/main_test.go:35-36`).
7. Docs: rewrite the cliff sections — `run.go` ComparatorFactory godoc,
   `docs/extending/comparator.md` — to describe the real end-to-end path; add the
   step to `mentat steps` metadata + `docs/steps.md` (drift-proofed generation).

### Seam-type nameability (deferred here by 009)

009 froze the public surface but its reachable-set sweep walked outward from
`mentat.Config` and `mentat.Results` only — **not** seam-interface parameter and
result types. Four types are frozen in the golden yet cannot be named from outside
the module. Verified by compile probe at 009 close; re-verify during planning:

| Type | Reached via | Consequence | Priority |
|---|---|---|---|
| `RunReport` | `method (Reporter) Report(rep RunReport, w io.Writer) error` | **`Reporter` cannot be implemented at all** — item (B) above | **required** |
| `ExtractPolicy` | `field (RunSpec) Extract ExtractPolicy` | a custom Driver author cannot build a `RunSpec` with `Extract` set | required |
| `HTTPSpec` | `field (RunSpec) HTTP HTTPSpec` | same, for `HTTP` | required |
| `AggregateDetail` | `field (Verdict) Detail *AggregateDetail` | a comparator author cannot populate `Verdict.Detail` | **low** — see below |

`AggregateDetail` is deliberately lowest priority and may be left out: `core.go:58-60`
documents that `Detail` is non-nil *only* for canonical aggregate (`@runs`)
comparisons, and aggregate comparators are internal-only (009 documented that
exclusion; it stays out of scope here). A custom comparator correctly leaves
`Detail` nil, so the gap is a surface incoherence rather than a blocker. Decide at
clarify time whether to alias it for consistency or leave it and document why —
either is defensible; silently ignoring it is not.

**Required behaviours:**

8. Alias the required types on the facade. Each addition is a golden line plus a
   composite literal in the facade-only compile test
   (`mentat_external_test.go`) — the same acknowledgment protocol 009 established.
9. **Extend the reachable-set definition** in
   `specs/009-extension-surface-integrity/contracts/facade-nameability.md` to
   cover seam-interface parameter and result types, and extend the compile test to
   enforce it — so the *next* unnameable seam type fails the build instead of
   being found by a manual sweep two features later. Closing the four instances
   without closing the hole that produced them just defers the same discovery.
10. **Prove (B) the way this spec proves (A):** an external-module witness that
    actually implements `Reporter` and is exercised — `examples/kafkaecho` is the
    existing CI-gated external consumer. A compile test proves nameability; only a
    real implementation proves usability.

Evidence: `specs/009-extension-surface-integrity/contracts/facade-nameability.md`
("Verified gap, deferred to spec 010") and `docs/extending/stability.md`
boundary 5.

**Opportunistic refactor (same feature, separate commit):** this work lands in
`internal/steps/steps.go`, already 750 lines against the repo's own split rule —
split it along responsibility lines (step defs vs world/lifecycle vs rendering)
as a behaviour-preserving refactor BEFORE adding the new grammar, so the feature
diff stays reviewable.

## Constraints

- TDD via go-test-writer end to end (this is pure behaviour change). One failing
  test at a time; hermetic tier first (inmem/otlp-file store + gomock), e2e last.
- CLI is consumer zero: whatever the library exposes, `cmd/mentat run` must
  exercise it with no CLI-only wiring.
- Public surface changes (new step metadata is not public API, but any new
  facade type/option is) go through the 009-hardened golden with each diff called
  out in the PR body. Aliasing a struct now also emits its expanded field lines,
  so expect several lines per alias, not one.
- **Widening the public surface is a one-way door.** 009 declined to alias these
  four types precisely because doing so outside an authorizing spec would be an
  unreviewed commitment. This spec is that authorization — but each alias still
  needs its own justification row in `public-surface.md`, per the manifest rule
  ("a symbol appears in the contract with a justification, or it does not get
  exported").
- The CI e2e job runs on the PR; the stdout golden
  (`cmd/mentat/testdata/golden-green.txt`) is sensitive to step-registration
  changes — expect churn there and regenerate deliberately, per the
  e2e-golden-gate memory note.

## Out of scope

New comparator *kinds* (use `new-comparator` skill separately); aggregate
comparator publicization (its internal-only status is documented in 009);
matcher-seam publicization.
