# Writing a custom TraceStore

A **TraceStore** is Mentat's trace-backend seam: it resolves a run's trace forest
by the correlation tag the driver injected, and hands it back to the correlator.
Mentat ships a Tempo store and a file-replay store; implement this interface to
serve traces from anything else (an in-memory bus, a different tracing backend, a
saved-fixture format) *without forking* — you register it under a store name and
`store: <name>` in `mentat.yaml` selects it exactly like a built-in.

Everything below is reachable through the public `github.com/thetonymaster/mentat`
facade alone — no `internal/...` import is required (and CI forbids one).

## The contract

```go
type TraceStore interface {
	FetchPayload(ctx context.Context, id string) ([]byte, error)
	DecodePayload(id string, payload []byte) (*mentat.Trace, error)
	Query(ctx context.Context, q mentat.TraceQuery) ([]mentat.TraceRef, error)
	Caps() mentat.StoreCaps
}
```

The correlator drives these four methods together, tag-first:

| Method          | Role                                                                         |
|-----------------|-----------------------------------------------------------------------------|
| `Query`         | resolve a `mentat.TraceQuery` (`Tag`/`Value`, e.g. `test.run.id=<runID>`) to the matching `[]mentat.TraceRef` |
| `FetchPayload`  | return the raw payload bytes for one trace id — the per-round change-detection signal of the stability poll |
| `DecodePayload` | decode bytes previously returned by `FetchPayload` for the **same** id into a `mentat.Trace` forest |
| `Caps`          | advertise capabilities via `mentat.StoreCaps` (`StructuralQuery`) so the engine knows what the store can answer |

`mentat.TraceQuery` is `{ Tag, Value string }`; `mentat.TraceRef` is
`{ TraceID string }`; `mentat.StoreCaps` is `{ StructuralQuery bool }`.

## Implementer obligations

These are contract requirements, not style preferences. The seam guides are
reviewed against the [constitution](../../.specify/memory/constitution.md).

1. **Complete-or-loud (Constitution IV).** Return the *whole* trace forest or a
   wrapped, descriptive error — never a silently-empty store that looks like a
   clean but traceless run. `FetchPayload` for a trace it cannot produce bytes for
   returns an error, **never `(nil, nil)`**; `DecodePayload` on malformed bytes
   returns an error naming the id. Wrap the underlying cause with `%w` and name the
   value: `fmt.Errorf("mystore: fetch trace %q: %w", id, err)`. A partial or empty
   forest presented as success silently corrupts every comparator's evidence.

2. **Forest semantics (Constitution II).** A run may span **≥1 root trace**
   (multi-turn, sub-agent, fan-out), so `DecodePayload` returns a `mentat.Trace`
   whose `Roots` is a slice — populate every root you decoded. Never collapse the
   result to a single root or drop disjoint roots; assuming one root silently drops
   evidence.

3. **Tag-first resolution (Constitution II).** `Query` resolves the correlation
   tag the driver injected — the engine issues `test.run.id=<runID>` and your store
   keys on that value. A trace that has not been ingested yet is a **normal poll
   state**: return an empty `[]mentat.TraceRef` (not an error) so the correlator
   retries. That is distinct from `FetchPayload`'s hard miss — an id you claimed via
   `Query` but then cannot fetch is a loud error (obligation 1).

4. **A self-consistent payload (feature 004, no partial-evidence window).**
   `FetchPayload` MUST be byte-identical across calls for a fixed trace (the
   stability poll hashes it to detect change; a store with no wire payload should
   emit a deterministic canonical serialization — `json.Marshal` sorts map keys).
   `DecodePayload` MUST decode only the bytes it is handed and MUST NOT re-fetch:
   the hashed bytes and the decoded bytes are the same fetch.

## Registration

Register your store under a name at the one composition root — the `mentat.Run`
call — via `mentat.WithStore`. A factory builds the store from the resolved
`Config`; a name already taken by a built-in or an earlier registration is a
**loud** collision error, never a silent last-wins overwrite (FR-002).

```go
res, err := mentat.Run(ctx, cfg,
	mentat.WithFeatures("testdata/echo.feature"),
	mentat.WithStore("kafkaecho", func(mentat.Config) (mentat.TraceStore, error) {
		return kafkaecho.NewStore(bus), nil
	}),
)
```

Config then selects the store by name:

```yaml
store: kafkaecho
```

## Walkthrough: the `kafkaecho` example

[`examples/kafkaecho`](../../examples/kafkaecho) is a standalone module (its own
`go.mod`) that imports **only** the facade. Its `Store` (see `store.go`) is the
paired half of the driver walkthrough: the driver publishes a forest keyed on the
injected `spec.RunID` onto a shared in-memory bus, and the store serves that same
forest back to the correlator by run id — a fully custom driver + store pair drives
the feature green end to end, with no Tempo and no network.

Its four methods (see `store.go`):

1. **`Query`** resolves `q.Value` (the `test.run.id`) against the bus. A miss
   returns an empty ref set, not an error (obligation 3) — "not yet ingested" is a
   normal poll state the correlator retries.
2. **`FetchPayload`** returns a canonical JSON serialization of the stored forest.
   It is byte-identical across calls for a fixed trace so the stability poll
   converges (obligation 4), and an absent id is a hard error, never `(nil, nil)`
   (obligation 1).
3. **`DecodePayload`** unmarshals those bytes back into a `mentat.Trace` forest
   without consulting the bus (obligation 4).
4. **`Caps`** advertises no structural-query support — this store resolves by run
   id only.

The driver side is documented in the [driver guide](driver.md); the trace forest
your `DecodePayload` produces is the evidence comparators inspect, described in the
[Evidence primer](evidence.md).
