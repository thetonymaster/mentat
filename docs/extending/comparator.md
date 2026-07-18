# Writing a custom Comparator

A **Comparator** is Mentat's behaviour-assertion seam: it inspects one run's
`Evidence` and returns a pass/fail `Verdict`. Mentat ships the sequence, budget,
and result comparators; implement this interface to assert a property the built-ins
do not cover *without forking* — you register it under a name at the `mentat.Run`
call.

Everything below is reachable through the public `github.com/thetonymaster/mentat`
facade alone — no `internal/...` import is required (and CI forbids one).

## The contract

```go
type Comparator interface {
	Name() string
	Compare(ctx context.Context, ev mentat.Evidence, e mentat.Expectation) (mentat.Verdict, error)
}
```

- `Name` returns the registered comparator name.
- `Compare` receives the run's `mentat.Evidence` (the trace forest + driver output)
  and a comparator-specific `mentat.Expectation` (`= any`; type-assert your own
  expectation shape from it), and returns a `mentat.Verdict`:

```go
type Verdict struct {
	Pass    bool
	Reasons []string
	// Detail / Judge are set only by canonical aggregate / judge-backed comparators;
	// a custom comparator leaves them nil.
}
```

## Implementer obligations

These are contract requirements, not style preferences. The seam guides are
reviewed against the [constitution](../../.specify/memory/constitution.md).

1. **Evidence-only (Constitution I — the portability boundary).** A comparator
   consumes `mentat.Evidence` **only** — the `Trace` forest plus the driver
   `Output`. It MUST NOT do I/O, and MUST NOT reach through to a `TraceStore`, a
   `Driver`, or any transport. This boundary is exactly what keeps the same
   behaviour spec portable across an AI-agent SUT and a microservice SUT; the moment
   a comparator fetches data past `Evidence`, it couples the assertion to one store
   or driver and loses that portability. Read `ev.Trace` (walk `Spans`/`Roots`) and
   `ev.Output`; see the [Evidence primer](evidence.md) for the shape and the
   canonical status/kind vocabulary.

2. **A FAIL is a `Verdict`, not an error (Constitution IV).** Return
   `Verdict{Pass: false, Reasons: […]}` for a run that *did not meet the
   assertion* — that is a normal, expected outcome, and the `Reasons` are what the
   report shows. Reserve the returned `error` for a comparator that *cannot do its
   job* (a malformed expectation, a missing required attribute): wrap it with `%w`
   and name the concrete failure and value —
   `fmt.Errorf("mycomparator: expected int threshold, got %T", e)`. Never return a
   zero-value `Verdict` (a silent PASS) when you could not actually evaluate.

3. **Handle failed-run evidence.** On a harness-level run failure `ev.Failed` is
   true and `ev.Trace` is nil (see the [Evidence primer](evidence.md)). A
   comparator that walks `ev.Trace` must guard for that rather than dereference a
   nil forest.

## Registration

Register your comparator under a name at the one composition root — the
`mentat.Run` call — via `mentat.WithComparator`. A factory builds it from the
resolved `Config`; a name already taken by a built-in or an earlier registration is
a **loud** collision error (FR-002).

```go
res, err := mentat.Run(ctx, cfg,
	mentat.WithFeatures("testdata/echo.feature"),
	mentat.WithComparator("mycomparator", func(mentat.Config) (mentat.Comparator, error) {
		return newMyComparator(), nil
	}),
)
```

> **Out of scope for feature 007 (planned as spec 010, not yet started).**
> `WithComparator` publishes the *registration* surface: a custom comparator is
> registered and composes at build today. Actually *invoking* it from a `.feature`
> step, however, needs new Gherkin grammar plus generic expectation parsing. That
> work is planned as spec 010, custom comparator steps; it has not been started, so
> there is no spec document to read yet. A registered comparator is therefore
> composable now but is not yet driven by an authored step — see the "Out of scope"
> note in
> [`specs/007-public-extension-api/tasks.md`](../../specs/007-public-extension-api/tasks.md).
> (Custom **drivers** and **stores** do work end to end today — see
> [`examples/kafkaecho`](../../examples/kafkaecho).)

## Walkthrough: a conceptual sketch

There is no shipped custom-comparator example (the invocation grammar is deferred,
above), so the shape is a short sketch rather than a runnable module. A comparator
reads `Evidence` and returns a `Verdict`:

```go
type myComparator struct{}

func (myComparator) Name() string { return "mycomparator" }

func (myComparator) Compare(_ context.Context, ev mentat.Evidence, e mentat.Expectation) (mentat.Verdict, error) {
	// Obligation 2: a malformed expectation is a loud error, not a silent PASS.
	want, ok := e.(int)
	if !ok {
		return mentat.Verdict{}, fmt.Errorf("mycomparator: expected int, got %T", e)
	}
	// Obligation 3: a failed run carries no Trace.
	if ev.Failed || ev.Trace == nil {
		return mentat.Verdict{Pass: false, Reasons: []string{"run failed; no trace"}}, nil
	}
	// Obligation 1: read the Evidence forest and Output only — no store, no driver.
	got := len(ev.Trace.Spans)
	if got < want {
		return mentat.Verdict{
			Pass:    false,
			Reasons: []string{fmt.Sprintf("expected >= %d spans, got %d", want, got)},
		}, nil
	}
	return mentat.Verdict{Pass: true}, nil
}
```

The `Evidence` this reads — the `Trace` forest, the driver `Output`, and the
failure fields — is described in the [Evidence primer](evidence.md).
