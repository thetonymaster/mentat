# Phase 0 Research: Observability & Config Integrity

## R1. Logger seam — how logging enters without violating manual DI

**Decision**: `log/slog` with a `*slog.Logger` injected as an option on
`engine.Build`/`BuildStore`/`BuildCorrelator` and stored on the constructed
seams (engine, correlator, drivers). Default is `slog.New(discardHandler)` —
silent, zero-allocation on disabled levels. CLIs map `-v` → Info, `-vv` → Debug,
handler writes text format to stderr. No package-level logger variables.

**Rationale**: slog is stdlib (no dependency), leveled, structured, and a
disabled handler is free on the happy path (SC-005's byte-identical requirement).
Constructor injection keeps Principle III intact and makes buffer-backed test
assertion trivial.

**Alternatives considered**: global `slog.SetDefault` — hidden wiring, races with
parallel tests; zap/zerolog — external deps for no needed capability;
context-carried loggers — obscures which seam narrates what.

## R2. Narration points and levels

**Decision**:
- Info (`-v`): target+command driven (run id), store endpoint at build,
  resolution started (query tag/value), resolution outcome (spans, roots,
  rounds, elapsed), report emission paths.
- Debug (`-vv`): injected env for Mentat-set keys (names+values; inherited env
  never echoed), per-poll round observations (span count, stable streak), driver
  exit codes, per-ref fetch timing.

**Rationale**: matches the spec's two-level assumption; keeps Info terse enough
for CI logs while Debug answers "what exactly did Mentat do".

## R3. Strict config mechanics (D2)

**Decision**: switch `config.Load` to `yaml.NewDecoder(bytes.NewReader(data))`
with `KnownFields(true)` — the exact pattern `internal/expectations` already uses
— and wrap the yaml error to name the file. A table test enumerates one typo per
config section (root, poll, judge, targets.<name>, reporters) and asserts the key
name appears in the error.

**Rationale**: proven in-repo pattern; yaml.v3 reports unknown-field errors with
line and key, satisfying "names the key and its path".

**Alternatives considered**: post-parse reflection diffing — reinvents what the
decoder already does; JSON-schema validation — new dependency and format for no
additional guarantee here.

## R4. Adapter validation source of truth (D3)

**Decision**: `engine.Build` validates every target's adapter against
`registry.Drivers()` (new listing accessor) after driver registration completes;
failure: `engine: target "svc": adapter "grpc" has no registered driver (registered: http, shell)`.
`config.defaultConcurrency` drops `mcp`/`grpc` (concurrency defaults keyed only
to adapters that exist); config.Load keeps only shape validation.

**Rationale**: the drifted hardcoded list *was* the bug; the registry is the
runtime truth and Build is the moment both config and registry exist. Load-time
cannot know registered drivers without inverting the dependency.

**Alternatives considered**: registering stub drivers that error on Run —
converts a startup error back into a mid-suite one (the audit finding).

## R5. Telemetry env injection policy (D4)

**Decision**: in `driveOnce`: omit `OTEL_EXPORTER_OTLP_ENDPOINT` entirely when
`cfg.OTLPEndpoint == ""` (config wins when set). In the shell driver: build
`OTEL_RESOURCE_ATTRIBUTES` by parsing any ambient value, merging Mentat's tags
over it (Mentat keys win collisions — `test.run.id` correlation integrity), and
re-encoding with the existing `otelEncode`. Narrate injected values at Debug.

**Rationale**: preserves working developer environments (spec US3) while keeping
correlation deterministic. Merge lives beside `resourceAttrs()` which already
owns the encoding.

**Alternatives considered**: always requiring `otlpEndpoint` in config — breaks
the ambient-only workflow that works today by accident; documenting the clobber —
it's a bug, not a feature.

## R6. Ordinal parse surfacing (D5)

**Decision**: `steps.parseSpanSpec` propagates the `strconv.Atoi` error:
`steps: span ordinal "99999999999999999999" in step %q: value out of range`.
Table-test the overflow and a plain-huge case.

## R7. Correlator single construction (D6)

**Decision**: add `engine.BuildCorrelator(cfg, logger)` (mirroring `BuildStore`)
that owns uuid-source, `PollConfig` defaulting (200ms/30s/3), and the parseDur/
orDefault helpers; both `cmd/mentat` and `cmd/mentatctl` call it and delete their
copies. Poll defaults become named constants in one file.

**Rationale**: identical shape to the existing `BuildStore` seam treatment — the
audit's F4 observation was precisely that this pattern already exists and the
correlator missed it. Registry indirection is unnecessary (only one correlator
implementation exists; a registry can be added when a second appears — three
examples before abstraction).

## R8. Diagnostic checklist content (D1/FR-003)

**Decision**: static suffix on the zero-span timeout error:

```
correlate: no trace for run "<id>" within 30s (0 spans seen)
  store: http://localhost:3200  query: { .test.run.id = "<id>" }
  checklist: (1) is the collector/Tempo up? (deploy: make harness-up)
             (2) does the SUT export OTLP to the endpoint above?
             (3) were OTEL_RESOURCE_ATTRIBUTES applied? (run with -vv to see injected env)
```

Substrings `store:`, `query:`, and `checklist:` are pinned by tests (FR-009).
Composes with feature 002's unstable-deadline error (that error gains the same
store/query enrichment, not the checklist).
