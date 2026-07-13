# Investigation: 004/N1 — per-round change-detection sensitivity (A/B/C)

**Date**: 2026-07-11 · **Status**: complete — **DECIDED: B** (Q, 2026-07-11)

**Decision**: Q selected **B — payload length + FNV hash** (research R1's design),
conditional on the two guards in the Recommendation section: the hermetic
observation-parity test (makes FR-006 provable) and churn-naming deadline errors.
**Applied 2026-07-13**: the four artifact fixes below and the B decision are
encoded in spec.md (Clarifications 2026-07-11, FR-002, FR-006, edge case,
Assumptions), research.md R1, contracts/resolution-modes.md, tasks.md
(T005/T006/T009 + dependency note), plan.md, and data-model.md.
**Question**: when feature 004 makes the stability poll decode each trace at most
once, what cheap per-round "did this trace change?" signal replaces today's full
re-decode — (A) span-count + byte-size, (B) full byte-hash, (C) span-count only?

## What N1 is (provenance)

The label "N1" appears nowhere in the repo (verified: repo-wide `grep -rn "N1"
--include='*.md'` → zero hits). It comes from a prior `/speckit-analyze` session
whose report was not persisted — the uncommitted tasks.md edit references sibling
finding "U1" the same way (`specs/004-correlation-performance/tasks.md:67`,
"see U1 (resolve in plan before implementing)"). N1's substance is the verified
artifact conflict:

- `spec.md:145-146` (FR-002): cheap checks "whose sensitivity is **at least** the
  current span-count comparison".
- `spec.md:120-124` (edge case): "the cheap check must be at least as sensitive as
  the current span-count comparison (the stability gate's observation semantics are
  the feature-002 contract and must not weaken)".
- `contracts/resolution-modes.md:11`: "**strengthened**: any payload byte change
  counts as instability (previously span-count only)" — mandates B.
- `research.md:5-11` (R1): decided payload length + FNV-1a hash — i.e. B (with
  length as a fast pre-check).
- `spec.md:157-159` (FR-006) + `spec.md:185-186` (SC-004): feature-002 guarantees
  "preserved **bit-for-bit**: same pass/fail decisions, same error classes, on the
  entire existing test corpus" / "Zero verdict changes".

The tension: B is a behaviour *strengthening*; FR-006 promises *no behaviour
change* on the corpus. If any real trace's payload bytes churn without semantic
change, B (and possibly A) flips a today-passing run into stability resets and,
at worst, a false `unstable at deadline` hard error.

---

## FACTS (verified, file:line)

### F1. Today's signal is merged span COUNT, nothing else (Q1)

`internal/correlate/correlate.go:86-94`:

```go
if len(m.Spans) > 0 && len(m.Spans) == lastCount {
    stable++
    if stable >= c.poll.StableFor {
        return merged, nil
    }
} else {
    stable = 0
}
lastCount = len(m.Spans)
```

- The comparison is the **count of the merged forest across all refs**, vs the
  previous round. Not span-set, not content.
- Every round pays a full `Query` (`correlate.go:67`) plus a full fetch+decode per
  ref via `GetByID` (`correlate.go:74`).
- Consequences: content changes at constant count are invisible today; a span-set
  swap at equal count is invisible too. C is *exactly* today's semantics.

### F2. No signal exists today without full fetch+decode; bytes live only inside the store (Q2)

- Seam: `internal/core/core.go:149-153` — `TraceStore.GetByID(ctx, id)
  (*trace.Trace, error)`. Decoded trace only; no raw-bytes, size, or count
  accessor.
- `internal/store/tempo.go:108-116` — `GetByID` fetches `body []byte`
  (`tempo.go:109`), `json.Unmarshal`s it (`tempo.go:114`), and **discards the
  bytes**. The raw payload exists pre-decode, but *inside the store*, invisible to
  the resolve loop.
- Therefore research R1's rationale (`research.md:15-16`, "`GetByID` already
  returns the raw payload before decode; the split is internal to the resolve
  loop") is **wrong as written** — the split cannot be internal to the resolve
  loop; it requires new seam surface (a raw accessor or an opaque change-token).
  T006 half-acknowledges this (`tasks.md:49`, "internal/store/tempo.go if a raw
  accessor is needed").
- **Span count is not available without decoding.** Tempo search-metadata counts
  were considered and rejected (`research.md:18-19`). So under decode-once:
  - B (and the size half of A) is computable from raw bytes without decode;
  - C — and the count half of A — **requires a decode in every round it is
    checked**, defeating FR-002's "decode at most once".
- Hermetic stores have **no bytes at all**: `InMemStore.GetByID` returns a stored
  `*trace.Trace` (`internal/store/filestore.go:100-105`); gomock stores return
  constructed traces. Any byte-based signal must be *defined* for hermetic stores
  (token/derived hash) — it cannot be observed.

### F3. Byte behaviour of the real store (Q3 — live probe, 2026-07-11)

Probe: injected one synthetic 3-span trace via the dev collector
(`deploy/otel-collector.yaml:5`, OTLP HTTP :4318), then fetched
`/api/traces/{id}` 6 times over ~12s. Artifacts in session scratchpad
(`otlp.json`, `f1..f6.json`).

- **Ingest rewrites bytes**: sent standard `resourceSpans` envelope, hex IDs,
  resource attrs ordered `[service.name, test.run.id]`; response uses Tempo's
  `batches` envelope (handled at `tempo.go:83`), base64 IDs, string enum kinds,
  and resource attrs **reordered** to `[test.run.id, service.name]`. Sent bytes
  and fetched bytes are incomparable; only fetch-to-fetch comparison matters,
  which is what the poll does.
- **Fetch-to-fetch bytes were stable**: all 6 fetches returned identical payloads
  — 1099 bytes each, one SHA-256 (`87429fa5…eec3eef`) across all 6.
- Deployment probed: monolithic single-node Tempo, local backend, no replication
  (`deploy/tempo.yaml`, `deploy/docker-compose.yml`) — the same stack the e2e
  corpus (SC-004) runs against.
- **Evidence limits** (per evidence standards): one trace, one idle node, ~12s
  window. Not tested: ingester→block flush boundary mid-poll, RF>1 querier
  merges, distributed Tempo. This is an anecdote in favour of stability, not
  proof.
- The C5 copy (`tempo.go:126-130`; audit `docs/audits/2026-07-01-codebase-audit.md:44`)
  merges resource attrs into each span's Go map **after** decode — Go map
  iteration nondeterminism affects *decoded-content* comparison strategies, never
  payload bytes. Irrelevant to A/B; fatal to any "compare decoded structs" idea.

### F4. Corpus reality: no existing case distinguishes A/B from C (Q4)

- The only round-*varying* fixtures in the whole hermetic corpus are in
  `internal/correlate/correlate_test.go`:
  - counts 1,2,3,3,3 (`correlate_test.go:110-122`);
  - strictly growing counts (`correlate_test.go:254-262`).
  Both vary count and content together.
- Every other store fixture returns a **constant trace per run across polls** —
  stated explicitly at `internal/steps/steps_test.go:793` ("GetByID returns a
  constant trace per run across polls") and used at e.g. `steps_test.go:56,101,167,735`.
- **No fixture changes content while holding span count constant** — the only
  case where A or B diverges from C. None found in unit tests; none constructed
  in e2e (live traces only grow during the poll window).

### F5. The FR-006/SC-004 regression guards, and one test T009 overlooks (Q5)

The named feature-002 guards (per `tasks.md:52`, T009):

- unstable-deadline: `TestResolveDeadlineUnstableSpansIsHardError`
  (`internal/correlate/correlate_test.go:241`);
- zero-span: `TestResolveTimeoutZeroSpans` (`correlate_test.go:289`);
- truncation: `TestTempoQueryTruncationGuard` (`internal/store/tempo_test.go:233`).

Plus e2e: `TestHappyScenarioPasses` (`e2e/e2e_test.go:15`) and the L3 meta-suite
`TestBadScenariosAreCaught` (`e2e/meta_test.go:16`).

**Overlooked**: `TestResolveStablePollsUntilCountStable`
(`correlate_test.go:101`) pins the **exact GetByID call count** (`calls == 5`,
`correlate_test.go:134-136`) to prove stability-path exit. Decode-once changes
the store-call pattern (raw accessor per round, one decode) for *any* of A/B/C,
so this test needs editing regardless — T009's "no edits expected" holds only
for the three guards above, not this pin.

Live poll parameters the corpus runs under: Interval 200ms, Timeout 30s,
StableFor 3 (`cmd/mentat/main.go:51-55`, `cmd/mentatctl/main.go:183-187`).

---

## THEORIES (plausible, untested)

- **T1 (byte-churn exists somewhere).** Tempo can regroup/reorder batches for an
  unchanged trace across fetches when data migrates ingester→WAL→block mid-poll,
  or when RF>1 queriers merge partials in varying order. Untested (probe was
  single-node, may not have crossed a flush boundary). If true: B sees phantom
  changes → stability resets; byte-*size* also churns (regrouping duplicates or
  merges resource envelopes), so **A shares the exposure**. With
  StableFor=3/200ms the cost is extra rounds; a hard error requires churn to
  persist past the 30s deadline — unlikely for transitional churn, but that is
  exactly N1's flip risk.
- **T2 (dev-stack bytes are stable).** On the monolithic RF=1 harness, an
  unchanged complete trace is byte-stable for any realistic poll window.
  Supported by F3 (6/6 identical hashes); not proven in general.
- **T3 (hermetic parity is free).** In the hermetic corpus the "payload" does not
  exist (F2), so the feature *defines* the hermetic change-signal; any
  deterministic definition reproduces today's observation sequences on the
  existing fixtures (F4) → hermetic FR-006 parity holds by construction for all
  of A/B/C.
- **T4 (case for A).** A would be the right choice only if span-count stayed
  cheaply available per round *and* byte-size caught meaningful same-count
  changes. F2 falsifies the first half (count needs a decode), and same-size
  content edits evade the second. A degenerates to "size-only, decode when size
  changes" — strictly weaker than B while sharing B's churn exposure (T1).
  Evidence does not support A.
- **T5 (case for B).** B is right if fetch-to-fetch bytes of an unchanged trace
  are stable on supported deployments during the poll window. Supported by F3 on
  the SC-004 harness; unproven for flush-boundary/RF>1. B is the only signal
  computable without decode (F2), satisfies FR-002's "at least span-count"
  (strictly ≥), and catches same-count changes C misses.
- **T6 (case for C).** C is right if FR-006's "bit-for-bit" is read strictly:
  identical observation semantics guarantee identical verdicts *and* identical
  round counts. C preserves parity trivially — but requires a per-round decode
  (F2), so it cannot deliver FR-002's decode-once at all; it reduces feature US2
  to "skip re-copying attrs", abandoning the audit C1 win.
- **T7 (N1 is really a contract-drift bug).** `resolution-modes.md:11,34-35`
  quietly redefines observation semantics inside a performance feature, while
  `spec.md:122-124` frames feature-002 semantics as the contract. The wording
  conflict is verified fact; whether it flips a verdict is exactly T1 vs T2. The
  contract line should either be softened to match the chosen signal or the spec
  should explicitly bless the strengthening with a parity guard.

---

## Recommendation: **B** (payload length + hash, per research R1) — with a parity guard

- **Strongest evidence FOR B**: it is the *only* candidate computable without
  decoding (F2: raw bytes exist pre-decode in the store; span count does not).
  A and C silently reintroduce per-round decode and thus cannot implement
  FR-002's "full decode at most once" as designed. The live probe (F3) adds:
  bytes were fetch-stable on the exact harness SC-004's corpus runs against.
- **Strongest evidence AGAINST B**: byte-stability is verified only as an
  anecdote (one trace, idle single-node Tempo, ~12s). If T1 churn exists on any
  supported deployment, B manufactures instability out of nothing the verdict
  cares about — the precise false-hard-error N1 warns about — and byte-size (A)
  would not save it.
- **Corpus-parity guard test: YES, needed to make FR-006 provable.** Two pieces:
  1. Hermetic observation-parity test: replay the existing poll sequences
     (growing 1,2,3,3,3; strictly-growing; constant-trace) through the new loop
     and assert the per-round stable/reset decisions and final verdicts equal the
     span-count baseline (makes T3 a fact).
  2. Churn observability: when a reset fires with *unchanged span count*, the
     eventual deadline error must say so explicitly (e.g. "payload hash changed
     N× with span count constant 3") — so if T1 ever materialises in the field it
     is diagnosable as byte-churn, not mistaken for a growing trace. (Repo error
     convention: name the concrete thing that failed.)

### Items to fix in 004's artifacts — APPLIED 2026-07-13

1. `research.md:15-16` — correct the false "GetByID already returns the raw
   payload" rationale; the seam must grow a raw accessor or change-token (T006
   already hedges; make it explicit, cf. tasks.md U1-style note).
2. Decide the hermetic-store signal definition (token vs derived hash) — F2 means
   mocks/InMem must fabricate it.
3. `tasks.md:52` (T009) — scope "no edits expected" to the three guards; add the
   `TestResolveStablePollsUntilCountStable` call-count pin (F5) as a known
   must-edit.
4. Reconcile `contracts/resolution-modes.md:11` with `spec.md:120-124,145-146`
   per the chosen signal (T7).
