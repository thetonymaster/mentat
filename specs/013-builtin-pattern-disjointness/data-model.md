# Data Model: Built-in Step-Pattern Disjointness

**Feature**: 013-builtin-pattern-disjointness | **Date**: 2026-09-11

Types are named as proposals; TDD owns the final signatures. What is *not* negotiable is marked,
because each such point is a requirement or a measured constraint rather than a preference.

---

## 1. `LabelledPattern` — a pattern plus where it came from

```go
type PatternSource int

const (
    SourceBuiltin PatternSource = iota
    SourceContributed
)

type LabelledPattern struct {
    Pattern    string        // the regex source, as registered
    Source     PatternSource
    Comparator string        // "" for built-ins; the contributing comparator's name otherwise
}
```

**Why it exists**: `StepPatterns` is `[]*regexp.Regexp` and has thrown away both the pattern
*string* and its provenance by the time a check sees it. A `pattern-overlap` message must name the
contributing comparator to be actionable, and the Validate-time check must be able to tell
built-in × built-in pairs apart from the rest so it can skip them (R6).

**Validation rules**:
- `Comparator` MUST be non-empty when `Source == SourceContributed`, and empty otherwise. This is
  an invariant of construction, not a runtime check — the two call sites know which they are
  building.
- Order is significant and MUST be registration order: built-ins first, in `stepDefs` table order,
  then contributed phrases in resolution order. Registration order is what decides which
  definition godog binds, so a report that lists patterns out of order would misdescribe the
  runtime (`Key Entities`, spec).

---

## 2. `Verdict` — the decider's answer

```go
type Verdict struct {
    Intersects bool
    Witness    string // non-empty iff Intersects; a string both patterns match
}
```

**NOT NEGOTIABLE** (FR-011): `Intersects == true` requires a `Witness`, and the witness MUST be
re-verified against both compiled patterns before it is reported. A positive verdict proves
itself; that asymmetry is the whole reason the decider is trustworthy in one direction for free.

**The negative direction has no such proof** and must never be described as one. `Intersects ==
false` means "this code found no shared string", which is why US3 exists (D4, R3).

**Validation rules**:
- `Witness == "" && Intersects == true` is an invariant violation — the empty string is a legal
  witness only when both patterns match the empty string, so the check is on the verification, not
  on emptiness. Verify by `MatchString` on both, never by inspecting the witness.
- A `Verdict` is meaningless without its error being nil. See §3.

---

## 3. Decider errors — the refusal path

```go
func Intersects(a, b string) (Verdict, error)
```

**NOT NEGOTIABLE** (FR-012, Constitution IV): returning `Verdict{Intersects: false}` for a pattern
the decider cannot model is PROHIBITED. Errors, wrapped with `%w`, naming the concrete pattern and
the concrete construct:

| Condition | Behaviour |
|---|---|
| Pattern fails `syntax.Parse` / `Compile` | error naming the pattern and the parse failure |
| `EmptyWordBoundary` (`\b`) | error naming the pattern and the assertion |
| `EmptyNoWordBoundary` (`\B`) | error, same shape |
| `EmptyBeginLine` / `EmptyEndLine` (multi-line `^`/`$`) | error, same shape |
| `EmptyBeginText` / `EmptyEndText` (`^`/`$`) | **modelled** — satisfiable at position 0 / at end |
| `FoldCase` on a single-rune instruction | **modelled** — expand via `unicode.SimpleFold` (R3) |
| Any future `EmptyOp` bit | error by default — refuse the unrecognised rather than join the list of things that vanish |

That last row is the lesson 012 paid for three times: an unrecognised argument kind was rejected
**by default** so the next kind godog adds is refused rather than silently ignored. Same discipline
here, same reason.

**`panic` is prohibited** on this path. Built-in patterns are literals and `MustCompile` is
justified for them (`precheck.go`'s existing rationale), but contributed patterns are author input
and reach the same decider.

---

## 4. Internal: the product automaton

Not exported; recorded because its shape is what makes termination and cost arguable rather than
hoped for.

```go
type nfa struct {
    pat  string
    prog *syntax.Prog
}

// A search state: a pair of program-counter sets, plus position context.
type productState struct {
    a, b    []uint32 // sorted, deduplicated "core" pcs — targets of consumed runes
    atStart bool     // BeginText is satisfiable only here
}
```

**State transitions**: BFS from `{prog.Start}` on each side.

1. **Closure** expands core pcs across epsilon edges (`InstAlt`, `InstAltMatch`, `InstCapture`,
   `InstNop`), gating `InstEmptyWidth` on `atStart`/`atEnd` and refusing the four unmodelled
   assertions. It is computed **twice per state**: with `atEnd=false` for stepping, and with
   `atEnd=true` for the acceptance test. Collapsing those into one closure is the bug that would
   make every `$`-anchored pattern look unsatisfiable.
2. **Acceptance**: both sides' `atEnd=true` closures contain `InstMatch` → the accumulated string
   is a witness.
3. **Stepping**: over **rune equivalence classes**, one representative per class, where classes
   come from the union of *both* programs' rune-range boundaries. A rune class absent from the
   partition is a missed transition and therefore a false "disjoint" — this is one of the
   mutations US3 must rehearse.
4. **Termination**: reachable `(a, b, atStart)` triples are finite, and `seen` dedupes them.
   Measured bound over the built-in set: **max 74 states per pair**, 2589 total across 780 pairs.

**Cost is measured, not asserted** (FR-016, and `research.md`'s recommendation): report the pair
count and wall clock; do not assert a ceiling that a slow CI runner would trip.

---

## 5. `Finding` — one new class, no shape change

The existing type is unchanged:

```go
type Finding struct {
    File    string `json:"file"`
    Line    int    `json:"line"`
    Class   string `json:"class"`
    Message string `json:"message"`
}
```

**New class key: `pattern-overlap`** (R8), joining `bad-cel`, `unknown-target`, `unbound-step`,
`ambiguous-step`, `step-argument`, `unknown-shape`, `bad-runs-tag`.

- `File: ""`, `Line: 0` — a pattern-pair defect has no feature-file location. `Source` already
  tolerates zero (`precheck.go:30-38`, scenario-init leaves it zero by design).
- **Distinct from `ambiguous-step` on purpose.** `ambiguous-step` answers "how many patterns match
  *this sentence*" and points at a line someone wrote. `pattern-overlap` answers "can these two
  patterns ever match the same string" and points at no line at all. Reusing the key would make a
  pattern-level defect look line-level and would break the guarantee that an `ambiguous-step`
  finding names a step in the file.
- **Message MUST carry**: both patterns, the contributing comparator(s) where there are any, and
  the witness. Exact wording is a TDD decision (`research.md`, open items).

---

## 6. `StepArguments` matching — from first-match to count

The US1 change. Today:

```go
func (s StepArguments) matchBuiltin(text string) *builtinArg      // FIRST match
func (s StepArguments) matchPhrase(text string) *contributedPhrase // FIRST match
```

`stepProblem` then switches on which of the two is non-nil, deferring only when *both* are. The
defect is that each helper collapses "several matched" into "here is one".

**Target shape**: the helpers report **every** match (or the count plus one match), and
`stepProblem` decides from the **total count across both sources**:

| Total matching definitions | Behaviour |
|---|---|
| 0 | no argument finding (`unbound-step` is `StepBindingFindings`' answer) |
| 1 | diagnose against that one definition — **byte-identical to today** (FR-003, SC-005) |
| >1 | **defer**, whatever the sources are (FR-002) |

**NOT NEGOTIABLE** (FR-002): the decision comes from the count, not from a per-source case
analysis. A per-source switch is how a third pattern source added later reintroduces the
positional path by omission — and how this defect survived two rounds of fixing in 012, each
scoped to where it had been found.

**The single-match path must not change.** SC-005 asserts byte-identical findings and verdicts for
every existing suite; the >1 branch is new, the 1 branch is untouched.
