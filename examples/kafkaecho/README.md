# kafkaecho — the public-surface proof

`kafkaecho` is a standalone, third-party-shaped example extension for Mentat. It
is the executable proof that Mentat's **public surface is sufficient to build a
real adapter** — you can write a custom `Driver` and a custom `TraceStore` and run
a feature green without forking Mentat or reaching into its private packages.

## What it actually is

A **separate Go module** — `examples/kafkaecho/go.mod` declares
`github.com/thetonymaster/mentat/examples/kafkaecho` and pulls the framework in via
a `replace github.com/thetonymaster/mentat => ../../`. Because it is its own
module, the repo-root `go test ./...` never reaches it; `make example` is what
covers it in CI (spec 007 SC-001).

Every non-test file imports **only** `github.com/thetonymaster/mentat` (the
facade) plus the standard library. No `mentat/internal/...` anywhere.

| File | What's in it |
| --- | --- |
| `kafkaecho.go` | The shared in-memory `Bus` (a mutex-guarded `map[string]*mentat.Trace`), the `Adapter` / `StoreName` registration constants, and `New()` — which builds a Bus and returns the `mentat.DriverFactory` + `mentat.StoreFactory` that share it. |
| `driver.go` | `Driver`, a `mentat.Driver` that echoes the scenario input back as `"pong: <payload>"` and publishes a one-span trace keyed on the engine-injected `spec.RunID`. |
| `store.go` | `Store`, a `mentat.TraceStore` implementing `FetchPayload`, `DecodePayload`, `Query`, and `Caps` — serving back the forest the driver published, under the same run id. |
| `main_test.go` | `TestKafkaEchoDrivesGreen` — registers both seams through `mentat.WithDriver` / `mentat.WithStore` and drives `testdata/echo.feature` to green via `mentat.Run`. |
| `testdata/echo.feature` | The Gherkin feature the test drives. |

### The narrative

The driver stands in for a message-queue SUT that Mentat does not ship. The `Bus`
stands in for a real OpenTelemetry backend: `Bus.Publish` is the analogue of
exporting a trace tagged `test.run.id=<runID>`, and the store's `Query` is the
analogue of querying that tag. The whole thing is hermetic — no Tempo, no network.

It also demonstrates two architecture invariants rather than just describing them:

- **Tag-first correlation.** The driver keys its emitted trace on exactly the
  `spec.RunID` the engine injected. Drop that and the correlator could never
  resolve the trace.
- **No silent fallbacks.** An empty `RunSpec.RunID` is a hard, descriptive error,
  never a zero-value success. `Store.FetchPayload` errors on an unknown id. (The
  one deliberate non-error is `Store.Query` returning an empty ref set for a
  missing trace — "not yet ingested" is a normal poll state the correlator
  retries, distinct from a hard miss.)

## The internal-import tripwire

The example's value depends entirely on it *not* cheating. `Makefile:32` enforces
that:

```make
! grep -rn --include='*.go' "mentat/internal" examples/
```

A grep match is a successful exit, which the leading `!` negates into a **non-zero
exit** — so CI fails, and because of `-n` the output names the offending
`file:line`. That is the mechanism that stops this example from quietly reaching
into private packages and thereby proving nothing.

## How `make example` gates it

The full target (`Makefile` lines 28–32) runs, in order:

1. `gofmt -l .` inside the module must be empty — otherwise it echoes the
   unformatted files and exits 1.
2. `go vet ./...`, then `go build ./...`, then `go test ./...` — all inside
   `examples/kafkaecho`.
3. `golangci-lint run ./...`, but only if `examples/kafkaecho/.golangci.yml`
   exists (it currently does not, so this step is skipped).
4. The internal-import grep described above, run across all of `examples/`.

`make example` is part of `make ci` (`ci: lint test cover example`).

## Running it yourself

From the repo root:

```bash
make example
```

Or just the test, from inside the module:

```bash
cd examples/kafkaecho && go test ./...
```

## See also

- [`docs/extending/driver.md`](../../docs/extending/driver.md) — the Driver
  contract this example follows.
- [`docs/extending/store.md`](../../docs/extending/store.md) — the TraceStore
  contract.
- [`docs/extending/stability.md`](../../docs/extending/stability.md) — what the
  public surface promises (and what it does not, pre-1.0).
