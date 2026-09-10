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

Registration alone makes a comparator *composable*. To make it reachable from a
`.feature` file, implement `ExpectationParser` as well — see the next section.

## Driving your comparator from a feature file

A registered comparator is invoked by name through one generic step:

```gherkin
Then the "mycomparator" comparator is satisfied by:
  """
  { "minSpans": 3 }
  """
```

The quoted name is resolved through the same per-engine registry the built-in steps
use. The docstring is handed to your comparator's own `ExpectationParser`, which turns
it into whatever type your `Compare` expects:

```go
// ExpectationParser is OPTIONAL and separate from Comparator. Implementing it is
// what makes a comparator Gherkin-drivable; omitting it changes nothing.
type ExpectationParser interface {
	ParseExpectation(text string) (Expectation, error)
}
```

Four things are worth knowing before you implement it:

1. **The text arrives verbatim.** Mentat does not trim, dedent or normalize the
   docstring — whitespace may be significant to your format. An *empty* docstring is
   passed through, because whether empty is valid is your decision, not the step's. A
   *missing* docstring is a malformed step and is rejected before your parser is called.
2. **It takes text and nothing else** — no `context.Context`, no target, no `Evidence`.
   `Evidence` is the single channel through which a comparator sees run data
   (obligation 1), and a parser able to reach run context would be a second, weaker one.
   The narrow signature also makes the parser trivially unit-testable.
3. **You own the round trip.** If `ParseExpectation` returns a type your own `Compare`
   does not accept, that surfaces as your comparator's own type-assertion error.
   Mentat does not police it — doing so would require Mentat to know your expectation
   type, which is exactly the coupling this seam removes.
4. **The step is always completeness-sensitive.** Against a bounded (request-scoped,
   non-strict) target, a verdict from this step carries the ingestion-window qualifier.
   The step cannot know your comparator's sensitivity, and the errors are asymmetric:
   over-qualifying adds a visible caveat, under-qualifying produces an unsound green.

Three things fail loudly rather than silently (no fallbacks, no skipped assertions):
a name that is not registered — the error lists the names that *are*; a comparator that
does not implement `ExpectationParser`; and a parser that returns an error, which is
wrapped and surfaced rather than converted into a failing verdict, because a parse
failure is not an assertion failure.

## Walkthrough: a complete comparator

A comparator reads `Evidence` and returns a `Verdict`:

```go
// myExpectation is YOUR type. Mentat never names it.
type myExpectation struct {
	MinSpans int `json:"minSpans"`
}

type myComparator struct{}

func (myComparator) Name() string { return "mycomparator" }

// ParseExpectation makes this comparator Gherkin-drivable. Text in, your type out.
func (myComparator) ParseExpectation(text string) (mentat.Expectation, error) {
	var exp myExpectation
	if err := json.Unmarshal([]byte(text), &exp); err != nil {
		return nil, fmt.Errorf("mycomparator: parsing expectation %q: %w", text, err)
	}
	if exp.MinSpans < 0 {
		return nil, fmt.Errorf("mycomparator: minSpans must be >= 0, got %d", exp.MinSpans)
	}
	return exp, nil
}

func (myComparator) Compare(_ context.Context, ev mentat.Evidence, e mentat.Expectation) (mentat.Verdict, error) {
	// Obligation 2: a malformed expectation is a loud error, not a silent PASS.
	exp, ok := e.(myExpectation)
	if !ok {
		return mentat.Verdict{}, fmt.Errorf("mycomparator: expected myExpectation, got %T", e)
	}
	want := exp.MinSpans
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

Registered and driven together:

```go
res, err := mentat.Run(ctx, cfg,
	mentat.WithFeatures("testdata/spans.feature"),
	mentat.WithComparator("mycomparator", func(mentat.Config) (mentat.Comparator, error) {
		return myComparator{}, nil
	}),
)
```

```gherkin
Feature: span floor
  Scenario: the agent emits enough spans
    Given the agent target "bot"
    When I run scenario "any"
    Then the "mycomparator" comparator is satisfied by:
      """
      { "minSpans": 3 }
      """
```

The `Evidence` this reads — the `Trace` forest, the driver `Output`, and the
failure fields — is described in the [Evidence primer](evidence.md).
