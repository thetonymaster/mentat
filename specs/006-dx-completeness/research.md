# Phase 0 Research: DX & Product Completeness

Per-slice decisions. Grounding: audit cluster E, design docs §6/§10/§15/§17,
current code.

## R1. Step reference source of truth (E1)

**Decision**: Extend the step-registration table in `internal/steps` so each
registration carries metadata: `{pattern, summary, argGrammar, example}` in a
single slice literal that both `RegisterSteps` and the reference generator
consume. `mentat steps` prints it (grouped by category); a `go:generate`d
`docs/steps.md` commits the same rendering; a drift test asserts every
registered pattern has metadata and the committed doc is regeneration-clean.
Selector/quantifier/ordinal grammar and CEL variables get hand-written sections
in the same generator (they are grammar, not steps — but live beside the code
that parses them).

**Rationale**: one table → CLI and doc cannot drift from code; the drift test
makes missing metadata a CI failure, satisfying the spec's generated-never-listed
edge case.

**Alternatives**: reflection over godog's registered handlers — godog does not
expose doc metadata; hand-written docs page — the drift the audit found in the
design docs, institutionalized.

## R2. Validate orchestration (E2)

**Decision**: `mentat validate [paths...]` builds the engine with a **noop
store and driver** (never contacted), parses features via godog's
dry-run-equivalent (`godog` suite with a no-op test runner is unreliable —
instead reuse the scenario-init prechecks directly): parse feature files with
the gherkin parser, walk pickles, check every step against the registration
table (binding), run CEL precompile, shape-pattern resolution, expectations
load, and target-name checks from config. Findings accumulate (file:line,
class, message); exit 1 if any; `--format json` for CI.

**Rationale**: the prechecks exist as functions today (steps.go scenario-init);
validate is orchestration + a gherkin parse, honoring "reuse, not new logic".
Judge/store are structurally validated only (config shape), never called.

## R3. JUnit + console (E3)

**Decision**: use godog's multi-formatter syntax — `Format: "pretty,junit:<file>"`
(comma-separated formats with per-format output redirection, supported since
godog v0.13). `--junit` keeps its meaning (adds the junit formatter) instead of
replacing the format. Reporter-error contract: junit file-open failure keeps
failing the run as today.

## R4. HTTP body steps (E4)

**Decision**: two steps — `When I send the request with body:` (doc-string) and
`When I send the request with body fixture "<path>"` (path relative to the
feature file's dir; absolute allowed). Both set `RunSpec.Input`; the http driver
sends it when non-empty (Content-Type from target config header, unchanged).
Missing fixture → step failure naming the resolved path.

## R5. File store (E5)

**Decision**: register `"file"` in `BuildStore` with config
`store: file` + `storePath: <dir>`. Implementation: the existing
`InMemStore`/`LoadFixture` machinery becomes a directory-backed store — `Query`
scans the dir for fixtures tagged with the run id (fixture format already
records it via WriteFixture), `GetByID` loads by id; absent → the same
descriptive not-found errors as Tempo. `@runs(N>1)` against the file store:
**hard error** ("file store serves one recorded sample per run id; multi-run
requires a live store") — deterministic replays cannot fabricate independent
samples (constitution IV; the spec left this to plan).

**Rationale**: closes the write-only loop with the format that already exists;
the multi-run hard error prevents silently-correlated "samples" from feeding
statistics.

**Coordination**: consumes feature 002's canonical status/kind fixture
vocabulary — file store lands after 002's fixture migration or includes it.

## R6. Judge ledger, budget, defaults (E6)

**Decision**:
- Ledger: `core.JudgeUsage{Calls, InputTokens, OutputTokens, CostUSD, Model}`
  captured per call in the claude judge (SDK usage field), aggregated per
  scenario through the semantic matcher's Verdict detail, summed by the
  collector; rendered in JSON (`judge` object per scenario + suite total) and
  HTML.
- Budget: `judge.max_cost_usd` config; the collector checks after each
  scenario; exceeded → suite aborts with
  `judge budget exceeded: spent $X.XX of $Y.YY after scenario "..."`
  (completed-call accounting per spec).
- Cost: model→pricing via the existing pricing-table mechanism (same
  ambiguous-model hard-error rules as SUT cost).
- Defaults: default judge model moves to the current fast tier (Haiku-class,
  exact id pinned in config constants at implementation time against the live
  price sheet — recorded in the contract doc); `votes > 1 && temperature == 0`
  → hard config error at load with the two remedies named (raise temperature or
  drop votes). No auto-diversification (silent behavior change — IV smell).

**Rationale**: binary match/no-match at temperature 0 gains nothing from the
top tier (audit's 10–30× cost estimate); loud config error beats silently
choosing a temperature the user didn't set.

## R7. mentatctl surface (E7)

**Decision**: summary gains tokens/cost/latency/trace-ids — all already
computable from `ev.Trace` (FormatTools computes tokens/cost today; latency =
trace envelope; ids = root trace ids). Flags: `--prompt-file` (or `-` = stdin),
`-o <file>` (answer only), `--timeout` (per-invocation resolve override).
Existing output format is preserved as a prefix (additive lines), keeping
script compatibility.

## R8. Answer extraction (E8)

**Decision**: per-target config block:
`extract: {mode: whole|marker|pattern, marker: "ANSWER:", pattern: "..."}`
(default whole). Applied in the driver layer where `ExtractAnswer` runs today;
marker = text after last occurrence, trimmed; pattern = first capture group;
no-match → run failure naming marker/pattern (never silent whole-output).
`core.ExtractAnswer` becomes policy-parameterized (pure function; table-tested).

## R9. Prebuilt lab SUTs + e2e conventions (E9)

**Decision**: `make harness-up` (and a new `make labs`) builds
`bin/researchbot`, `bin/orderflow`, etc. via Go build with source-dependency
tracking (Make prerequisites on `**/*.go`); `mentat.yaml` and e2e configs point
at the binaries. The two report-meta tests switch to `mentatBin` +
`t.Parallel()` (top and subtests) per the repo rule.
