# Phase 1 Data Model: Observability & Config Integrity

References research.md R1–R8.

## Logger wiring (internal/engine, correlate, driver, cmd/*)

| Element | Change | Rules |
|---------|--------|-------|
| `engine.Build(cfg, opts...)` | gains `WithLogger(*slog.Logger)` option | Default: discard handler (silent). Logger passed to correlator, drivers, and kept on engine. No package-global logger anywhere. |
| CLI flags | `-v` (Info), `-vv` (Debug) on both binaries | Handler: slog text → stderr. Default: silent. |

## Log line schema (stderr, text handler)

| Level | Event | Attributes |
|-------|-------|------------|
| Info | `drive.start` | target, adapter, run_id |
| Info | `resolve.start` | run_id, store_endpoint, query |
| Info | `resolve.done` | run_id, spans, roots, rounds, elapsed |
| Debug | `drive.env` | Mentat-set keys with values (never inherited env) |
| Debug | `resolve.poll` | round, spans_seen, stable_streak |
| Debug | `drive.done` | run_id, exit_code, elapsed |

Attribute names are contract (tests pin them); wording of message text is not.

## Config (internal/config)

| Element | Change | Rules |
|---------|--------|-------|
| YAML decode | strict (`KnownFields(true)`) | Unknown key at any level → load error naming key + path (+ file). |
| adapter allowlist | shrunk | `defaultConcurrency` keys only `shell`, `http`. Adapter existence is validated at Build (below), not Load. |

## Engine build validation (internal/engine, internal/registry)

| Element | Change | Rules |
|---------|--------|-------|
| `registry.Drivers()` | **new accessor** | Sorted names of registered drivers. |
| `engine.Build` | validates targets | Any target adapter ∉ registered drivers → build error naming target, adapter, and the registered set. Runs before first scenario. |

## Env injection policy (internal/engine, internal/driver)

| Variable | Rule |
|----------|------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Injected only when config value non-empty (config wins over ambient; unset config → ambient untouched). |
| `OTEL_RESOURCE_ATTRIBUTES` | Merge: parse ambient (percent-decoding via inverse of `otelEncode`), overlay Mentat tags (Mentat wins collisions), re-encode. Malformed ambient value → hard error naming the value (constitution IV, not silent drop). |

## Error shape changes

| Error | Enrichment |
|-------|-----------|
| zero-span resolve timeout | + `store:` endpoint, `query:` TraceQL, `checklist:` static 3 items (R8) |
| unstable-deadline (feature 002) | + `store:`/`query:` lines (no checklist) |
| unknown config key | names key, path, file |
| unregistered adapter | names target, adapter, registered set |
| span ordinal unparseable | names ordinal text and step |

## Correlator construction (internal/engine)

| Element | Change | Rules |
|---------|--------|-------|
| `engine.BuildCorrelator(cfg, logger)` | **new** (mirrors `BuildStore`) | Owns uuid source + PollConfig defaults (named constants: 200ms / 30s / 3). Both binaries consume it; their local `parseDur`/`orDefault` copies are deleted. |
