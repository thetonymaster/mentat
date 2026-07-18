# The Evidence a comparator inspects

**Evidence** is everything Mentat lets a comparator see about a single run: the
run's OpenTelemetry `Trace` forest plus the driver-captured `Output`. It is the
input to the [Comparator](comparator.md) contract and the portability boundary that
keeps a behaviour spec grading an AI-agent SUT and a microservice SUT alike
(Constitution I). This is a primer on that value — not a seam with a registration
hook — because it is the shared vocabulary the comparator guide builds on.

Everything below is reachable through the public `github.com/thetonymaster/mentat`
facade alone — no `internal/...` import is required (and CI forbids one).

## The shape

```go
type Evidence struct {
	RunID  string
	Trace  *mentat.Trace
	Output mentat.Output
	// Harness-level failure of this run (driver invocation or trace resolution).
	Failed      bool
	FailureKind string
	FailureMsg  string
}
```

A comparator reads `Trace` and `Output`; the three failure fields tell it when a run
never produced usable evidence.

## The `Trace` forest

`Evidence.Trace` is a **forest** — a single run MAY span more than one root trace
(multi-turn, sub-agent, fan-out). Never assume a single root (Constitution II):

```go
type Trace struct {
	RunID string
	Roots []*mentat.Span // one or more roots
	Spans []*mentat.Span // every span in the forest, flat
}
```

Walk `Spans` for a flat scan, or `Roots` to descend the forest. Each span:

```go
type Span struct {
	ID       string
	ParentID string
	Name     string
	Kind     string            // canonical kind vocabulary (below)
	Status   string            // canonical status vocabulary (below)
	Start    time.Time
	End      time.Time
	Attrs    map[string]string // e.g. gen_ai.* attributes
}
```

`Span` also offers `Attr(k)`, `AttrInt(k)`, and `AttrFloat(k)` accessors over
`Attrs`, and `Trace` offers `ByOp(op)` (spans by `gen_ai.operation.name`) and
`Envelope()` (the run's wall-clock span).

## The driver `Output`

`Evidence.Output` is the boundary result the driver captured for the run:

```go
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int    // shell adapters
	Status   int    // http adapters (HTTP status)
	Body     []byte // http adapters
	Answer   string // the extracted result (what `the result contains …` asserts on)
}
```

`Answer` is the field the result matchers grade; `Stdout`/`ExitCode` apply to shell
transports and `Status`/`Body` to HTTP transports.

## Failure evidence

When a run fails at the harness level, `Evidence.Failed` is true, `FailureMsg`
carries the wrapped error text, and `FailureKind` classifies which engine call
failed:

| `FailureKind`                | Meaning                    | `Trace` | `Output`                       |
|------------------------------|----------------------------|---------|--------------------------------|
| `mentat.FailureKindDriver`   | driver invocation failed   | nil     | zero                           |
| `mentat.FailureKindResolve`  | trace resolution failed    | nil     | **retained** (driver succeeded)|
| `""` (empty)                 | not failed                 | present | present                        |

A failed run carries **no `Trace`**, so a comparator that walks `ev.Trace` must
guard `ev.Failed`/`ev.Trace == nil` before dereferencing it. On a `resolve` failure
the real driver `Output` is still present (the driver succeeded); on a `driver`
failure the `Output` is zero.

## The canonical vocabulary

Store decoders normalize every span's status and kind onto a fixed vocabulary, so a
comparator compares only these canonical values (never a raw wire spelling):

**Status** — `Span.Status`:

| Constant             | Value     |
|----------------------|-----------|
| `mentat.StatusUnset` | `"Unset"` |
| `mentat.StatusOk`    | `"Ok"`    |
| `mentat.StatusError` | `"Error"` |

**Kind** — `Span.Kind`:

| Constant                  | Value                  |
|---------------------------|------------------------|
| `mentat.KindInternal`     | `"SPAN_KIND_INTERNAL"` |
| `mentat.KindServer`       | `"SPAN_KIND_SERVER"`   |
| `mentat.KindClient`       | `"SPAN_KIND_CLIENT"`   |
| `mentat.KindProducer`     | `"SPAN_KIND_PRODUCER"` |
| `mentat.KindConsumer`     | `"SPAN_KIND_CONSUMER"` |
| `mentat.KindUnspecified`  | `""` (empty string)    |

Compare against these constants, not string literals, so an assertion stays correct
across the fixture and live-Tempo wire spellings the store normalizes.

## See also

- [Writing a custom Comparator](comparator.md) — the seam that consumes this Evidence.
- [Writing a custom TraceStore](store.md) — the seam whose `DecodePayload` produces
  the `Trace` forest.
