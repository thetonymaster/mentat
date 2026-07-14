# Phase 1 Data Model: Correlation Performance

References research.md R1–R5. No config or fixture format changes.

## Correlator seam (internal/core, internal/correlate)

| Element | Change | Rules |
|---------|--------|-------|
| `Correlator.Resolve` | behaviour-preserving optimization (sensitivity strengthened per Clarifications 2026-07-11) | Same contract as feature 002 (stable-or-hard-error). Internally: per-ref cached `{payloadLen, payloadHash, decodedForest}`; unchanged payload → stable observation without decode; changed → decode + reset stability. Byte-change detection is probabilistic (length + FNV-1a-64: an undetected change requires an equal-length hash collision, ≈2⁻⁶⁴ — research R1 precision note). Unstable-at-deadline error names byte-change-at-constant-span-count. |
| `TraceStore` fetch/decode split | **new seam surface** | Raw-payload accessor + decode step (names at implementation; mocks regenerated). Tempo: exact `/api/traces/{id}` body. `InMemStore`/stubs: deterministic canonical serialization of stored forest (content-identical ⇒ byte-identical). The hashed bytes are the decoded bytes. |
| `Correlator.ResolveComplete(ctx, store, runID)` | **new seam method** | One query + one concurrent fetch pass; no stability loop, no sleep. Zero traces → existing not-found error. Used only by historical-inspection callers. |
| per-round fetches | concurrent | errgroup fan-out; canonical merge order (refs sorted by TraceID after Query — store Query order affects neither the stability key nor the merge); first error fails resolution with the existing wrapped error. |

## Engine (internal/engine)

| Element | Change | Rules |
|---------|--------|-------|
| per-target semaphore | scope narrowed | Acquired before `drv.Run`, released immediately after it returns (all paths). `cor.Resolve` runs outside the slot. |
| resolve bound | **new internal constant** | Max 8 concurrent resolutions per engine (store protection); not user-configurable in this feature. |

## Comparator matchers (internal/comparator)

| Element | Change | Rules |
|---------|--------|-------|
| `regexMatcher`, `schemaMatcher` | compile-once | Compilation moves to expectation construction (same lifecycle as CEL precompile); compile errors surface at authoring/precheck time. `Match` is read-only; safe under parallel scenarios. |

## ctl call sites (internal/ctl, cmd/mentatctl)

| Element | Change | Rules |
|---------|--------|-------|
| replay / format / diff resolution | switched to `ResolveComplete` | `diff` resolves its two runs concurrently. Verdict/rendering output unchanged. |

## Test instrumentation (hermetic, FR-007)

| Instrument | Asserts |
|------------|---------|
| counting stub store (gomock `DoAndReturn`) | decode ≤ 1 per trace per resolution; fetch calls per round == refs; zero sleeps in `ResolveComplete` (fake clock or elapsed bound) |
| observation-parity replay | the existing corpus poll sequences (growing 1,2,3,3,3; strictly-growing; constant-trace) yield the same per-round stable/reset decisions through the byte-level check as the span-count baseline (FR-006 proof) |
| delayed stub store | parallel batch overlap: 10-run batch with 300ms availability lag completes ≪ 10×300ms (generous bound, e.g. < 4×) |
| compile counter | one regex/schema compilation per expectation regardless of matched-span count; `-race` clean |
