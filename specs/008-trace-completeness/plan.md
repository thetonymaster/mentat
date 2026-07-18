# Implementation Plan: Trace Completeness Contract — Flush Barrier for Sound Absence Assertions

**Branch**: `008-trace-completeness` | **Date**: 2026-07-03 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/008-trace-completeness/spec.md`

## Summary

Make trace resolution refuse to conclude before the run's completeness barrier is
satisfied, so absence and aggregate assertions are never judged against a partial
forest. Three mechanisms by strength: (1) for spawned-process targets, drive-return
(= process exit, since the shell driver blocks) becomes the contractual start of
completeness observation, followed by a per-target **settle window** that sets a
minimum observation period before the stability gate may conclude; (2) for
request-scoped targets, the same settle window plus an explicit
**ingestion-window-bounded qualifier** attached to completeness-sensitive verdicts
in reports; (3) an opt-in per-target **strict mode** where the SUT declares its
total span count in-trace (`test.span.count` sentinel) and resolution hard-errors
on any mismatch. The `Correlator` seam gains a per-run `ResolveRequest` carrying
the contract; feature 002's stability gate is preserved as-is underneath the new
barriers. The tracelab harness gains a late-flushing bad SUT and L3 meta-features
prove a partial forest can never produce green.

## Technical Context

**Language/Version**: Go 1.25 (module `github.com/thetonymaster/mentat`)

**Primary Dependencies**: godog (BDD runner), `go.uber.org/mock` (seam mocks),
Tempo HTTP API (live store), OTLP JSON fixtures; OTel SDK batching behaviour
(BatchSpanProcessor ~5s default schedule delay) drives the settle-window defaults

**Storage**: no new storage; `mentat.yaml` gains a per-target `completeness`
block; the sentinel is a span attribute (`test.span.count`) inside the run's own
trace

**Testing**: `go test` (hermetic, table-driven, gomock; short poll intervals as in
existing correlate tests), `//go:build e2e` against `make harness-up`, new L3
meta-features under `features/meta/` driven by a late-flushing researchbot scenario

**Target Platform**: developer workstations + Linux CI

**Project Type**: single Go module — CLI (`cmd/mentat`, `cmd/mentatctl`) over
library packages (`internal/...`) + `tracelab/` harness SUTs

**Performance Goals**: for a well-behaved SUT the barrier adds no wall-clock beyond
the configured settle default (SC-005); the settle window runs concurrently with
the polling that already happens — it is a minimum observation period, not an
additive sleep (coordination note with feature 004 recorded in research R3)

**Constraints**: feature 002's stability-gate semantics preserved exactly (FR-010);
comparators remain Evidence-only and unchanged (FR-011); every barrier failure
names the unsatisfied barrier and its values (FR-013, Constitution IV); the live
`Correlator.Resolve` seam signature changes — and because feature 007 is already
merged and its surface gate is blind to re-exported interface method sets, 008
expands that gate to render method sets and regenerates 007's public-surface
golden in the same PR (see Complexity Tracking)

**Scale/Scope**: 6 packages touched (`core`, `correlate`, `config`, `engine`,
`report`, `store` untouched) + `tracelab/researchbot` + `features/meta/` + `e2e/`
+ SUT-contract documentation; ~10 red→green pairs

## Constitution Check

*GATE: evaluated pre-Phase-0 and re-evaluated post-Phase-1 — PASS (no violations).*

- **I. Evidence-Only Comparators**: PASS. Comparators are untouched (FR-011). The
  completeness qualifier is attached by the engine/report layer from the run's
  contract — comparators never learn adapter kinds or touch stores. `Verdict`
  gains an additive `Qualifiers` field written outside the comparator.
- **II. Trace Is a Forest, Tag-First**: PASS — strengthened. The sentinel count
  applies to the whole merged forest (all roots), never a single trace; sentinel
  detection scans the merged forest, honouring multi-root runs.
- **III. Seams Are Interfaces, Wired Once**: PASS. The `Correlator` interface
  evolves (`Resolve` takes a `ResolveRequest`) but stays a consumer-defined
  interface wired at `engine.Build`; no DI framework; no new global state.
- **IV. No Silent Fallbacks**: PASS — this feature extends IV to completeness:
  count-short, missing sentinel, duplicate sentinel, and count-exceeded are each
  distinct hard errors naming run id, expected/observed counts, and elapsed time.
  Settle/strict configuration is validated at load (bad duration, unknown mode →
  load error, not a runtime default).
- **V. Test-First & Hermetic**: PASS. Red→green per FR; correlate/engine tests
  hermetic (gomock store, short windows); the late-flush proof is a
  `//go:build e2e` + L3 meta-feature pair (FR-012); coverage floor re-checked per
  touched package.

## Project Structure

### Documentation (this feature)

```text
specs/008-trace-completeness/
├── plan.md              # This file
├── research.md          # Phase 0: barrier placement, settle defaults, sentinel format, qualifier plumbing
├── data-model.md        # Phase 1: CompletenessContract, ResolveRequest, Verdict.Qualifiers, config block
├── quickstart.md        # Phase 1: how to validate end-to-end
├── contracts/
│   └── completeness-contract.md   # per-adapter barrier table, sentinel format, error catalog, qualifier text
└── tasks.md             # Phase 2 (/speckit-tasks — not created here)
```

### Source Code (repository root)

```text
internal/core/core.go            # CompletenessContract, ResolveRequest, Verdict.Qualifiers (additive)
internal/core/mocks/             # regenerated gomock mocks (Correlator signature change)
internal/correlate/correlate.go  # barrier-aware Resolve: settle window, strict sentinel termination
internal/config/config.go        # per-target completeness block (mode, settle) + validation + defaults
internal/engine/engine.go        # build contract per target; attach qualifier to sensitive verdicts
internal/report/                 # render Verdict.Qualifiers in every emitted report format (json, html, junit) + ScenarioResult carry-through
cmd/mentat/main.go               # thread contract defaults (poll config already flows here)
cmd/mentatctl/main.go            # replay/diff resolve historical traces as known-complete (no barrier)
tracelab/researchbot/            # late-flush scenario; strict-mode sentinel scenarios (good + short-count)
features/meta/                   # L3: late-flush absence must go RED; strict short-count must hard-error
e2e/                             # live-Tempo late-flush + strict-mode proofs (t.Parallel, prebuilt binary)
docs/ (or README section)        # the SUT completeness contract: flush-on-exit, sentinel format
```

**Structure Decision**: single Go module, existing package layout; no new
packages. The only seam-shape change is `Correlator.Resolve` gaining a
`ResolveRequest` (research R1); everything else is additive fields and new
tracelab/meta/e2e assets.

## Complexity Tracking

No constitution violations. Two cross-feature notes (not violations), both now
resolved or gated, recorded here for visibility:

| Risk | Why accepted | Mitigation |
|------|--------------|------------|
| Live `Correlator.Resolve` signature change lands on 007's ALREADY-MERGED public surface, which the surface gate does NOT catch — it renders only the alias line `type Correlator = core.Correlator`, never the method set | The contract is per-run data; smuggling it through config or engine-side sleeps would either break "wired once" or turn the settle window into a fixed additive sleep (fighting feature 004). The real hazard is the gate's blindness to re-exported interface method sets, not the signature change itself | Fix the root cause: expand `surface_test.go` to resolve aliased interfaces into their method sets, then regenerate `public-surface.golden` in 008's PR as the deliberate acknowledgment act under feature 007's FR-005 surface-manifest policy (T028). The known-complete path stays the pre-existing separate `ResolveComplete` method — 008 adds NO `KnownComplete` flag |
| Feature 002 (verdict integrity) is a hard prerequisite; 008 builds on its hardened stability gate | Spec declares 002-first ordering | 002 is merged; T001 gates on it (correlate hard-errors on deadline-with-unstable-spans, `correlate.go`) before any 008 work starts |
