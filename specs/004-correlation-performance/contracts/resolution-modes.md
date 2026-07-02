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
