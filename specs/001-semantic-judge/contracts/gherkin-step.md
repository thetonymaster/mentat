# Contract — `the result means` Gherkin step

## Grammar (registered in `internal/steps/steps.go`)

```gherkin
Then the result means "<expected meaning>"
And  the result means:
  """
  <expected meaning, multi-line>
  """
```

- Inline regex: `^the result means "([^"]*)"$` → `w.resultMeans(s)`
- Docstring regex: `^the result means:$` → `w.resultMeansDoc(doc)`

## Behavior
- Builds `comparator.ResultExpectation{Matcher: "semantic", Want: <meaning>}` and calls
  `w.check("result", exp)` — identical path to `the result contains/equals/matches regex`.
- The result comparator resolves the `"semantic"` matcher from the registry and dispatches —
  **zero change to `result.go`**.
- Candidate = `Evidence.Output.Answer` (the boundary result). Target selection is not used by
  the semantic matcher in v1 (final answer only).

## Pass / fail / error
| Outcome | Result |
|---|---|
| Judge majority `match=true` | step passes |
| Judge majority `match=false` | step fails; failure carries the judge's reason (FR-008) |
| Empty expected meaning (`""` / blank docstring) | fail-fast at scenario start (FR-013) |
| Judge backend failure (auth/transport/malformed/refusal) | hard error, not a verdict (FR-007) |
| Even-N vote tie | hard error (FR-015) |
| Used under `@runs(N>1)` | existing `world.check` guard hard-errors → directs to `the runs satisfy` (US4 boundary; US4 deferred) |

## Acceptance mapping
- US1-AC1/AC2/AC3, US2-AC1/AC2/AC3 are all expressible through this step against a
  gomock/fake Judge.
