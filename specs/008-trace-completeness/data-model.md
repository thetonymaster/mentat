# Phase 1 Data Model: Trace Completeness Contract

Additive changes only, except the `Correlator.Resolve` signature (research R1).
No storage schema; no comparator-visible changes beyond the additive
`Verdict.Qualifiers`.

## New types (internal/core)

### CompletenessContract

Per-run completeness requirements, built by the engine from target config.

| Field | Type | Meaning |
|-------|------|---------|
| `Kind` | `string` | `"spawned"` (shell/mcp) or `"request"` (http/grpc); derived from the adapter, not user-set |
| `Mode` | `string` | `"settle"` (default) or `"strict"` |
| `Settle` | `time.Duration` | minimum observation period from drive-return; default 2s (spawned) / 5s (request); 0 permitted |
| `KnownComplete` | `bool` | historical trace (replay/diff): skip barriers and stability, fetch once |

Validation (config load, Constitution IV): unknown `Mode` → load error; negative
or unparsable `Settle` → load error naming the target and value. `Kind` and
`KnownComplete` are engine-derived — not part of the YAML surface.

### ResolveRequest

The new `Resolve` argument.

| Field | Type | Meaning |
|-------|------|---------|
| `RunID` | `string` | the correlation tag value (unchanged semantics) |
| `Contract` | `CompletenessContract` | barriers for this run |

### Changed seam

```go
// before
Resolve(ctx context.Context, store TraceStore, runID string) (*trace.Trace, error)
// after
Resolve(ctx context.Context, store TraceStore, req ResolveRequest) (*trace.Trace, error)
```

Mocks regenerated (`go generate ./...`). Callers: engine (contract from target),
`cmd/mentatctl` replay/format/diff (`KnownComplete: true`), tests.

## Changed types

### core.Verdict (additive)

| Field | Type | Meaning |
|-------|------|---------|
| `Qualifiers` | `[]string` | completeness qualifiers attached by the engine (never by comparators); rendered by reporters on pass AND fail |

### config.Target (additive)

```yaml
targets:
  mybot:
    adapter: shell
    completeness:        # optional block; omitted → mode=settle, kind-default window
      mode: strict       # "settle" (default) | "strict"
      settle: 2s         # Go duration string; kind-dependent default when omitted
```

| Field | Type | Default |
|-------|------|---------|
| `Completeness.Mode` | `string` | `"settle"` |
| `Completeness.Settle` | `string` (Go duration) | `2s` spawned / `5s` request-scoped |

## Sentinel contract (in-trace, strict mode)

| Property | Value |
|----------|-------|
| Attribute key | `test.span.count` |
| Value | total span count of the whole merged run forest, **including** the sentinel-bearing span |
| Cardinality | exactly one sentinel-bearing span per run |
| Detection | scan merged forest per poll round for the attribute key |

State machine per poll round (strict mode):

| Sentinels found | Observed vs declared | Action |
|-----------------|----------------------|--------|
| 0 | — | keep polling; at timeout → hard error (missing sentinel) |
| ≥2 | — | hard error immediately (ambiguous declaration, span ids named) |
| 1 | observed < declared | keep polling; at timeout → hard error (declared/observed/elapsed) |
| 1 | observed == declared | resolution concludes; verdicts proceed |
| 1 | observed > declared | hard error immediately (declaration violated) |

## Resolution termination condition (post-008)

```
KnownComplete            → single fetch, no polling
strict (1 sentinel seen) → conclude when observed == declared
settle                   → conclude when elapsed(drive-return) >= Settle
                           AND stability gate (feature 002 semantics) satisfied
all modes                → zero spans at timeout → hard error (unchanged)
                           deadline with spans, condition unmet → hard error
                           naming the unsatisfied barrier (FR-013)
```

## Relationships

- Engine derives `CompletenessContract` from `config.Target` + adapter kind at
  drive time; drive-return timestamp anchors `Settle`.
- Steps layer marks each expectation as completeness-sensitive or not (absence /
  count / aggregate steps → sensitive); engine joins sensitivity × bounded
  contract → appends the qualifier to `Verdict.Qualifiers`.
- Strict mode ⇒ no qualifier (FR-009), any adapter kind.
- Reporters (`internal/report`) render `Qualifiers` verbatim; no reporter derives
  or invents them.
