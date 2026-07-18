# Writing a custom Driver

A **Driver** is Mentat's SUT-driving seam: it invokes your system-under-test for
one run and returns what the SUT produced. Mentat ships shell and HTTP drivers;
implement this interface to drive anything else (a message queue, a custom RPC,
an in-process agent) *without forking* — you register it under an adapter name
and it works in `mentat.yaml` and feature files exactly like a built-in.

Everything below is reachable through the public `github.com/thetonymaster/mentat`
facade alone — no `internal/...` import is required (and CI forbids one).

## The contract

```go
type Driver interface {
	Run(ctx context.Context, spec mentat.RunSpec) (mentat.RunResult, error)
}
```

The engine calls `Run` once per run with a fully populated `mentat.RunSpec`:

| Field           | Meaning                                                            |
|-----------------|-------------------------------------------------------------------|
| `RunID`         | the correlation id the engine injected for **this** run           |
| `Tags`          | correlation tags (`test.run.id`, `test.scenario`, `test.case`)    |
| `Target`        | the target name from config                                       |
| `Adapter`       | your registered adapter name                                      |
| `Command`       | the target's `command` plus the step args (e.g. `--scenario foo`) |
| `Input`         | the prompt / request body (empty for scenario/prompt steps)       |
| `Env`           | environment to pass to a spawned SUT (e.g. the OTLP endpoint)     |
| `KillGrace`     | grace period before a forceful kill of a spawned process tree     |

`Run` returns a `mentat.RunResult`:

```go
type RunResult struct {
	RunID          string
	PrimaryTraceID string // reserved; leave unset under baggage-only correlation
	Output         mentat.Output
}
```

## Implementer obligations

These are contract requirements, not style preferences. The seam guides are
reviewed against the [constitution](../../.specify/memory/constitution.md).

1. **Tag-first correlation is your job (Constitution II).** The engine has
   already injected `spec.RunID` (and `spec.Tags`) *before* it calls you. You
   MUST make the SUT's OpenTelemetry trace discoverable by that run id — inject
   it as the `test.run.id` resource attribute (via `OTEL_RESOURCE_ATTRIBUTES` for
   a spawned agent, baggage for HTTP/gRPC) so the store can later resolve the run
   by querying that tag. A driver that drops `spec.RunID` produces traces the
   correlator can never find. Echo `spec.RunID` back in `RunResult.RunID`.

2. **No silent fallbacks (Constitution IV).** If `Run` cannot do its job, return
   a wrapped, descriptive error — never a zero-value `RunResult` that looks like
   success. Wrap the underlying cause with `%w` and name the concrete failure and
   the value involved: `fmt.Errorf("kafkaecho: publish run %q: %w", spec.RunID, err)`.
   A missing required input (e.g. an empty `spec.RunID`) is a hard error, not a
   guess.

3. **Return a populated `Output`.** Comparators read `Evidence.Output` only
   (Constitution I). Fill `Output.Answer` with the extracted result the behaviour
   spec asserts on (`the result contains …`); populate `Stdout`/`Status`/`Body`
   when they apply to your transport. An empty `Output` on a successful run is a
   silent failure of the assertion contract.

4. **Honour `ctx`.** Stop work when the context is cancelled — the engine bounds
   each run with the scenario/budget deadline and cancels on interruption.

## Registration

Register your driver under an adapter name at the one composition root — the
`mentat.Run` call — via `mentat.WithDriver`. A factory builds the driver from the
resolved `Config`; a name already taken by a built-in or an earlier registration
is a **loud** collision error, never a silent last-wins overwrite (FR-002).

```go
res, err := mentat.Run(ctx, cfg,
	mentat.WithFeatures("testdata/echo.feature"),
	mentat.WithDriver("kafkaecho", func(mentat.Config) (mentat.Driver, error) {
		return kafkaecho.NewDriver(bus), nil
	}),
)
```

Config then references the adapter by name:

```yaml
targets:
  bot:
    adapter: kafkaecho
    command: ["noop"]
```

## Walkthrough: the `kafkaecho` example

[`examples/kafkaecho`](../../examples/kafkaecho) is a standalone module (its own
`go.mod`) that imports **only** the facade. It is the CI-enforced proof the public
surface suffices — a toy driver for a message-queue SUT Mentat does not ship.

Its `Driver.Run` (see `driver.go`):

1. **Guards the contract** — an empty `spec.RunID` is a hard error (obligation 2),
   because without it correlation is impossible.
2. **Derives the answer** by echoing the scenario input (`pong: <scenario>`),
   standing in for a real request/response over the queue.
3. **Emits a trace keyed on `spec.RunID`** into a shared in-memory bus
   (obligation 1) — the stand-in for exporting an OTLP trace tagged with
   `test.run.id`.
4. **Returns a populated `Output`** with `Answer` (and `Stdout`) set to the echo
   (obligation 3), and echoes `spec.RunID` in `RunResult.RunID`.

The paired custom `TraceStore` (see `store.go`) serves that same bus back to the
correlator by run id, so a fully custom driver + store pair drives the feature
green end to end — no Tempo, no network. The store side is documented in the
[store guide](store.md); for a driver, the load-bearing lesson is obligations 1–3
above. The `Output` a driver returns becomes the `Evidence.Output` comparators
read — see the [Evidence primer](evidence.md).
