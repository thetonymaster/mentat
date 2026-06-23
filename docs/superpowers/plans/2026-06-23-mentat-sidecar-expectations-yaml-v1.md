# Sidecar Expectations YAML Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add named, reusable shape patterns in `expectations/*.yaml`, asserted from Gherkin by `Then the run matches shape "<name>"`, evaluated as a conjunction that aggregates every failing clause.

**Architecture:** A new `internal/expectations` package parses YAML into validated `comparator.ShapeExpectation` clauses (depends one-way on `comparator`; no new matching logic). The shipped `shape` comparator gains a `ShapePatternExpectation{Name, Clauses}` branch that loops clauses and aggregates failures. Patterns are loaded + fully validated once at the composition root (`engine.Build`); an unknown pattern name referenced by a feature fails in `sc.Before`, before the SUT is driven.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3` (already a dependency, used by `internal/config`), `github.com/cucumber/godog` (BDD), `go.uber.org/mock` (TraceStore mock in step tests). Design spec: `docs/superpowers/specs/2026-06-23-mentat-sidecar-expectations-yaml-design.md`.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat`; Go 1.25.
- **Format/vet/lint clean:** `gofmt -l .` prints nothing; `go vet ./...` clean; `golangci-lint run` clean (a `.golangci.yml` exists).
- **Comparators consume `Evidence` only** (invariant #1). The new `internal/expectations` package does the YAML/file IO; `internal/comparator` never imports it and never touches files.
- **`Trace` is a forest** (invariant #2) — never assume a single root. (Unchanged here; the shape comparator already handles it.)
- **No silent fallbacks** (invariant #4). A function that cannot do its job returns a wrapped `error` (`fmt.Errorf("...: %w", err)`), never a zero-value success. Behavioural mismatch → `core.Verdict{Pass:false, Reasons:[...]}`; author/wiring bug (bad YAML, bad selector, unknown pattern name) → hard `error`. The **one** deliberate non-erroring fallback: a missing/empty expectations dir loads zero patterns (design §7), guarded by the unknown-name pre-check.
- **Errors name the concrete thing + value:** `fmt.Errorf("count %q: must start with \">=\" or \"==\"", s)`, not `"invalid input"`.
- **Tests:** table-driven default; hermetic (no network) except the `//go:build e2e` L3 meta-test; ≥80% coverage for every touched package (`internal/comparator`, `internal/expectations`, `internal/config`). `t.Parallel()` is a soft default for new table-driven tests sharing no mutable state; **omit it** for tests that write files into `t.TempDir()` only if they share state (these don't, so parallel is fine).
- **Git:** Conventional Commits; `git add .` is forbidden (add files individually); **no AI attribution** in commits.

## File Structure

- **Modify** `internal/config/config.go` — add `Config.Expectations` field + default (`"expectations"`). Responsibility: config surface.
- **Modify** `internal/comparator/shape.go` — add `ShapePatternExpectation`; extract `evalClause`; add `evalPattern`; turn `Compare` into a type switch. Responsibility: structural matching (now over one clause *or* a bundle).
- **Create** `internal/expectations/clause.go` — YAML schema structs (`patternYAML`, `clauseYAML`, `fanoutYAML`), `parseCount`, `clauseToExpectation`. Responsibility: "one YAML clause → one validated `ShapeExpectation`."
- **Create** `internal/expectations/clause_test.go` — clause translation + count parse unit tests.
- **Create** `internal/expectations/expectations.go` — `Patterns` type, `Get`, `Load(dir)`, `parsePattern`. Responsibility: "directory + file → named patterns."
- **Create** `internal/expectations/expectations_test.go` — loader unit tests (`t.TempDir`).
- **Modify** `internal/engine/engine.go` — `patterns` field + `ShapePattern` accessor.
- **Modify** `internal/engine/build.go` — call `expectations.Load(cfg.Expectations)`, store on the `Engine`.
- **Modify** `internal/engine/build_test.go` — assert patterns load + `ShapePattern` resolves.
- **Modify** `internal/steps/steps.go` — regex, step binding, `matchesShape` handler, `precheckShapePatterns` in the `sc.Before` hook.
- **Modify** `internal/steps/steps_test.go` — passing feature, red feature, unknown-name feature (hermetic).
- **Create** `expectations/bad_expectation.yaml` — a valid-but-unsatisfiable pattern for the L3 meta-test (repo root; loaded by the e2e binary's default config).
- **Create** `features/meta/bad_expectation.feature` — references that pattern.
- **Modify** `e2e/meta_test.go` — add the `bad_expectation` case.

Tasks are ordered so each builds on the last and ends with an independently testable, committable deliverable.

---

### Task 1: Config field — `expectations` directory

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces (used by Tasks 5): `config.Config.Expectations string` — the expectations directory; defaults to `"expectations"` when the YAML omits it.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoadExpectationsDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"defaults when absent", "store: tempo\n", "expectations"},
		{"explicit value preserved", "store: tempo\nexpectations: ./exp\n", "./exp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Expectations != tt.want {
				t.Errorf("Expectations = %q, want %q", c.Expectations, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadExpectationsDefault -v`
Expected: FAIL — `c.Expectations undefined (type Config has no field or method Expectations)` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, add the field to `Config` (after `Targets`):

```go
type Config struct {
	Store        string            `yaml:"store"`
	Tempo        Endpoint          `yaml:"tempo"`
	OTLPEndpoint string            `yaml:"otlpEndpoint"`
	Poll         PollSpec          `yaml:"poll"`
	Targets      map[string]Target `yaml:"targets"`
	Pricing      Pricing           `yaml:"pricing"`
	Expectations string            `yaml:"expectations"`
}
```

In `Load`, default it right after the `Store` default (near the existing `if c.Store == "" { c.Store = "tempo" }`):

```go
	if c.Expectations == "" {
		c.Expectations = "expectations"
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadExpectationsDefault -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/config/config.go internal/config/config_test.go
go vet ./internal/config/
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): expectations directory field (default \"expectations\")"
```

---

### Task 2: Comparator — `ShapePatternExpectation` + `evalClause` refactor + aggregation

**Files:**
- Modify: `internal/comparator/shape.go`
- Test: `internal/comparator/shape_test.go`

**Interfaces:**
- Consumes: the existing `ShapeExpectation`, `Count`, `Selector`, and the per-kind matchers (`shapeExists`/`shapeAbsent`/`shapeContainment`/`shapeFanout`).
- Produces (used by Tasks 5–7): `type ShapePatternExpectation struct{ Name string; Clauses []ShapeExpectation }`; behaviour: `shape.Compare` evaluates a `ShapePatternExpectation` clause-by-clause and aggregates **all** failing clauses into one `Verdict`.

> The refactor must preserve the shipped inline behaviour **exactly** — all existing `internal/comparator` tests stay green. `Compare`'s nil-trace check moves ahead of the type switch (it applies to both expectation types); everything else in the old `Compare` body moves verbatim into `evalClause`.

- [ ] **Step 1: Write the failing test**

Add to `internal/comparator/shape_test.go` (the `treeTrace()` helper from the shape work is reused; it has `root(invoke_agent) → mid(chat) → leaf(execute_tool search)`, a sibling `other(execute_tool fetch)` under root, and a second root `root2 → orphan(execute_tool pay)`):

```go
func TestShapePattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		exp        ShapePatternExpectation
		wantPass   bool
		wantReason int // expected number of aggregated reasons when failing
	}{
		{
			name: "all clauses pass",
			exp: ShapePatternExpectation{Name: "ok", Clauses: []ShapeExpectation{
				{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search")},
				{Kind: "containment", Relation: "child",
					Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.operation.name=chat")},
			}},
			wantPass: true,
		},
		{
			name: "two clauses fail, both reported",
			exp: ShapePatternExpectation{Name: "bad", Clauses: []ShapeExpectation{
				{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=search")},            // passes
				{Kind: "exists", Subject: sel(t, "gen_ai.tool.name=delete")},            // fails
				{Kind: "containment", Relation: "child",
					Subject: sel(t, "gen_ai.tool.name=search"), Parent: sel(t, "gen_ai.tool.name=nope")}, // fails
			}},
			wantPass:   false,
			wantReason: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := NewShape().Compare(context.Background(), core.Evidence{Trace: treeTrace()}, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if !tt.wantPass && len(v.Reasons) != tt.wantReason {
				t.Errorf("got %d reasons, want %d: %v", len(v.Reasons), tt.wantReason, v.Reasons)
			}
		})
	}
}

func TestShapePatternErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ev   core.Evidence
		exp  core.Expectation
	}{
		{"empty clauses", core.Evidence{Trace: treeTrace()}, ShapePatternExpectation{Name: "empty"}},
		{"malformed clause (unknown kind)", core.Evidence{Trace: treeTrace()},
			ShapePatternExpectation{Name: "x", Clauses: []ShapeExpectation{{Kind: "bogus", Subject: sel(t, "a=b")}}}},
		{"nil trace", core.Evidence{},
			ShapePatternExpectation{Name: "x", Clauses: []ShapeExpectation{{Kind: "exists", Subject: sel(t, "a=b")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewShape().Compare(context.Background(), tt.ev, tt.exp); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comparator/ -run 'TestShapePattern' -v`
Expected: FAIL — `undefined: ShapePatternExpectation` (compile error).

- [ ] **Step 3: Write minimal implementation**

In `internal/comparator/shape.go`, add the new type next to `ShapeExpectation`:

```go
// ShapePatternExpectation is a named bundle of shape clauses evaluated as a conjunction.
// Each clause is a fully-formed ShapeExpectation (produced by the expectations loader, or
// constructed directly in tests). Compare aggregates every failing clause into one Verdict.
type ShapePatternExpectation struct {
	Name    string
	Clauses []ShapeExpectation
}
```

Replace the whole `Compare` method (current lines 58–108) with a type switch that delegates to the extracted `evalClause`, and append `evalPattern`:

```go
func (shape) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("shape: Evidence.Trace is nil")
	}
	switch exp := e.(type) {
	case ShapeExpectation:
		return evalClause(ev.Trace, exp)
	case ShapePatternExpectation:
		return evalPattern(ev.Trace, exp)
	default:
		return core.Verdict{}, fmt.Errorf("shape: expectation must be ShapeExpectation or ShapePatternExpectation, got %T", e)
	}
}

// evalClause validates and evaluates one structural assertion. This is the former body of
// Compare, verbatim, except it takes the *trace.Trace directly (the nil check now lives in
// Compare) — preserving the shipped inline behaviour exactly.
func evalClause(tr *trace.Trace, exp ShapeExpectation) (core.Verdict, error) {
	if len(exp.Subject) == 0 {
		return core.Verdict{}, fmt.Errorf("shape: Subject selector is empty")
	}
	if exp.Count != nil && exp.Count.Op != ">=" && exp.Count.Op != "==" {
		return core.Verdict{}, fmt.Errorf("shape: unknown count op %q (want \">=\" or \"==\")", exp.Count.Op)
	}
	if exp.Count != nil && exp.Count.N < 0 {
		return core.Verdict{}, fmt.Errorf("shape: count N must be >= 0, got %d", exp.Count.N)
	}
	switch exp.Kind {
	case "exists":
		return shapeExists(tr, exp), nil
	case "absent":
		return shapeAbsent(tr, exp), nil
	case "containment":
		if err := validateShapeTraceIDs(tr); err != nil {
			return core.Verdict{}, fmt.Errorf("shape: containment requires valid span IDs: %w", err)
		}
		if len(exp.Parent) == 0 {
			return core.Verdict{}, fmt.Errorf("shape: containment requires a Parent selector")
		}
		if exp.Relation != "child" && exp.Relation != "descendant" {
			return core.Verdict{}, fmt.Errorf("shape: containment Relation must be \"child\" or \"descendant\", got %q", exp.Relation)
		}
		return shapeContainment(tr, exp), nil
	case "fanout":
		if err := validateShapeTraceIDs(tr); err != nil {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires valid span IDs: %w", err)
		}
		if len(exp.Parent) == 0 {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires a Parent selector")
		}
		if exp.Count == nil {
			return core.Verdict{}, fmt.Errorf("shape: fanout requires a Count")
		}
		if exp.Relation != "" && exp.Relation != "child" {
			return core.Verdict{}, fmt.Errorf("shape: fanout supports only direct children (v1); Relation %q not allowed", exp.Relation)
		}
		return shapeFanout(tr, exp), nil
	default:
		return core.Verdict{}, fmt.Errorf("shape: unknown Kind %q", exp.Kind)
	}
}

// evalPattern evaluates every clause and aggregates the reasons of all that fail. A clause
// that errors (malformed) aborts with a hard error (author bug); a clause that fails its
// behaviour contributes one Reasons element, prefixed with the pattern name, 1-based clause
// index, and kind. The existing step `check` joins Reasons with "; " behind "shape failed: ".
func evalPattern(tr *trace.Trace, exp ShapePatternExpectation) (core.Verdict, error) {
	if len(exp.Clauses) == 0 {
		return core.Verdict{}, fmt.Errorf("shape: pattern %q has no clauses", exp.Name)
	}
	var reasons []string
	for i, c := range exp.Clauses {
		v, err := evalClause(tr, c)
		if err != nil {
			return core.Verdict{}, fmt.Errorf("shape: pattern %q clause %d: %w", exp.Name, i+1, err)
		}
		if !v.Pass {
			for _, r := range v.Reasons {
				reasons = append(reasons, fmt.Sprintf("pattern %q clause %d (%s): %s", exp.Name, i+1, c.Kind, r))
			}
		}
	}
	if len(reasons) > 0 {
		return core.Verdict{Pass: false, Reasons: reasons}, nil
	}
	return core.Verdict{Pass: true}, nil
}
```

- [ ] **Step 4: Run the new tests, then the whole package to prove no regression**

Run: `go test ./internal/comparator/ -run 'TestShapePattern|TestShapePatternErrors' -v`
Expected: PASS.

Run: `go test ./internal/comparator/ -cover`
Expected: PASS (all existing shape/sequence/result/cel/budgets tests still green), coverage ≥ 80%.

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/comparator/shape.go internal/comparator/shape_test.go
go vet ./internal/comparator/
git add internal/comparator/shape.go internal/comparator/shape_test.go
git commit -m "feat(comparator): ShapePatternExpectation — aggregate multi-clause shape verdict"
```

---

### Task 3: Expectations package — clause translation (`clause.go`)

**Files:**
- Create: `internal/expectations/clause.go`
- Test: `internal/expectations/clause_test.go`

**Interfaces:**
- Consumes (Task 2): `comparator.ShapeExpectation`, `comparator.Count`, `comparator.ParseSelector`.
- Produces (used by Task 4):
  - `type patternYAML struct{ Name, Description string; Clauses []clauseYAML }`
  - `type clauseYAML struct{ Exists, Absent, Child, Descendant, Of, Count string; Fanout *fanoutYAML }`
  - `type fanoutYAML struct{ Parent, Child, Count string }`
  - `func parseCount(s string) (*comparator.Count, error)` — `""`→`(nil,nil)`; `">=N"`/`"==N"`→`Count`; else error.
  - `func clauseToExpectation(c clauseYAML) (comparator.ShapeExpectation, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/expectations/clause_test.go`:

```go
package expectations

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/comparator"
)

func TestParseCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    *comparator.Count
		wantErr bool
	}{
		{"empty is nil", "", nil, false},
		{"ge", ">=3", &comparator.Count{Op: ">=", N: 3}, false},
		{"eq", "==2", &comparator.Count{Op: "==", N: 2}, false},
		{"trims spaces", "  >= 4 ", &comparator.Count{Op: ">=", N: 4}, false},
		{"bad op gt", ">5", nil, true},
		{"bad op le", "<=5", nil, true},
		{"non-integer", "==x", nil, true},
		{"negative", "==-1", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCount(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCount(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCount(%q): %v", tt.in, err)
			}
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("parseCount(%q) = %v, want %v", tt.in, got, tt.want)
			}
			if got != nil && (got.Op != tt.want.Op || got.N != tt.want.N) {
				t.Errorf("parseCount(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestClauseToExpectation(t *testing.T) {
	t.Parallel()
	fan := func(p, c, n string) *fanoutYAML { return &fanoutYAML{Parent: p, Child: c, Count: n} }
	tests := []struct {
		name     string
		in       clauseYAML
		wantKind string
		wantErr  bool
	}{
		{"exists no count", clauseYAML{Exists: "gen_ai.tool.name=search"}, "exists", false},
		{"exists with count", clauseYAML{Exists: "gen_ai.tool.name=search", Count: ">=2"}, "exists", false},
		{"absent", clauseYAML{Absent: "span.status=ERROR"}, "absent", false},
		{"child of", clauseYAML{Child: "gen_ai.tool.name=search", Of: "gen_ai.operation.name=chat"}, "containment", false},
		{"descendant of", clauseYAML{Descendant: "gen_ai.tool.name=search", Of: "gen_ai.operation.name=invoke_agent"}, "containment", false},
		{"fanout", clauseYAML{Fanout: fan("gen_ai.operation.name=chat", "gen_ai.tool.name=search", ">=3")}, "fanout", false},
		{"no discriminator", clauseYAML{Of: "a=b"}, "", true},
		{"two discriminators", clauseYAML{Exists: "a=b", Absent: "c=d"}, "", true},
		{"exists with of", clauseYAML{Exists: "a=b", Of: "c=d"}, "", true},
		{"absent with count", clauseYAML{Absent: "a=b", Count: ">=1"}, "", true},
		{"child without of", clauseYAML{Child: "a=b"}, "", true},
		{"child with count", clauseYAML{Child: "a=b", Of: "c=d", Count: ">=1"}, "", true},
		{"fanout without count", clauseYAML{Fanout: fan("a=b", "c=d", "")}, "", true},
		{"fanout missing parent", clauseYAML{Fanout: fan("", "c=d", ">=1")}, "", true},
		{"bad selector", clauseYAML{Exists: "not-a-selector"}, "", true},
		{"bad count", clauseYAML{Exists: "a=b", Count: ">5"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := clauseToExpectation(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("clauseToExpectation(%+v) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("clauseToExpectation(%+v): %v", tt.in, err)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
		})
	}
}

// Verify the fields are wired through, not just the Kind.
func TestClauseToExpectationFields(t *testing.T) {
	t.Parallel()
	got, err := clauseToExpectation(clauseYAML{
		Fanout: &fanoutYAML{Parent: "gen_ai.operation.name=chat", Child: "gen_ai.tool.name=search", Count: ">=3"},
	})
	if err != nil {
		t.Fatalf("clauseToExpectation: %v", err)
	}
	if got.Kind != "fanout" || got.Relation != "child" || got.Count == nil || got.Count.Op != ">=" || got.Count.N != 3 {
		t.Fatalf("unexpected fanout expectation: %+v (count %+v)", got, got.Count)
	}
	if len(got.Subject) != 1 || got.Subject[0] != (comparator.Pred{Key: "gen_ai.tool.name", Value: "search"}) {
		t.Errorf("Subject = %v, want child selector", got.Subject)
	}
	if len(got.Parent) != 1 || got.Parent[0] != (comparator.Pred{Key: "gen_ai.operation.name", Value: "chat"}) {
		t.Errorf("Parent = %v, want parent selector", got.Parent)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/expectations/ -run 'TestParseCount|TestClauseToExpectation' -v`
Expected: FAIL — package/symbols undefined (`undefined: parseCount`, `undefined: clauseYAML`).

- [ ] **Step 3: Write minimal implementation**

Create `internal/expectations/clause.go`:

```go
// Package expectations loads named sidecar shape patterns (expectations/*.yaml) into
// validated comparator.ShapeExpectation clauses. It depends one-way on comparator; the
// comparator never imports this package and never touches files (architecture invariant #1).
package expectations

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
)

// patternYAML is the on-disk form of one named pattern (one document per file).
type patternYAML struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Clauses     []clauseYAML `yaml:"clauses"`
}

// clauseYAML is one clause. Exactly one discriminator key (exists/absent/child/descendant/
// fanout) must be present; `of` and `count` are modifiers.
type clauseYAML struct {
	Exists     string      `yaml:"exists"`
	Absent     string      `yaml:"absent"`
	Child      string      `yaml:"child"`
	Descendant string      `yaml:"descendant"`
	Of         string      `yaml:"of"`
	Count      string      `yaml:"count"`
	Fanout     *fanoutYAML `yaml:"fanout"`
}

type fanoutYAML struct {
	Parent string `yaml:"parent"`
	Child  string `yaml:"child"`
	Count  string `yaml:"count"`
}

// parseCount parses a count string into a *comparator.Count. An empty string returns
// (nil, nil) — "no constraint", the caller supplies the default. Only ">=N" and "==N"
// are valid (matching comparator.Count's two ops); any other operator or a non-integer or
// negative N is a hard error.
func parseCount(s string) (*comparator.Count, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, op := range []string{">=", "=="} {
		if strings.HasPrefix(s, op) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s, op)))
			if err != nil {
				return nil, fmt.Errorf("count %q: %w", s, err)
			}
			if n < 0 {
				return nil, fmt.Errorf("count %q: N must be >= 0", s)
			}
			return &comparator.Count{Op: op, N: n}, nil
		}
	}
	return nil, fmt.Errorf("count %q: must start with \">=\" or \"==\"", s)
}

// clauseToExpectation translates one YAML clause into a validated ShapeExpectation. It
// enforces exactly-one-discriminator, modifier legality (`of` only on child/descendant;
// `count` only on exists/fanout), and parses every selector via comparator.ParseSelector.
func clauseToExpectation(c clauseYAML) (comparator.ShapeExpectation, error) {
	var kinds []string
	if c.Exists != "" {
		kinds = append(kinds, "exists")
	}
	if c.Absent != "" {
		kinds = append(kinds, "absent")
	}
	if c.Child != "" {
		kinds = append(kinds, "child")
	}
	if c.Descendant != "" {
		kinds = append(kinds, "descendant")
	}
	if c.Fanout != nil {
		kinds = append(kinds, "fanout")
	}
	if len(kinds) == 0 {
		return comparator.ShapeExpectation{}, fmt.Errorf("clause has no recognized key (want one of exists/absent/child/descendant/fanout)")
	}
	if len(kinds) > 1 {
		return comparator.ShapeExpectation{}, fmt.Errorf("clause has multiple keys %v; exactly one of exists/absent/child/descendant/fanout is allowed", kinds)
	}

	switch kinds[0] {
	case "exists":
		if c.Of != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("exists clause does not take 'of'")
		}
		sub, err := comparator.ParseSelector(c.Exists)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("exists selector: %w", err)
		}
		cnt, err := parseCount(c.Count)
		if err != nil {
			return comparator.ShapeExpectation{}, err
		}
		return comparator.ShapeExpectation{Kind: "exists", Subject: sub, Count: cnt}, nil

	case "absent":
		if c.Of != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("absent clause does not take 'of'")
		}
		if c.Count != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("absent clause does not take 'count'")
		}
		sub, err := comparator.ParseSelector(c.Absent)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("absent selector: %w", err)
		}
		return comparator.ShapeExpectation{Kind: "absent", Subject: sub}, nil

	case "child", "descendant":
		if c.Count != "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("%s clause does not take 'count'", kinds[0])
		}
		if c.Of == "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("%s clause requires 'of' (the parent selector)", kinds[0])
		}
		raw := c.Child
		if kinds[0] == "descendant" {
			raw = c.Descendant
		}
		childSel, err := comparator.ParseSelector(raw)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("%s selector: %w", kinds[0], err)
		}
		parentSel, err := comparator.ParseSelector(c.Of)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("of selector: %w", err)
		}
		return comparator.ShapeExpectation{Kind: "containment", Subject: childSel, Parent: parentSel, Relation: kinds[0]}, nil

	default: // "fanout"
		f := c.Fanout
		if f.Parent == "" || f.Child == "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout requires both 'parent' and 'child'")
		}
		if f.Count == "" {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout requires 'count'")
		}
		parentSel, err := comparator.ParseSelector(f.Parent)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout parent selector: %w", err)
		}
		childSel, err := comparator.ParseSelector(f.Child)
		if err != nil {
			return comparator.ShapeExpectation{}, fmt.Errorf("fanout child selector: %w", err)
		}
		cnt, err := parseCount(f.Count)
		if err != nil {
			return comparator.ShapeExpectation{}, err
		}
		return comparator.ShapeExpectation{Kind: "fanout", Subject: childSel, Parent: parentSel, Relation: "child", Count: cnt}, nil
	}
}
```

> Note: `comparator.Pred` is referenced in the test (`TestClauseToExpectationFields`). It is an exported type in `internal/comparator/shape_selector.go` (`type Pred struct{ Key, Value string }`), so the test compiles without further changes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/expectations/ -run 'TestParseCount|TestClauseToExpectation' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/expectations/clause.go internal/expectations/clause_test.go
go vet ./internal/expectations/
git add internal/expectations/clause.go internal/expectations/clause_test.go
git commit -m "feat(expectations): YAML clause schema + translation to ShapeExpectation"
```

---

### Task 4: Expectations package — directory loader (`expectations.go`)

**Files:**
- Create: `internal/expectations/expectations.go`
- Test: `internal/expectations/expectations_test.go`

**Interfaces:**
- Consumes (Task 3): `patternYAML`, `clauseToExpectation`.
- Produces (used by Task 5):
  - `type Patterns map[string][]comparator.ShapeExpectation`
  - `func (p Patterns) Get(name string) ([]comparator.ShapeExpectation, bool)`
  - `func Load(dir string) (Patterns, error)` — empty/missing dir → empty `Patterns`, nil error; malformed file / duplicate name → hard error.

- [ ] **Step 1: Write the failing test**

Create `internal/expectations/expectations_test.go`:

```go
package expectations

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const goodPattern = `name: fanout-summarize
description: planner fans out then summarizes
clauses:
  - exists: "gen_ai.tool.name=search"
    count: ">=2"
  - child: "gen_ai.tool.name=summarize"
    of: "gen_ai.operation.name=chat"
  - fanout:
      parent: "gen_ai.operation.name=chat"
      child: "gen_ai.operation.name=execute_tool"
      count: ">=3"
`

func TestLoadGood(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "fanout.yaml", goodPattern)
	pats, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	clauses, ok := pats.Get("fanout-summarize")
	if !ok {
		t.Fatalf("pattern fanout-summarize not loaded; got %v", pats)
	}
	if len(clauses) != 3 {
		t.Errorf("got %d clauses, want 3", len(clauses))
	}
	if _, ok := pats.Get("nope"); ok {
		t.Errorf("Get(nope) = true, want false")
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, dir string }{
		{"empty string", ""},
		{"missing dir", filepath.Join(t.TempDir(), "does-not-exist")},
		{"empty dir", t.TempDir()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pats, err := Load(tt.dir)
			if err != nil {
				t.Fatalf("Load(%q): %v", tt.dir, err)
			}
			if len(pats) != 0 {
				t.Errorf("Load(%q) = %v, want empty", tt.dir, pats)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"malformed yaml", map[string]string{"a.yaml": "name: x\nclauses: [oops"}},
		{"unknown clause key", map[string]string{"a.yaml": "name: x\nclauses:\n  - exits: \"a=b\"\n"}},
		{"missing name", map[string]string{"a.yaml": "clauses:\n  - exists: \"a=b\"\n"}},
		{"empty clauses", map[string]string{"a.yaml": "name: x\nclauses: []\n"}},
		{"bad clause", map[string]string{"a.yaml": "name: x\nclauses:\n  - child: \"a=b\"\n"}}, // child without of
		{"duplicate name", map[string]string{
			"a.yaml": "name: dup\nclauses:\n  - exists: \"a=b\"\n",
			"b.yaml": "name: dup\nclauses:\n  - exists: \"c=d\"\n",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for n, b := range tt.files {
				write(t, dir, n, b)
			}
			if _, err := Load(dir); err == nil {
				t.Fatalf("Load() = nil error, want error")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/expectations/ -run 'TestLoad' -v`
Expected: FAIL — `undefined: Load` / `undefined: Patterns` (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/expectations/expectations.go`:

```go
package expectations

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thetonymaster/mentat/internal/comparator"
)

// Patterns maps a pattern name to its ordered, validated shape clauses.
type Patterns map[string][]comparator.ShapeExpectation

// Get returns the clauses for name and whether it exists.
func (p Patterns) Get(name string) ([]comparator.ShapeExpectation, bool) {
	c, ok := p[name]
	return c, ok
}

// Load reads every *.yaml / *.yml file under dir into named patterns. An empty dir
// argument, or a dir that does not exist, yields an empty (non-nil) Patterns and no error
// (design §7: the default expectations dir is absent in pattern-free projects; the
// unknown-name pre-check in the step layer is the real safety net). A malformed file, an
// invalid clause, a missing name, empty clauses, or a duplicate name is a hard error.
func Load(dir string) (Patterns, error) {
	pats := Patterns{}
	if strings.TrimSpace(dir) == "" {
		return pats, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pats, nil
		}
		return nil, fmt.Errorf("expectations: read dir %q: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
			files = append(files, n)
		}
	}
	sort.Strings(files) // deterministic duplicate-name error ordering

	srcOf := make(map[string]string, len(files))
	for _, fn := range files {
		path := filepath.Join(dir, fn)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("expectations: read %q: %w", path, err)
		}
		name, clauses, err := parsePattern(data)
		if err != nil {
			return nil, fmt.Errorf("expectations: %q: %w", path, err)
		}
		if prev, dup := srcOf[name]; dup {
			return nil, fmt.Errorf("expectations: duplicate pattern name %q in %q and %q", name, prev, path)
		}
		srcOf[name] = path
		pats[name] = clauses
	}
	return pats, nil
}

// parsePattern decodes one YAML document (strict: unknown keys are errors), rejects a
// second document in the same file, and translates every clause. It returns the pattern
// name and its clauses.
func parsePattern(data []byte) (string, []comparator.ShapeExpectation, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var py patternYAML
	if err := dec.Decode(&py); err != nil {
		return "", nil, fmt.Errorf("parse: %w", err)
	}
	// No silent fallback: a second document would be silently dropped, so reject it.
	var extra patternYAML
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", nil, fmt.Errorf("multiple YAML documents in one file; one pattern per file")
		}
		return "", nil, fmt.Errorf("parse (second document): %w", err)
	}
	if strings.TrimSpace(py.Name) == "" {
		return "", nil, fmt.Errorf("missing 'name'")
	}
	if len(py.Clauses) == 0 {
		return "", nil, fmt.Errorf("pattern %q has no clauses", py.Name)
	}
	clauses := make([]comparator.ShapeExpectation, 0, len(py.Clauses))
	for i, c := range py.Clauses {
		exp, err := clauseToExpectation(c)
		if err != nil {
			return "", nil, fmt.Errorf("pattern %q clause %d: %w", py.Name, i+1, err)
		}
		clauses = append(clauses, exp)
	}
	return py.Name, clauses, nil
}
```

- [ ] **Step 4: Run tests + coverage**

Run: `go test ./internal/expectations/ -cover`
Expected: PASS (all subtests), coverage ≥ 80%.

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/expectations/expectations.go internal/expectations/expectations_test.go
go vet ./internal/expectations/
git add internal/expectations/expectations.go internal/expectations/expectations_test.go
git commit -m "feat(expectations): load + validate expectations/*.yaml into named patterns"
```

---

### Task 5: Engine wiring — load patterns at Build, expose via `ShapePattern`

**Files:**
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/build.go`
- Test: `internal/engine/build_test.go`

**Interfaces:**
- Consumes (Tasks 1, 4): `cfg.Expectations`, `expectations.Load`, `expectations.Patterns`.
- Produces (used by Task 6): `func (e *Engine) ShapePattern(name string) ([]comparator.ShapeExpectation, bool)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/engine/build_test.go`. Add `"os"` and `"path/filepath"` to its import block (it already imports `config`). `Build` never calls the store or correlator — it only stores them on the `Engine` — so passing `nil` for both is safe and keeps the test hermetic with no mocks:

```go
func TestBuildLoadsShapePatterns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"),
		[]byte("name: p1\nclauses:\n  - exists: \"gen_ai.tool.name=search\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := config.Config{OTLPEndpoint: "x", Expectations: dir}
	eng, err := Build(cfg, nil, nil) // Build does not call st/cor; nil is safe
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clauses, ok := eng.ShapePattern("p1")
	if !ok || len(clauses) != 1 {
		t.Fatalf("ShapePattern(p1) = (%v, %v), want 1 clause", clauses, ok)
	}
	if _, ok := eng.ShapePattern("missing"); ok {
		t.Errorf("ShapePattern(missing) = true, want false")
	}
}

func TestBuildNoExpectationsDir(t *testing.T) {
	cfg := config.Config{OTLPEndpoint: "x"} // Expectations == "" → zero patterns, no error
	eng, err := Build(cfg, nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := eng.ShapePattern("anything"); ok {
		t.Errorf("ShapePattern = true on empty engine, want false")
	}
}

func TestBuildRejectsMalformedPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"),
		[]byte("name: bad\nclauses:\n  - child: \"a=b\"\n"), 0o644); err != nil { // child without of
		t.Fatalf("write: %v", err)
	}
	cfg := config.Config{OTLPEndpoint: "x", Expectations: dir}
	if _, err := Build(cfg, nil, nil); err == nil {
		t.Fatalf("Build() = nil error, want error for malformed pattern")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run 'TestBuildLoadsShapePatterns|TestBuildNoExpectationsDir|TestBuildRejectsMalformedPattern' -v`
Expected: FAIL — `eng.ShapePattern undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/engine/engine.go`, add the import and the field + accessor. Add `"github.com/thetonymaster/mentat/internal/comparator"` and `"github.com/thetonymaster/mentat/internal/expectations"` to the import block, then:

```go
type Engine struct {
	cfg      config.Config
	cor      core.Correlator
	st       core.TraceStore
	sems     map[string]chan struct{} // per-target concurrency gate
	pinned   string                   // when set, Drive resolves this run id instead of driving
	pricing  core.Pricing
	patterns expectations.Patterns
}

// ShapePattern resolves a named sidecar shape pattern loaded at Build. The bool is false
// for an unknown name; the step layer pre-checks names in sc.Before so this is a safety net.
func (e *Engine) ShapePattern(name string) ([]comparator.ShapeExpectation, bool) {
	return e.patterns.Get(name)
}
```

In `internal/engine/build.go`, load the patterns and store them on the returned `Engine`. Add `"github.com/thetonymaster/mentat/internal/expectations"` to the imports, then replace the final `return` of `Build`:

```go
	pats, err := expectations.Load(cfg.Expectations)
	if err != nil {
		return nil, err
	}

	sems := map[string]chan struct{}{}
	for name, t := range cfg.Targets {
		n := t.MaxConcurrency
		if n < 1 {
			n = 1
		}
		sems[name] = make(chan struct{}, n)
	}
	return &Engine{cfg: cfg, cor: cor, st: st, sems: sems, pricing: pricing, patterns: pats}, nil
```

- [ ] **Step 4: Run tests to verify they pass + whole engine package**

Run: `go test ./internal/engine/ -cover`
Expected: PASS (new + existing), coverage ≥ 80%.

- [ ] **Step 5: Format, vet, commit**

```bash
gofmt -w internal/engine/engine.go internal/engine/build.go internal/engine/build_test.go
go vet ./internal/engine/
git add internal/engine/engine.go internal/engine/build.go internal/engine/build_test.go
git commit -m "feat(engine): load sidecar shape patterns at Build; expose ShapePattern"
```

---

### Task 6: Steps — `the run matches shape` grammar + unknown-name pre-check + hermetic features

**Files:**
- Modify: `internal/steps/steps.go`
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes (Tasks 2, 5): `comparator.ShapePatternExpectation`, `engine.Engine.ShapePattern`.
- Produces: the `the run matches shape "<name>"` step; `w.matchesShape`; `w.precheckShapePatterns`.

> The hermetic feature tests reuse `shapeTrace()` (added to `steps_test.go` by the shape work): `invoke_agent(root) → chat → {search, search, summarize(ERROR)}`, all with explicit IDs. A pattern of `exists search >=2` + `child search of chat` + `fanout chat→search >=2` is satisfied by it.

- [ ] **Step 1: Write the failing feature tests (pass + red + unknown-name)**

Add to `internal/steps/steps_test.go`. Add `"os"` and `"path/filepath"` to the import block if absent.

```go
// writeExpectation writes one pattern file into a fresh dir and returns the dir.
func writeExpectation(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write pattern: %v", err)
	}
	return dir
}

func shapePatternEngine(t *testing.T, expDir string) *engine.Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Expectations: expDir,
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(shapeTrace(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

func runShapePatternFeature(t *testing.T, eng *engine.Engine, feature string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	suite := godog.TestSuite{
		ScenarioInitializer: Initializer(eng),
		Options: &godog.Options{
			Format:          "pretty",
			Output:          &out,
			FeatureContents: []godog.Feature{{Name: "pattern", Contents: []byte(feature)}},
		},
	}
	return suite.Run(), out.String()
}

func TestFeatureMatchesShapePattern(t *testing.T) {
	eng := shapePatternEngine(t, writeExpectation(t, `name: research-shape
clauses:
  - exists: "gen_ai.tool.name=search"
    count: ">=2"
  - child: "gen_ai.tool.name=search"
    of: "gen_ai.operation.name=chat"
  - fanout:
      parent: "gen_ai.operation.name=chat"
      child: "gen_ai.tool.name=search"
      count: ">=2"
`))
	feature := `Feature: pattern
  Scenario: structural pattern holds
    Given the agent target "bot"
    When I run scenario "happy"
    Then the run matches shape "research-shape"
`
	if status, out := runShapePatternFeature(t, eng, feature); status != 0 {
		t.Fatalf("expected passing suite, status=%d\n%s", status, out)
	}
}

func TestFeatureMatchesShapePatternRed(t *testing.T) {
	eng := shapePatternEngine(t, writeExpectation(t, `name: impossible
clauses:
  - child: "gen_ai.operation.name=invoke_agent"
    of: "gen_ai.tool.name=search"
`))
	feature := `Feature: pattern
  Scenario: impossible containment fails
    Given the agent target "bot"
    When I run scenario "happy"
    Then the run matches shape "impossible"
`
	status, out := runShapePatternFeature(t, eng, feature)
	if status == 0 {
		t.Fatalf("expected failing suite, but it passed\n%s", out)
	}
	if !strings.Contains(out, "shape failed") {
		t.Fatalf("expected \"shape failed\" in output, got:\n%s", out)
	}
}

func TestFeatureUnknownShapePattern(t *testing.T) {
	eng := shapePatternEngine(t, writeExpectation(t, `name: known
clauses:
  - exists: "gen_ai.tool.name=search"
`))
	feature := `Feature: pattern
  Scenario: unknown pattern name fails before driving
    Given the agent target "bot"
    When I run scenario "happy"
    Then the run matches shape "does-not-exist"
`
	status, out := runShapePatternFeature(t, eng, feature)
	if status == 0 {
		t.Fatalf("expected failing suite for unknown pattern, but it passed\n%s", out)
	}
	if !strings.Contains(out, "unknown shape pattern") {
		t.Fatalf("expected \"unknown shape pattern\" in output, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/steps/ -run 'TestFeatureMatchesShapePattern|TestFeatureUnknownShapePattern' -v`
Expected: FAIL — godog reports the `the run matches shape` step as **undefined** (passing test fails). Both pass only once the step is bound and the pre-check added.

- [ ] **Step 3: Bind the step, add the handler, add the pre-check**

In `internal/steps/steps.go`, add the regex to the `var (...)` block (alongside `reSatisfiesInline` etc.):

```go
	reMatchesShape = regexp.MustCompile(`^the run matches shape "([^"]*)"$`)
```

Bind the step inside `InitializerWithCollector`'s `func(sc *godog.ScenarioContext)`, after the eight `w.shape*` bindings (near line 79):

```go
		sc.Step(`^the run matches shape "([^"]*)"$`, w.matchesShape)
```

In the `sc.Before(...)` hook, add the pattern pre-check after the existing `w.precompileScenario` call:

```go
			if err := w.precheckShapePatterns(scenario.Steps); err != nil {
				return ctx, err
			}
```

Add the handler and the pre-check methods among the other `func (w *world) ...` methods:

```go
func (w *world) matchesShape(name string) error {
	clauses, ok := w.eng.ShapePattern(name)
	if !ok {
		return fmt.Errorf("unknown shape pattern %q (no such pattern under the expectations dir)", name)
	}
	return w.check("shape", comparator.ShapePatternExpectation{Name: name, Clauses: clauses})
}

// precheckShapePatterns fails a scenario at init if it references a shape pattern that was
// not loaded — before the SUT is driven, mirroring precompileScenario for CEL (§7).
func (w *world) precheckShapePatterns(steps []*messages.PickleStep) error {
	for _, st := range steps {
		m := reMatchesShape.FindStringSubmatch(st.Text)
		if m == nil {
			continue
		}
		if _, ok := w.eng.ShapePattern(m[1]); !ok {
			return fmt.Errorf("scenario-init: unknown shape pattern %q (no such pattern under the expectations dir)", m[1])
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/steps/ -run 'TestFeatureMatchesShapePattern|TestFeatureUnknownShapePattern' -v`
Expected: PASS (all three).

- [ ] **Step 5: Run the steps + engine packages, format, vet, commit**

Run: `go test ./internal/steps/ ./internal/engine/`
Expected: PASS.

```bash
gofmt -w internal/steps/steps.go internal/steps/steps_test.go
go vet ./internal/steps/
git add internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): \"the run matches shape\" grammar + unknown-name pre-check"
```

---

### Task 7: L3 binary meta-test (prove Mentat goes red on an unsatisfiable pattern)

**Files:**
- Create: `expectations/bad_expectation.yaml`
- Create: `features/meta/bad_expectation.feature`
- Modify: `e2e/meta_test.go`

**Interfaces:**
- Consumes (Task 6): the `the run matches shape` step, available to the built `mentat` binary.

> This is the mandatory L3 meta-test (CLAUDE.md). It is `//go:build e2e` and runs the prebuilt binary from the repo root against live Tempo (`make harness-up`). The repo-root `mentat.yaml` sets no `expectations:` key, so it defaults to `expectations/` — this new dir. The pattern is **valid YAML** (so `Build` succeeds for every e2e run and the name resolves in `sc.Before`) but asserts an **impossible** containment (the `invoke_agent` root can never be a *child* of a tool span), so the run fails behaviourally with `"shape failed"`.

- [ ] **Step 1: Create the expectations pattern**

Create `expectations/bad_expectation.yaml`:

```yaml
name: bad-expectation
description: meta - structurally impossible; invoke_agent root cannot be a child of a tool span
clauses:
  - child: "gen_ai.operation.name=invoke_agent"
    of: "gen_ai.tool.name=search"
```

- [ ] **Step 2: Create the meta feature**

Create `features/meta/bad_expectation.feature` (mirrors `features/meta/bad_shape.feature`'s target/scenario):

```gherkin
Feature: meta - bad expectation pattern must fail
  Scenario: a sidecar pattern asserting impossible structure goes red
    Given the agent target "research-agent"
    When I run scenario "happy"
    Then the run matches shape "bad-expectation"
```

- [ ] **Step 3: Add the failing meta-test case**

In `e2e/meta_test.go`, add one row to the `cases` slice in `TestBadScenariosAreCaught`:

```go
		{"features/meta/bad_expectation.feature", "shape failed"},
```

- [ ] **Step 4: Bring up the harness and run the e2e meta-test**

Run:
```bash
make harness-up
go test -tags e2e ./e2e/ -run 'TestBadScenariosAreCaught/features_meta_bad_expectation.feature' -v
```
Expected: PASS — the subtest confirms `mentat run features/meta/bad_expectation.feature` exits non-zero with `"shape failed"` in its combined output.

- [ ] **Step 5: Confirm the new dir does not break other e2e runs, then commit**

Run (sanity: a non-pattern meta case still behaves, proving the new `expectations/` dir loads cleanly for every run):
```bash
go test -tags e2e ./e2e/ -run 'TestBadScenariosAreCaught/features_meta_bad_shape.feature' -v
```
Expected: PASS.

```bash
git add expectations/bad_expectation.yaml features/meta/bad_expectation.feature e2e/meta_test.go
git commit -m "test(e2e): L3 meta-test — sidecar pattern goes red on impossible structure"
```

---

## Final verification (after Task 7)

- [ ] `gofmt -l .` → prints nothing.
- [ ] `go vet ./...` → clean.
- [ ] `golangci-lint run` → clean.
- [ ] `go test ./...` → PASS (hermetic suite).
- [ ] `go test ./internal/comparator/ ./internal/expectations/ ./internal/config/ ./internal/engine/ -cover` → each ≥ 80%.
- [ ] (optional, needs harness) `go test -tags e2e ./e2e/ -run TestBadScenariosAreCaught` → PASS.
