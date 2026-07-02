# Phase 1 Data Model: Verdict Integrity

Additive changes only; no existing field changes meaning. References: research.md
decisions R1–R5.

## trace.Span (internal/trace)

| Field | Change | Rules |
|-------|--------|-------|
| `Status string` | semantics tightened | Always one of the canonical constants `StatusUnset` (""? no — explicit `"Unset"`), `StatusOk` (`"Ok"`), `StatusError` (`"Error"`). Set only by store decoders (R1). Unknown source spelling → decode error naming span id/name and the raw value. |
| `Kind string` | **new** | OTLP enum spelling (`SPAN_KIND_*`) or `""` = unspecified (R2). Set by store decoders and fixture loader. |

Canonical constants live in `internal/trace` so comparators, CEL, selectors, and
report all reference one definition (Principle I: comparators stay
transport-ignorant).

### State/validation notes

- `errorCount` (comparator) counts `Status == trace.StatusError` only.
- `span.status` / `span.kind` selector values are validated against the canonical
  sets at parse time — a selector naming an unknown value is a hard authoring error
  (prevents the "permanently green selector" class).

## core.Evidence (internal/core)

| Field | Change | Rules |
|-------|--------|-------|
| `Failed bool` | unchanged | |
| `FailureKind string` | unchanged (`driver` \| `resolve`) | |
| `FailureMsg string` | **new** | Wrapped error text from the failing engine call; non-empty iff `Failed`. |
| `Output Output` | semantics extended | Real driver Output is retained when `FailureKind == "resolve"` (driver succeeded). Zero Output only for `FailureKind == "driver"`. Doc comment updated (the current "a failed run carries no Trace" stays true; "carries no Output" was never promised). |

## Fixture format (internal/store/filestore)

| Element | Change | Rules |
|---------|--------|-------|
| `parentIndex` | validation added | `-1` = root (only root marker). `0 ≤ i < len(spans)` and `i != self` = parent. Anything else (including omitted for span 0 self-reference cases) → load error naming span index/name and the offending value (A7). |
| `status` | vocabulary | Canonical or OTLP spellings accepted; else load error (R1). Existing repo fixtures migrated to canonical spellings. |
| `kind` | **new, optional** | OTLP spellings; omitted = unspecified (R2). |

## Aggregate record (internal/comparator/aggregate_cel)

| CEL field | Change | Rules |
|-----------|--------|-------|
| `runId`, `failed`, `failureKind` | unchanged, always bound | |
| `status`, `exitCode`, `bodyText`, `answer` | binding guarded | Bound only for samples with real Output (not failed, or resolve-failed). Expression references a boundary field while a driver-failed sample is present → hard error advising `r.failed` guard (R4.3, A6). |
| trace-derived (`tokens`, `cost`, `errors`, `latencyMs`) | unchanged | Already reference-gated and skipped for failed samples. `errors` now counts canonical `StatusError`. |

## Report entry (internal/report)

| Field | Change | Rules |
|-------|--------|-------|
| `DerivationNote string` | **new** | Human-readable note when sequence/detail derivation was impossible (e.g. missing `service.name`); rendered in JSON + HTML. Derivation can no longer return a scenario-failing error (R5, A8). |

## Correlate resolve result (internal/correlate)

No type change. Behaviour contract: returns `(*trace.Trace, nil)` only when the
stability gate passed; deadline with unstable/nonzero spans → `(nil, error)` naming
run id, last span count, stability progress, and timeout (A3). Zero-span deadline
error unchanged.

## Tempo query (internal/store/tempo + config)

| Element | Change | Rules |
|---------|--------|-------|
| search request | `limit` param added | Default 100; `poll.searchLimit` config override. |
| response guard | **new** | `len(traces) == limit` → hard error suggesting the config bump (R3, A4). |
