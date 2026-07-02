# Phase 0 Research: Verdict Integrity

All Technical Context items were known; research resolved the four design forks the
spec left implementation-defined. Sources: the 2026-07-01 audit (empirical Tempo
probe), OpenTelemetry proto/semconv definitions, and the current code.

## R1. Canonical status vocabulary and where normalization happens

**Decision**: Three canonical values — `Unset`, `Ok`, `Error` — as constants in
`internal/trace`. Normalization is a store-boundary duty: each `TraceStore`
implementation maps its wire/fixture spellings onto the canonical set at decode
time; everything downstream (comparators, CEL, selectors, report) compares only
canonical values. Accepted inputs per source:

- Tempo OTLP JSON: `STATUS_CODE_UNSET` / `STATUS_CODE_OK` / `STATUS_CODE_ERROR`,
  plus omitted/empty status → `Unset` (audit A1 verified `STATUS_CODE_ERROR` and
  omitted on the wire; OTLP proto enum names are stable).
- Fixtures: the canonical spellings themselves, plus the OTLP enum spellings.
  Anything else is a hard decode error naming the span and the value (constitution
  IV) — not best-effort passthrough.

**Rationale**: Comparators must never know transport spellings (Principle I keeps
them portable across stores). Hard-erroring unknown spellings converts the next
vocabulary drift into a loud failure instead of a repeat of A1.

**Alternatives considered**: (a) comparators accept both spellings — rejected:
spreads transport knowledge across every consumer and invites the next drift;
(b) case-insensitive contains-match ("error" in value) — rejected: silent guessing,
violates IV.

## R2. Span kind vocabulary

**Decision**: Populate `Span.Kind` with the OTLP enum names as-is
(`SPAN_KIND_INTERNAL|SERVER|CLIENT|PRODUCER|CONSUMER`), normalized from both Tempo
JSON (`kind` field, verified present as `SPAN_KIND_SERVER` in the audit probe) and
fixtures (same spellings; omitted → empty string = "unspecified"). The
`span.kind` selector documents these exact values.

**Rationale**: `span.kind` is already a validated selector key
(`shape_selector.go`), so the user-facing vocabulary should be the one users see in
their own OTLP data. No translation layer to invent or document separately.

**Alternatives considered**: short forms (`server`, `client`) — friendlier but adds
a second vocabulary to document and map; can be added later as accepted aliases
without breaking the enum spellings.

## R3. Search truncation guard (A4)

**Decision**: `Tempo.Query` sends an explicit `limit` (default 100, config
override `poll.searchLimit`) and hard-errors when the response returns exactly
`limit` traces: `"tempo: search for test.run.id=%q returned %d traces (== limit); result set may be truncated — raise poll.searchLimit"`.

**Rationale**: Tempo's search endpoint supports `limit`; its paging support varies
by version, so "complete or loud" is guaranteed by treating a full page as
possibly-truncated. False positive (exactly-limit result) costs one config bump —
acceptable versus silent evidence loss. Boundary case from the spec (N == limit)
is handled by erring loud, per IV.

**Alternatives considered**: (a) paging via `nextPageToken`/start-end windows —
rejected for now: version-dependent API surface for a case (>100 root traces per
run) with no known user; the error message makes the ceiling visible when it's hit;
(b) keep server default and count — rejected: cannot distinguish 20-of-20 from
20-of-40 without a limit we chose.

## R4. Failed-sample semantics for single runs and aggregates (A2, A6)

**Decision** (three coordinated rules):

1. `core.Evidence` gains `FailureMsg string` — the wrapped error text from
   `driveOnce` — populated whenever `Failed` is true. `driveOnce` keeps the
   existing typed-failure contract but on resolve failure now retains the real
   `res.Output` in the Evidence (the driver did succeed).
2. Single-run surface: after `DriveN` returns, the drive step fails the scenario
   whenever `evs[0].Failed`, propagating `FailureMsg` — so both assertion-free and
   asserting scenarios go red with the root cause (A2). `@runs(N>1)` keeps
   sample-level failures for aggregate policies.
3. Aggregate binding (`aggregate_cel.go record`): boundary fields (`status`,
   `exitCode`, `bodyText`, `answer`) are bound from Output only when the sample has
   a real Output (not failed, or failed at resolve where Output is real). If the
   expression references a boundary field and any sample lacks a real value
   (driver-failed), the step hard-errors:
   `"aggregate-cel: run %d (%s) has no boundary output (driver failed: %s); guard with r.failed or fix the run"`.
   `failed`/`failureKind`/`runId` remain always bound.

**Rationale**: keeps DriveN's documented batch model, makes n=1 loud at the only
place that knows n (steps), and honors IV in aggregates: computing `rate(r,
r.status == 200)` over fabricated zeros is exactly the silent corruption the
constitution forbids. The escape hatch (`r.failed` guard) is explicit in the error.

**Alternatives considered**: (a) DriveN returns the error for n==1 — rejected:
splits DriveN's contract by arity and the engine comment that promised this was
already dead code (audit A2); steps own scenario semantics; (b) binding failed
samples' fields as CEL `null` — rejected: cel-go null-propagation surprises
(`null == 200` is false, silently counting the sample) reproduce A6 with extra
steps.

## R5. Report derivation must not flip verdicts (A8)

**Decision**: `report.Derive` returns its per-scenario detail plus a non-fatal
degradation note; the steps After-hook records the note into the scenario's report
entry (`DerivationNote` field) instead of returning the error to godog. Verdicts
come only from step results. An empty sequence with a note like
`"sequence unavailable: span abc123 (\"fetch\") missing service.name"` is the
rendered outcome.

**Rationale**: the report is an observer. IV still holds — the failure is visible
in the report artifact itself, not swallowed.

**Alternatives considered**: keeping the hard error but only when the scenario
already failed — rejected: verdict-dependent reporting behavior is harder to
reason about than "derivation never fails a scenario".

## R6. e2e error-status coverage (F5) — SUT vehicle

**Decision**: add an "error mode" to the tracelab orderflow service (a scenario
header value that makes one downstream span set error status), plus a
`features/meta/` bad feature asserting `no span has status "ERROR"` must go RED.
Follows the existing meta-test pattern (prebuilt `mentatBin`, `t.Parallel()`,
reason-substring assertion).

**Rationale**: orderflow already has failure modes (payment_decline) and the meta
harness proves red-on-bad; this is the missing error-status case the audit showed
would have caught A1.
