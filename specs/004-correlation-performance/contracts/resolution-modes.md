# Contract: Resolution Modes

## Live resolution (`Resolve`) — unchanged contract, cheaper internals

| Guarantee | Status |
|-----------|--------|
| Returns only stability-gated evidence (StableFor consecutive unchanged observations) | unchanged (feature 002) |
| Deadline with unstable spans → hard descriptive error | unchanged (feature 002) |
| Zero traces within timeout → hard descriptive error | unchanged |
| Fetch/query errors → wrapped hard errors | unchanged |
| Observation sensitivity | **strengthened**: any payload byte change counts as instability (previously span-count only) |
| Full payload decode | **at most once per trace per resolution** (was: every round) |

### Change-check signal (clarified 2026-07-11)

- Signal: store payload byte **length + hash**, compared round-over-round per
  trace ref. The hashed bytes and the decoded bytes are the same fetch.
- Payload definition: Tempo — the exact `/api/traces/{id}` response body;
  stores with no wire payload (in-memory/mock) — a deterministic canonical
  serialization of the stored trace content (content-identical ⇒ byte-identical).
- The `TraceStore` seam splits fetch from decode to expose the payload; today's
  decoded-only `GetByID` cannot support this check.
- Guards: (1) observation-parity regression — existing corpus poll sequences
  produce the same per-round stable/reset decisions as the span-count baseline
  (FR-006 proof); (2) the unstable-at-deadline error names
  byte-change-at-constant-span-count, so store-side byte churn is diagnosable.
- Evidence for byte stability and rejected alternatives (span-count-only,
  count+size): `investigations/004-n1-change-sensitivity.md`.

## Known-complete resolution (`ResolveComplete`) — new, historical only

| Property | Contract |
|----------|----------|
| Callers | `mentatctl agent replay` / `format` / `diff`, saved-run paths only |
| Behaviour | one tag query + one concurrent fetch pass; no stability loop; no sleep |
| Absent trace | same descriptive not-found error as live mode |
| Live scenarios | cannot invoke it (separate seam method, not a flag) |

## Concurrency contract

- Per-target `max_concurrency` bounds **SUT execution only**.
- Trace resolution overlaps across parallel runs; internally bounded (constant)
  to protect the store.
- Per-round trace fetches within one resolution overlap; merge order is
  deterministic (ref order); first fetch error fails the resolution.

## Non-goals

- No user-facing config added. No poll semantics (interval/timeout/stableFor)
  redefined. No change to what "stable" means for verdicts beyond the strictly
  stronger byte-level sensitivity.
