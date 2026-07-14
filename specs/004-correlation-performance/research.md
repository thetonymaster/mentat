# Phase 0 Research: Correlation Performance

## R1. Cheap per-round change check (C1) — what signal, what sensitivity

**Decision** *(confirmed by clarification 2026-07-11 — option B in
`investigations/004-n1-change-sensitivity.md`)*: Keep the last decoded forest per
trace ref; each poll round fetches the trace payload bytes and compares (a)
payload length and (b) a fast hash (FNV-1a over the body) against the previous
round. Unchanged → count a stable observation without decoding. Changed → decode,
update the cached forest, reset stability. The final returned forest is the one
whose decode produced the last stable observation — no extra decode, no new
partial-evidence window (the bytes that were hashed are the bytes that were
decoded).

**Precision of the signal** *(review note, 2026-07-14)*: length + 64-bit FNV-1a
is a probabilistic change detector, not an exact one — a changed payload goes
undetected only if it keeps the exact byte length **and** collides on FNV-1a-64
(≈2⁻⁶⁴ per comparison on non-adversarial input). Accepted: the guarantee is
still strictly stronger in practice than the span-count baseline it replaces
(blind to every change preserving span count), and the store payload is not
attacker-controlled. If collision-grade certainty is ever required, the
escalation path is retaining the previous payload bytes per ref and comparing
`bytes.Equal` (hash as pre-filter), at a memory cost of one payload per ref.

**Seam consequence** *(corrected 2026-07-13; the original rationale claimed
`GetByID` "already returns the raw payload" — false: the bytes exist only inside
`Tempo.GetByID` and are discarded after unmarshal, invisible to the resolve
loop)*: the `TraceStore` seam must split fetch from decode — a raw-payload
accessor plus a decode step (exact method names decided at implementation), with
`internal/core` mocks regenerated. Stores with no wire payload (`InMemStore`,
gomock stubs) define their payload as a deterministic canonical serialization of
the stored trace content, so content-identical rounds hash identically and
hermetic FR-006 parity holds by construction. Tempo's payload is the exact
`/api/traces/{id}` response body.

**Guards** *(conditions attached to the clarification decision)*: a hermetic
observation-parity regression replays the existing corpus poll sequences through
the byte-level check and asserts the same per-round stable/reset decisions as the
span-count baseline (FR-006); the unstable-at-deadline error names
byte-change-at-constant-span-count so live byte churn (unproven but possible on
distributed/replicated stores) is diagnosable, not mistaken for a growing trace.

**Rationale**: Strictly more sensitive than today's span-count comparison (any
byte change trips it, including attribute mutations span-count misses), and the
only candidate computable without decoding — span count does not exist until the
payload is decoded, so count-based checks would reintroduce the per-round decode
this feature removes. Live probe (2026-07-11, dev harness): six fetches of an
unchanged complete trace over ~12s returned byte-identical payloads.

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

**Decision** *(amended 2026-07-14, PR #27 review)*: `errgroup.WithContext` per
round; results merged in canonical ref order after `g.Wait()` — refs are sorted
by TraceID immediately after `Query`, so store Query-order flapping neither
resets the stability gate (the ref-set key is order-independent) nor reorders
the merge; first error cancels the round and fails resolution with today's
wrapped `correlate: get %s` error.

**Rationale**: Roots/Spans merging is append-only and order-independent for
correctness, but a deterministic merge order preserves byte-identical report
rendering; canonicalizing on sorted TraceID makes that determinism independent
of the store's Query ordering. errgroup is already the codebase's fan-out idiom
(semantic matcher).

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
