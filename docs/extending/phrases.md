# Contributing Gherkin phrases from a comparator

A comparator can declare the Gherkin sentences that invoke it, so a feature file reads
in your domain language:

```gherkin
Then the revenue floor is 4 USD
```

instead of naming a registry key and handing it a payload:

```gherkin
Then the "revenue-shape" comparator is satisfied by:
  """
  {"min": 4, "currency": "USD"}
  """
```

Both routes remain supported and produce **identical verdicts** — the same pass/fail,
the same reason text, the same completeness qualifiers. Contributing a phrase adds an
invocation route, not a second semantics. Start with the generic step; add a phrase
when the sentence is one your team will write often enough that reading it matters.

Everything below is reachable through the public `github.com/thetonymaster/mentat`
facade alone — no `internal/...` import is required (and CI forbids one).

## The two seams

Both are **optional** and discovered by type assertion. A comparator that implements
neither keeps working exactly as it does today.

```go
// Declares the sentences.
type PhraseContributor interface {
	ContributedPhrases() []mentat.ContributedPhrase
}

// Turns a sentence's regex captures into your own Expectation.
type CaptureParser interface {
	ParseCaptures(caps []string) (mentat.Expectation, error)
}
```

`CaptureParser` is the **sibling** of 011's `ExpectationParser`, not its replacement:
one receives a docstring body, the other the captures of the sentence that matched. A
comparator may implement either, both, or neither.

```go
func (c *revenueShape) ContributedPhrases() []mentat.ContributedPhrase {
	return []mentat.ContributedPhrase{{
		Pattern: `^the revenue floor is (\d+) (\w+)$`,
		Group:   "Revenue",
		Summary: "Asserts the revenue floor is reachable in the named currency.",
		Example: `Then the revenue floor is 4 USD`,
	}}
}

func (c *revenueShape) ParseCaptures(caps []string) (mentat.Expectation, error) {
	floor, err := strconv.Atoi(caps[0])
	if err != nil {
		return nil, fmt.Errorf("revenue-shape: parsing floor %q: %w", caps[0], err)
	}
	return revenueExpectation{Min: floor, Currency: caps[1]}, nil
}
```

Register the comparator exactly as before — `mentat.WithComparator("revenue-shape", …)`.
There is no separate phrase registration, and `ContributedPhrase` carries no comparator
name: the phrase is resolved *through* the comparator that declared it, so the binding
is structural and there is nothing to keep in sync.

## Which seam serves a phrase

Decided once, at engine build, from the **shape of the pattern** — never guessed at
runtime from what your comparator happens to implement:

| Pattern has captures | Sentence takes a docstring | Seam used |
|---|---|---|
| no | yes | `ExpectationParser` (unchanged from 011) |
| yes | no | `CaptureParser` |
| yes | yes | `CaptureParser`, docstring appended as the final capture |
| no | no | `CaptureParser` with an empty capture list (a constant expectation) |

A pattern ending in `:$` declares that the sentence takes a docstring, which is the
convention every built-in docstring step already follows (`^the run satisfies:$`).

`caps` always contains **exactly** as many entries as your pattern declares capture
groups — never truncated, and an empty capture arrives as `""` rather than being
dropped, so `[]` and `[""]` stay distinguishable.

## Pattern rules

Every rule is enforced when the engine is built, before any scenario runs, and every
error names your comparator and the offending value.

1. **The pattern must compile** as a Go regular expression.
2. **The pattern must be anchored** — it must match a whole sentence and nothing less.
   Two shapes look anchored and are not, and both let your phrase swallow part of a
   neighbouring step (the runner matches unanchored):
   - A trailing **escaped** dollar is a literal: `^the price is 5\$` has no end anchor.
   - Top-level **alternation** binds looser than the anchors:
     `^the alpha reading|the beta reading$` parses as
     `(^the alpha reading)|(the beta reading$)`, and the second branch matches inside
     "I check the beta reading". Put the alternation inside the anchors instead:
     `^the (?:alpha|beta) reading$`. `^a$|^b$` is fine — every branch is anchored.
3. **`Group`, `Summary` and `Example` must be non-blank.** They render in the step
   reference; a blank row is worse than an absent phrase.
4. **No two contributed patterns may be identical.** The error names both contributors,
   because they may come from different modules and you need to know which pair to
   reconcile.
5. **A contributed pattern may not equal a built-in step's.** Built-ins register first,
   so a duplicate would be permanently shadowed and its assertion would never run.

6. **A step must carry exactly the argument its phrase declares.** Checked at scenario
   init, before any SUT is driven, and by `mentat.Validate`. Built-in steps follow the
   same rule, checked the same way — their expected argument is derived from the handler
   each one registers — so this is one rule about Gherkin, not a restriction that applies
   only to your phrases. This is the rule you are most likely to trip, because
   docstring-ness is inferred from that `:$` and the convention is easy to forget:
   - A body on a phrase that declares none would be **silently discarded** by the
     runner and the step would report a verdict that never read it.
   - A **data table** can never be received: `CaptureParser` takes `[]string` and
     `ExpectationParser` takes `string`, so no seam accepts one.

A phrase whose comparator implements no matching parser seam is rejected at build too:
a sentence that can never produce an expectation is an authoring defect, not a runtime
surprise waiting for the first person to write it.

## Collisions anchoring cannot prevent

Anchoring stops a pattern matching *part* of another sentence. It does not stop two
anchored patterns matching the *same* sentence:

```
^the (\w+) reading is fine$
^the alpha reading is fine$
```

Both match `the alpha reading is fine`. Mentat does not attempt to decide regex overlap
in general — instead the runner reports it at match time as a failed scenario naming
**every** matching expression, so you see exactly which patterns to reconcile:

```
ambiguous step definition, step text: the alpha reading is fine
    matches:
        ^the (\w+) reading is fine$
        ^the alpha reading is fine$
```

This matters more than it looks. Without it the first-registered pattern wins silently,
the other never runs, and the scenario reports **passed** — a green verdict nobody
wrote. Built-ins register before contributed phrases, so the silently-swallowed one
would always be yours.

## Scope: phrases belong to one engine

Contributed phrases are resolved per engine and are invisible to every other engine in
the process. Two test suites in one binary, each registering different comparators,
never see each other's sentences. A sentence belonging to another engine is reported as
an undefined step, exactly like a typo.

## Errors your parser returns

- Return a wrapped error and it reaches the author naming your comparator and the
  pattern that failed.
- Returning a **nil expectation with no error is refused**, not trusted. Forwarding it
  would let a comparator that tolerates nil return a passing verdict for a step that
  asserted nothing. A zero-valued but non-nil expectation is legitimate and passes
  through untouched.

## Seeing your phrases

`mentat steps` and `docs/steps.md` list Mentat's **built-in** steps only. A compiled
binary cannot reach comparators registered in your module, so your phrases cannot
appear there — that is structural, not an omission.

Render the reference for your own engine, and validate suites written in your own
sentences, from your own test binary:

```go
// Every step your suite can use: Mentat's built-ins, then your contributed phrases
// under "Extension: <your Group>" headings.
docs, err := mentat.StepReference(ctx, cfg,
	mentat.WithComparator("revenue-shape", newRevenueShape),
)

findings, err := mentat.Validate(ctx, cfg,
	mentat.WithFeatures("features/"),
	mentat.WithComparator("revenue-shape", newRevenueShape),
)
```

`StepReference` returns an error rather than a partial list if any phrase is malformed:
a reference silently missing a phrase sends you looking at your registration when the
defect is in the pattern.

`mentat.Validate` accepts the same options as `mentat.Run`, so it checks your suite
against exactly the engine your run will use. An `error` means validation could not run
(a bad config, a malformed phrase); `findings` mean it ran and the suite has defects.

Running `mentat validate` from the CLI against a suite written in contributed phrases
will report them as unbound steps. Use the library entry point instead.

## Related

- [Writing a custom Comparator](./comparator.md) — the seam a phrase invokes.
- [Adding a new seam](./new-seam.md) — the checklist a new extension point follows.
- [Stability and the public surface](./stability.md) — what the facade guarantees.
