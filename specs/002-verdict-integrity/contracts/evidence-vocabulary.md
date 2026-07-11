# Contract: Evidence Vocabulary & Failure Modes

The user-visible contract this feature adds or tightens. Feature files, fixtures,
and CI logs are the consumers.

## Canonical span status (what users write in steps/selectors/fixtures)

| Canonical | Accepted source spellings (store decode) | Meaning |
|-----------|-------------------------------------------|---------|
| `Unset` | omitted status, `""`, `STATUS_CODE_UNSET`, `Unset` | span completed without a status opinion — **not** an error |
| `Ok` | `STATUS_CODE_OK`, `Ok` | explicitly successful |
| `Error` | `STATUS_CODE_ERROR`, `Error` | errored span — counted by every error assertion |

- Steps/selectors: `no span has status "ERROR"` (case-insensitive step arg),
  `span.status=Error` selectors, `MaxErrors` budgets, CEL `errors` — all count
  canonical `Error` only.
- Any other spelling in a fixture or on the wire → hard decode error naming the
  span and value. Any other value in a selector → authoring error at parse time.

## Canonical span kind

`SPAN_KIND_INTERNAL` | `SPAN_KIND_SERVER` | `SPAN_KIND_CLIENT` |
`SPAN_KIND_PRODUCER` | `SPAN_KIND_CONSUMER` | unspecified (empty).
Selector `span.kind=<one of the above>`; unknown value → authoring error.

## Fixture format additions

```json
{
  "spans": [
    { "name": "root", "parentIndex": -1, "status": "Error", "kind": "SPAN_KIND_SERVER" }
  ]
}
```

- `parentIndex`: **required on every span** (decoded as `*int`); `-1` = root;
  `0 ≤ i < len(spans)`, `i ≠ selfIndex` = parent; anything else fails loading.
  Omitted on any span (not just span 0) → error, never a silent child-of-span-0:
  `filestore: span 1 ("child"): parentIndex is required (use -1 for root)`.
  Out of range (`< -1` or `≥ len`):
  `filestore: span 3 ("checkout"): parentIndex 99 out of range [0,5) (use -1 for root)`.
  Self-reference:
  `filestore: span 0 ("root"): parentIndex 0 points to itself (use -1 for root)`.
  After parentage is assigned, each span's `parentIndex` chain must terminate at a
  `-1` root; a chain that revisits an index is a cycle (rootless non-forest) and fails:
  `filestore: span 0 ("a"): parentIndex chain does not terminate at a root (cycle detected)`.
- `status` omitted → `Unset`; `kind` omitted → unspecified.

## Failure-mode error contracts (substrings tests may pin)

| Situation | Error contract (shape, not exact text) |
|-----------|----------------------------------------|
| Drive failed, single run | scenario fails; text contains the wrapped driver error (`shell: exec ...`) |
| Trace never arrived | scenario fails; text contains `no trace for run "<id>" within <timeout>` |
| Deadline, spans unstable | scenario fails; text names run id, observed span count, stability progress, timeout |
| Search page full | text contains `returned N traces (== limit)` and names `poll.searchLimit` |
| Aggregate over driver-failed sample's boundary field | text names run index/id and advises `r.failed` guard |
| Fixture bad parentIndex | text names span index/name and offending value |
| Derivation impossible | scenario verdict unchanged; report entry carries `DerivationNote` |

## Compatibility

- Existing green scenarios on healthy traces: verdicts unchanged (spec SC-004).
- Existing fixtures: migrated in-repo to canonical spellings; external fixtures
  using OTLP spellings keep working; unknown spellings that previously loaded
  silently now fail loudly (intentional breaking change — it was the bug).
- `@runs(N)` aggregates that never reference boundary fields are unaffected by the
  guarded binding.
