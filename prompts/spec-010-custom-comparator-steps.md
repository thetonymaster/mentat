# Spec 010 prompt — custom comparator Gherkin invocation

Input for `/speckit-specify`. Feature name suggestion: `010-custom-comparator-steps`.
Depends on: spec 009 (surface gate must freeze struct-alias fields first, since
this feature will extend the public surface). Evidence from the 2026-07-18 audit
at `b1aabb4`; re-verify file:line references during planning.

## Feature description (pass this to speckit-specify)

Finish the half-open Comparator seam: custom comparators registered via
`mentat.WithComparator` are today composable but **inert** — no `.feature` step
can ever invoke one. This is the only seam that is publicly registerable yet
unusable end-to-end, which is worse than closed: it invites integration work that
dead-ends at a documented cliff (`run.go:109-115`,
`docs/extending/comparator.md:81`, pinned by `mentat_run_test.go:467-472`). The
007 deferral was explicit and recorded
(`specs/007-public-extension-api/tasks.md:109-116`); this spec pays it off.

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
  out in the PR body.
- The CI e2e job runs on the PR; the stdout golden
  (`cmd/mentat/testdata/golden-green.txt`) is sensitive to step-registration
  changes — expect churn there and regenerate deliberately, per the
  e2e-golden-gate memory note.

## Out of scope

New comparator *kinds* (use `new-comparator` skill separately); aggregate
comparator publicization (its internal-only status is documented in 009);
matcher-seam publicization.
