# Contract: Report output is byte-stable across the reporter rewrite

**Consumers**: everyone parsing a Mentat report file — CI pipelines reading the JSON,
dashboards ingesting JUnit XML.
**Fulfils**: FR-013; SC-009.
**Research**: [research.md R1/R3](../research.md).

## What D5 changed about this contract

**Read this first.** This contract was written when D2/D3 specified facade-owned **mirror**
structs: the reporters would have been re-pointed at a *different* struct that had to
replicate six `omitempty` tags and an exact field order, with nothing in the repo able to
catch a slip. That made this contract the linchpin of the whole feature.

D5 collapses the mirrors into one type, moved to `internal/result` and aliased. The struct
that gets marshalled **is** the same struct, relocated and renamed; Go does not serialize
type names, so the emitted bytes are unchanged *by construction*.

The contract stays, with a smaller job: a **regression guard** against the relocation being
done carelessly — a field reordered while moving it, a tag dropped in transcription, an
`omitempty` lost. Those are ordinary transcription errors, and they are still invisible to
every other check in the repo. What no longer applies is the framing of this contract as the
thing the feature balances on, and the `Results`-vs-`RunReport` fixture port it would have
required.

## Why this contract exists at all

The emitted bytes are a compatibility promise, and at `f2afdda` **nothing protects them**:

- `jsonReporter` calls `json.Encode` on a `core.RunReport` wholesale
  (`internal/report/json.go:13-17`), so the wire format is whatever that struct's field
  names, tags and declaration order happen to be.
- The report types carry six `omitempty` tags (`interrupted`, `judgeTotal`, `qualifiers`,
  `FeatureFile`, `DerivationNote`, `judge`) that must survive the move to `internal/result`
  verbatim, along with declaration order — `encoding/json` emits keys in declaration order.
- The surface golden does not render struct tags — `stability.md` boundary 2, an accepted
  gap. A tag rename produces **zero** golden diff.
- The three committed goldens (`public-surface.golden`, `testdata/golden-hermetic.txt`,
  `cmd/mentat/testdata/golden-green.txt`) cover the public surface and stdout. None covers
  a report file.
- `internal/report/json_test.go` round-trips a `RunReport` through encode/decode. A
  symmetric test cannot detect a renamed tag: both sides move together. Its only literal
  assertion is a count of the `"qualifiers"` key.

So the single most likely way to break users in this feature is also the one the repo would
not notice. Hence a contract rather than a code-review note.

## The requirement

**The bytes each built-in reporter emits MUST be identical before and after this feature** —
field names, serialization tags, key order, and omission behaviour included.

## The ordering requirement

> The check MUST exist and be **green against today's reporters** before any reporter is
> rewritten.

This is the part that carries the value. A golden regenerated after the change records the
new bytes and proves nothing at all. In task terms: the golden is task one of the feature,
not a verification step at the end.

## Shape

Follow the repo's established convention rather than inventing one:

- A committed golden per format, under `internal/report/testdata/`.
- Regenerate with `MENTAT_UPDATE_GOLDEN=1`, matching `surface_test.go:168` and
  `mentat_golden_test.go:28`.
- Render from a **fixed fixture** report built in the test — not a live run — so scenario
  ordering, costs and ids are stable by construction.
- Normalize the nondeterministic tokens. `RunReport.StartedAt` (`time.Time`) and
  `.Duration` (`time.Duration`) are untagged and therefore serialized;
  `normalizeGoldenStdout` (`mentat_golden_test.go:44-46`) is the precedent for replacing
  such a token with a fixed placeholder. Prefer a fixed fixture timestamp where possible and
  normalize only what cannot be fixed.

The fixture MUST exercise the fields whose tags matter — at minimum one scenario with
qualifiers, one with an aggregate detail, one with a derivation note, one with judge usage,
and a multi-run scenario — because an `omitempty` field that is empty in every fixture row
proves nothing about its tag.

## Coverage of the three formats

| Format | Risk | Fixture must include |
|---|---|---|
| json | **Highest** — whole-struct marshal, all six tags in play, key order follows declaration order | every tagged field, both set and unset |
| html | Template reads named fields (`html.go:18-31`); a renamed Go field breaks the template loudly, but a *reordered* one does not | a multi-run scenario, an aggregate, qualifiers |
| junit | Reads `Total`, `Reasons`, `Qualifiers` (`junit.go:51,60,64-65`) | a failing scenario with reasons, a qualified scenario |

## Relationship to the surface gate

These goldens cover what the surface gate deliberately does not (boundary 2). They are
complementary: the surface gate freezes the Go-level field names and order; these freeze
the wire-level keys and order. Neither subsumes the other, and this feature is the first
time the repo needs both.

## Falsification

Prove the golden works by breaking it deliberately, in the test file's own record:

1. Rename one json tag on a facade mirror → the format golden fails, naming the format.
2. Reorder two fields on `ScenarioResult` → the json golden fails on key order.
3. Revert both → green.

Step 2 matters most: it is the failure the surface gate *would* catch as a diff but which a
reviewer could wave through as cosmetic, without realizing it reorders every user's JSON.
