# Writing a custom Judge

A **Judge** is Mentat's semantic-verdict seam: it renders a single pass/fail verdict
over two strings — a candidate answer and the expected meaning. It backs the
`the result means "…"` result matcher. Mentat ships a Claude backend; implement this
interface to grade by meaning through a different model or service *without forking*
— you register it under a backend name at the `mentat.Run` call.

Everything below is reachable through the public `github.com/thetonymaster/mentat`
facade alone — no `internal/...` import is required (and CI forbids one).

## The contract

```go
type Judge interface {
	Judge(ctx context.Context, req mentat.JudgeRequest) (mentat.JudgeVerdict, error)
}

type JudgeRequest struct {
	Candidate string // the run's result content (Evidence.Output.Answer)
	Expected  string // the author's expected meaning (from `the result means "..."`)
}

type JudgeVerdict struct {
	Match  bool
	Reason string           // human-readable rationale; flows into Verdict.Reasons on a fail
	Usage  mentat.JudgeUsage // this call's token ledger (Calls=1) with the judge model id
}
```

The `Judge` receives **no** `Evidence`, `TraceStore`, or `Driver` — only the two
strings. The matcher extracts `Candidate` from `Evidence.Output` and `Expected`
from the expectation before calling you; keeping the judge transport- and
evidence-free is the same portability boundary comparators observe (Constitution I).

`mentat.JudgeUsage` is the summable token-ledger row the cost report prices:

```go
type JudgeUsage struct {
	Calls        int
	InputTokens  int64
	OutputTokens int64
	CostUsd      float64 // left 0 by the judge — priced later at the report boundary
	Model        string  // the judge model id (required so pricing is per-model)
}
```

## Implementer obligations

These are contract requirements, not style preferences. The seam guides are
reviewed against the [constitution](../../.specify/memory/constitution.md).

1. **Classify API errors — never guess a verdict (Constitution IV).** A transport,
   auth, rate-limit, or malformed-response failure is a wrapped, descriptive
   `error`, **never** a fabricated `JudgeVerdict{Match: true}` or
   `{Match: false}`. Wrap the cause with `%w` and name it —
   `fmt.Errorf("myjudge: call model %q: %w", model, err)`. A guessed PASS/FAIL is
   the single worst failure for a framework whose job is to be trusted when it says
   PASS or FAIL; a real API failure must surface as a hard error, not a silent
   verdict.

2. **Stay evidence- and transport-free (Constitution I).** Decide only from
   `req.Candidate` and `req.Expected`. Do not reach for a store, a driver, or the
   run's trace — the judge sees strings by design.

3. **Report real usage, never fabricated zeros.** On a successful metered call, fill
   `JudgeVerdict.Usage` with the actual `InputTokens`/`OutputTokens` and the judge
   `Model` id (leave `Calls` at 1 for a single call; leave `CostUsd` 0 — the report
   prices it per-model at the render boundary). Leave `Usage` at its zero value on
   non-metered or error paths — absence of usage is not fabricated zero usage
   (judge-ledger contract, FR-006).

## Registration

Register your judge under a backend name at the one composition root — the
`mentat.Run` call — via `mentat.WithJudge`. A factory builds it from the resolved
`Config`. The judge is **used** only when `cfg.Judge.Backend` names it (like the
built-in `claude` backend), but the **name-collision check runs unconditionally** —
a name already taken by a built-in or an earlier registration is a loud collision
error whether or not any scenario ends up selecting it (FR-002).

```go
res, err := mentat.Run(ctx, cfg,
	mentat.WithFeatures("testdata/means.feature"),
	mentat.WithJudge("myjudge", func(mentat.Config) (mentat.Judge, error) {
		return newMyJudge(), nil
	}),
)
```

Config then selects the backend by name:

```yaml
judge:
  backend: myjudge
  model: my-model-v1
```

## Walkthrough: a conceptual sketch

There is no shipped custom-judge example, so the shape is a short sketch rather than
a runnable module. A judge calls its model, maps the response to `Match`/`Reason`,
and classifies any failure as an error:

```go
type myJudge struct{ client *someAPIClient }

func (j myJudge) Judge(ctx context.Context, req mentat.JudgeRequest) (mentat.JudgeVerdict, error) {
	resp, err := j.client.Grade(ctx, req.Candidate, req.Expected)
	if err != nil {
		// Obligation 1: an API failure is a hard error, never a guessed verdict.
		return mentat.JudgeVerdict{}, fmt.Errorf("myjudge: grade: %w", err)
	}
	// Obligation 3: report the real token usage and model id.
	return mentat.JudgeVerdict{
		Match:  resp.Equivalent,
		Reason: resp.Rationale,
		Usage: mentat.JudgeUsage{
			Calls:        1,
			InputTokens:  resp.InputTokens,
			OutputTokens: resp.OutputTokens,
			Model:        "my-model-v1",
		},
	}, nil
}
```

The candidate string a judge grades comes from `Evidence.Output.Answer`, described
in the [Evidence primer](evidence.md).
