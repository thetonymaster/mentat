# Contract: Completeness Barriers, Sentinel, Qualifier, Errors

The externally observable contract this feature commits to. Consumed by SUT
authors (sections 1–2), report readers (section 3), and implementers (section 4).

## 1. Per-adapter completeness barrier

| Adapter kind | Barrier before stability may conclude | Guarantee delivered |
|---|---|---|
| spawned (`shell`, future `mcp`) | SUT process exit (drive-return) + settle window (default 2s) + feature-002 stability gate | complete forest, provided the SUT flushes telemetry on exit and spawns no surviving children (process-group coverage arrives with feature 003) |
| request-scoped (`http`, future `grpc`) | response received (drive-return) + settle window (default 5s) + stability gate | **bounded, not exact**: spans exported after the window are missed; verdicts carry the qualifier (section 3) |
| any, strict mode | exactly one in-trace sentinel; resolved forest count == declared count | exact completeness or a hard error — never a verdict over partial evidence |
| historical (`mentatctl` replay/format/diff) | none (`KnownComplete`) | trace is immutable; fetched once, no polling |

**SUT contract (documented, spawned targets)**: flush and shut down the tracer
provider before process exit (standard OTel SDK shutdown). A SUT that exits
without flushing may lose spans; use strict mode when exactness is required.

## 2. Span-count sentinel (strict mode)

- Attribute key: `test.span.count` (namespace shared with `test.run.id`).
- Value: total spans of the whole run (all roots of the merged forest),
  including the sentinel-bearing span itself.
- Exactly one sentinel-bearing span per run. The sentinel may arrive in any
  batch, at any point in the run's export.

## 3. Report qualifier

Attached to completeness-sensitive verdicts (absence, exact-count, budget, and
aggregate assertions) when the run's contract is bounded (request-scoped,
non-strict). Rendered on pass and fail alike, in every format that shows verdict
reasons.

Canonical text (single source of truth, referenced by tests):

```
trace-completeness: bounded by ingestion window (settle 5s); spans exported later are not observed
```

(the duration reflects the target's effective settle value)

Strict-mode verdicts never carry the qualifier — including on request-scoped
targets (FR-009).

## 4. Error catalog (FR-013, Constitution IV)

Every barrier failure names the unsatisfied barrier and the values involved.
Exact wording is pinned by unit tests; shapes:

| Condition | Error shape |
|---|---|
| zero spans at timeout (unchanged) | `correlate: no trace for run %q within %v (0 spans seen)` |
| deadline, settle mode, condition unmet | `correlate: run %q: completeness not reached within %v: waiting on %s (spans seen: %d)` where `%s` ∈ `settle window (Xs remaining)` / `span-count stability` |
| strict: no sentinel at timeout | `correlate: run %q: strict mode: no test.span.count sentinel found within %v (%d spans seen)` |
| strict: duplicate sentinels | `correlate: run %q: strict mode: %d sentinel spans found (want exactly 1): [%s, %s, …]` |
| strict: count short at timeout | `correlate: run %q: strict mode: %d of %d declared spans within %v` |
| strict: count exceeded | `correlate: run %q: strict mode: %d spans exceed declared test.span.count=%d` |
| config: unknown mode | `target %q: completeness.mode must be "settle" or "strict", got %q` |
| config: bad settle | `target %q: completeness.settle: %v` (wrapping the duration parse error, or `must be >= 0, got %s`) |

## 5. Compatibility

- `Correlator.Resolve` signature changes (`ResolveRequest`); all in-repo callers
  updated in the same change; gomock mocks regenerated. Must land before (or
  amend) feature 007's public-surface manifest.
- `Verdict.Qualifiers` and `config.Target.Completeness` are additive; omitted
  config preserves today's behaviour plus the kind-default settle window.
- Feature 002's stability gate semantics are byte-for-byte preserved underneath
  settle mode; strict mode's equality condition is strictly stronger.
