# Phase 0 Research: Correlation Performance

## R1. Cheap per-round change check (C1) — what signal, what sensitivity

**Decision**: Keep the last decoded forest per trace ref; each poll round fetches
the trace payload bytes and compares (a) payload length and (b) a fast hash
(FNV-1a over the body) against the previous round. Unchanged → count a stable
observation without decoding. Changed → decode, update the cached forest, reset
stability. The final returned forest is the one whose decode produced the last
stable observation — no extra decode, no new partial-evidence window (the bytes
that were hashed are the bytes that were decoded).

**Rationale**: Strictly more sensitive than today's span-count comparison (any
byte change trips it, including attribute mutations span-count misses). Avoids
inventing store-API surface: `GetByID` already returns the raw payload before
decode; the split is internal to the resolve loop.

**Alternatives considered**: (a) Tempo search metadata span counts — extra API
shape, version-dependent, weaker sensitivity; (b) HTTP ETag/If-None-Match — Tempo
does not emit validators on `/api/traces`; (c) decode-and-compare (status quo) —
the cost being removed.

## R2. Semaphore scope (C2) — Chesterton check

**Decision**: Release the per-target slot immediately after `drv.Run` returns
(explicit release, not defer-to-end); `cor.Resolve` runs outside the slot.
Resolution concurrency gets its own internal bound (constant, 8 concurrent
resolutions per engine) purely as store protection.

**Rationale**: The audit's Chesterton check found `max_concurrency` documented as
bounding SUT execution (config.go's concurrency defaults are per-adapter SUT
limits); nothing documents an intent to gate Tempo load — and the same Tempo
instance already serves the fully-parallel e2e suite without protection. The
separate resolve bound (8) preserves polite store behaviour at negligible cost.

**Alternatives considered**: config-exposed resolve concurrency — YAGNI, adds a
knob nobody asked for; keeping defer and adding a second semaphore acquired by
Resolve — equivalent outcome, more moving parts.

## R3. Per-round fetch fan-out (C3)

**Decision**: `errgroup.WithContext` per round; results merged in ref order after
`g.Wait()` (deterministic merge preserved); first error cancels the round and
fails resolution with today's wrapped `correlate: get %s` error.

**Rationale**: Roots/Spans merging is append-only and order-independent for
correctness, but keeping ref-order merging preserves byte-identical report
rendering. errgroup is already the codebase's fan-out idiom (semantic matcher).

## R4. Known-complete resolution mode (C4)

**Decision**: Extend the `Correlator` seam with an explicit mode:
`ResolveComplete(ctx, store, runID)` — one Query, one fan-out fetch pass, no
stability loop, no sleep; zero traces → the existing descriptive not-found error.
`mentatctl` replay/format/diff and any saved-run path use it; live scenario
resolution cannot reach it (separate method, no flag plumbed through config).

**Rationale**: A separate method makes accidental live use a compile-time
impossibility (spec edge case) versus a boolean flag someone defaults wrong.
`diff`'s two resolves also parallelize trivially at the ctl layer (both are
fetch-once) — noted as an implementation task, not a contract.

**Alternatives considered**: PollConfig{StableFor:1, Interval:0} special-case —
works but is a magic-value contract (constitution IV smell) and still runs one
needless re-query round.

## R5. Compile-once matchers (C6)

**Decision**: Compile regex/JSON-Schema in the expectation constructor (where CEL
programs already precompile), store the compiled artifact on the matcher struct,
and make `Match` read-only. Race-safety by construction (compile happens before
any parallel evaluation; `regexp.Regexp` and compiled schemas are documented
concurrency-safe for matching).

**Rationale**: Mirrors the existing CEL precompile pattern (steps.go scenario-init
precompile) — same lifecycle, same failure timing (authoring errors surface at
parse/precompile, before the SUT is driven).

**Alternatives considered**: lazy `sync.Once` compile inside Match — hides
authoring errors until evaluation time, the opposite of the codebase's fail-fast
precheck direction.

## R6. Baseline measurement protocol (SC-001/SC-005)

**Decision**: Record before/after wall times for (a) the e2e aggregate suite and
(b) a `@runs(10, parallel)` scenario, same machine, same harness, 3 runs each,
median reported; stored in `specs/004-correlation-performance/baseline.md` at
implementation time. Hermetic counter tests (FR-007) are the merge gate; the live
numbers document the win but a flaky CI machine cannot block the feature on its
own.

**Rationale**: Spec separates guarantees (hermetic, gate) from measurements
(live, evidence); this protocol encodes that.
