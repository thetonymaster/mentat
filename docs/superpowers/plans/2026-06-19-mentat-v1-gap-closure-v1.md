# Mentat v1 Gap Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the five v1 design gaps (A1–A5) so the Mentat implementation matches the approved design — a `schema` result matcher, a cost pricing-table fallback, a `mentatctl service` CLI mirror, a `regex` Gherkin step, and a documented-and-reserved traceparent field.

**Architecture:** Each gap is an independent change against the existing layered design (Gherkin/godog → engine → driver/correlate/store seams → Evidence-only comparators). A1 ships as a registered `core.Matcher` (the matcher registry from Spec B already landed in commit #9). A2 threads a transport-free `core.Pricing` table into the `budgets`/`cel` comparators via the `engine.Build` composition root, mirroring the existing `config.HTTP` ↔ `core.HTTPSpec` pattern. A3 refactors the `mentatctl` dispatcher into `<agent|service> <verb>`, reusing adapter-agnostic engine paths. A4 is pure grammar wiring over the existing `regex` matcher. A5 is documentation plus a regression test.

**Tech Stack:** Go 1.25, godog (BDD), uber gomock (mocks), google/cel-go (CEL), and one new dependency `github.com/santhosh-tekuri/jsonschema/v6` (JSON Schema draft 2020-12, pure Go) for A1.

## Global Constraints

These apply to **every** task; each task's requirements implicitly include this section.

- **Module:** `github.com/thetonymaster/mentat`, `go 1.25.0`.
- **New dependency (A1 only):** `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2` — one direct dependency, pure Go, no cgo.
- **Invariant 4 — no silent fallbacks:** a function that cannot do its job returns a wrapped `error` (`fmt.Errorf("doing X: %w", err)`), never a zero-value success or guessed result. Error messages name the concrete thing and value that failed (`"port: expected int, got %q"`), never `"invalid input"`.
- **Comparators consume `Evidence` only** (`Trace` forest + driver `Output`); they never import `config`, `Driver`, or `TraceStore`.
- **Every seam is wired at one composition root** (`engine.Build`); the engine depends on interfaces, never concrete types.
- **Format & vet clean before any commit:** `gofmt -l .` prints nothing and `go vet ./...` exits 0. Run `golangci-lint run ./...` (a `.golangci.yml`/`make lint` exists).
- **TDD:** red → green → refactor, one failing test at a time. Table-driven tests are the default shape.
- **Mocks:** uber gomock (`go.uber.org/mock/gomock`) for the `core` interfaces; trivial value stubs only where no call-count/argument verification is needed.
- **Coverage floor: ≥80% per touched package** — verify with `bash .claude/skills/coverage/coverage.sh ./internal/<pkg>/`. `*/cmd/*` and `*/mocks` packages are exempt from the floor (see `coverage.sh::is_exempt`).
- **Hermetic by default:** unit/comparator tests use crafted traces / fixtures + gomock; no network, no Tempo. Live-Tempo tests are `//go:build e2e`.
- **Git:** Conventional Commits (`feat:`/`fix:`/`test:`/`docs:`/`refactor:`/`chore:`). `git add .` is forbidden — add files individually. **No AI attribution** in commits or PRs.

---

## File Structure

| File | Task | Responsibility |
| --- | --- | --- |
| `internal/steps/steps.go` | A4, A1 | Register the `regex` and `schema` Gherkin steps; map to `ResultExpectation`. |
| `internal/steps/steps_test.go` | A4, A1 | Step-method + godog grammar tests for the new steps. |
| `internal/core/core.go` | A5, A2 | Document `RunResult.PrimaryTraceID`; add `ModelRate`/`Pricing` value types. |
| `internal/driver/shell_test.go` | A5 | Assert the shell driver injects no `TRACEPARENT`. |
| `internal/comparator/matchers.go` | A1 | New `schemaMatcher` + JSON-Schema compile/validate/reason helpers; register it. |
| `internal/comparator/schema_test.go` (new) | A1 | Unit tests for the `schema` matcher. |
| `internal/genai/keys.go` | A2 | New `RequestModel = "gen_ai.request.model"` attribute key. |
| `internal/config/config.go` | A2 | `Config.Pricing` field + `config.ModelRate`/`config.Pricing` YAML types. |
| `internal/config/config_test.go` | A2 | Assert pricing parses from YAML. |
| `internal/comparator/budgets.go` | A2 | `costSum(t, pricing)` derivation + `NewBudgets(pricing)`. |
| `internal/comparator/cel.go` | A2 | `NewCEL(pricing)`; thread pricing into `bindVars`/`costSum`. |
| `internal/comparator/budgets_test.go`, `cel_test.go`, `fixtures_test.go`, `orderflow_fixtures_test.go` | A2 | Update constructor call sites; add derivation + CEL-parity tests. |
| `internal/engine/build.go` | A2 | Convert `config.Pricing` → `core.Pricing`; pass to both constructors. |
| `internal/engine/build_test.go` (new) | A2 | Test the `toPricing` conversion. |
| `internal/comparator/sequence.go` | A3 | Export `ServiceSequence` (single source of truth for the service path). |
| `internal/ctl/format.go` | A3 | New `FormatServices` (mirror of `FormatTools`). |
| `internal/ctl/diff.go` | A3 | New `DiffServices` + shared diff helper. |
| `internal/ctl/format_test.go`, `diff_test.go` | A3 | Tests for `FormatServices`/`DiffServices`. |
| `cmd/mentatctl/main.go` | A3 | `<agent\|service> <verb>` dispatch; domain-aware `tools`/`services`/`diff`. |
| `cmd/mentatctl/main_test.go` (new) | A3 | Test domain/verb parsing + unknown-domain error. |
| `features/checkout.feature` | A1 | Demonstrate the `schema` step against the live http SUT (harness-gated). |

---

## Task 1: A4 — `regex` Gherkin step

Pure grammar wiring; the `regex` matcher already exists and is registered (`internal/comparator/matchers.go:59-77`, registered at `:18-25`). This exposes it through a Gherkin step, analogous to `the result contains`/`the result equals`.

**Files:**
- Modify: `internal/steps/steps.go`
- Test: `internal/steps/steps_test.go`

**Interfaces:**
- Consumes: `comparator.ResultExpectation{Matcher, Want, Target}` (existing), `world.check(name string, exp core.Expectation) error` (existing, `internal/steps/steps.go:79`).
- Produces: step `^the result matches regex "([^"]*)"$` → `ResultExpectation{Matcher: "regex", Want: <re>}` (default `Target: "answer"`).

- [ ] **Step 1: Write the failing godog test**

Add to `internal/steps/steps_test.go` (the package already imports `bytes`, `strings`, `testing`, `godog`, and has the `buildEng` helper whose `svc` shell target prints `done`, so `answer == "done"`):

```go
// TestRegexStep exercises the `the result matches regex` grammar end-to-end
// through a godog suite: a matching pattern passes the suite; a non-matching one
// fails it and surfaces the result-comparator's regex reason. buildEng's `svc`
// shell target prints "done", so answer == "done".
func TestRegexStep(t *testing.T) {
	tests := []struct {
		name     string
		feature  string
		wantPass bool
		contains []string // substrings required in suite output (failure cases)
	}{
		{
			name: "matching pattern passes",
			feature: `Feature: regex
  Scenario: answer matches
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result matches regex "^do.e$"
`,
			wantPass: true,
		},
		{
			name: "non-matching pattern fails with reason",
			feature: `Feature: regex-red
  Scenario: answer does not match
    Given the agent target "svc"
    When I run scenario "happy"
    Then the result matches regex "zzz"
`,
			wantPass: false,
			contains: []string{"result regex"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			eng := buildEng(t, happyTrace())
			var out bytes.Buffer
			suite := godog.TestSuite{
				ScenarioInitializer: Initializer(eng),
				Options: &godog.Options{
					Format:          "pretty",
					Output:          &out,
					FeatureContents: []godog.Feature{{Name: tt.name, Contents: []byte(tt.feature)}},
				},
			}
			status := suite.Run()
			if tt.wantPass && status != 0 {
				t.Fatalf("expected passing suite, status=%d\n%s", status, out.String())
			}
			if !tt.wantPass && status == 0 {
				t.Fatalf("expected failing suite, got 0\n%s", out.String())
			}
			for _, sub := range tt.contains {
				if !strings.Contains(out.String(), sub) {
					t.Fatalf("expected %q in suite output, got:\n%s", sub, out.String())
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/steps/ -run TestRegexStep -v`
Expected: FAIL — the suite is non-zero on the "matching pattern passes" case because the step `the result matches regex "^do.e$"` is undefined (godog reports an undefined step).

- [ ] **Step 3: Register the step and add the handler**

In `internal/steps/steps.go`, add the step registration inside `Initializer` (after the `w.responseStatus` line at `:44`):

```go
		sc.Step(`^the result matches regex "([^"]*)"$`, w.resultMatchesRegex)
```

And add the handler method (next to `resultEquals` at `:138`):

```go
func (w *world) resultMatchesRegex(re string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "regex", Want: re})
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/steps/ -run TestRegexStep -v`
Expected: PASS (both sub-tests).

- [ ] **Step 5: Verify package, format, vet, coverage**

Run:
```bash
go test ./internal/steps/ && gofmt -l internal/steps/ && go vet ./internal/steps/
bash .claude/skills/coverage/coverage.sh ./internal/steps/
```
Expected: tests pass, `gofmt -l` prints nothing, vet clean, coverage ≥80%.

- [ ] **Step 6: Commit**

```bash
git add internal/steps/steps.go internal/steps/steps_test.go
git commit -m "feat(steps): expose regex result matcher via Gherkin step"
```

---

## Task 2: A5 — traceparent: document and reserve

No behaviour change. `RunResult.PrimaryTraceID` (`internal/core/core.go:64`) is never populated; this task documents it as reserved and adds the shell-driver regression test asserting drivers inject no `traceparent` (the http driver already asserts this at `internal/driver/http_test.go:77`).

**Files:**
- Modify: `internal/core/core.go:62-66`
- Test: `internal/driver/shell_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: nothing new (documentation + test only).

- [ ] **Step 1: Write the failing shell-driver test**

Add to `internal/driver/shell_test.go` (package already imports `context`, `strings`, `testing`, and `core`):

```go
// TestShellInjectsNoTraceparent is the shell-driver complement to the http
// driver's no-traceparent assertion (http_test.go): correlation rides
// OTEL_RESOURCE_ATTRIBUTES (a resource attribute), never a propagated
// traceparent, so the SUT's own first span roots the trace (spec §5). The host
// may export TRACEPARENT; t.Setenv clears it so ${VAR:-NONE} proves the driver
// injected nothing.
func TestShellInjectsNoTraceparent(t *testing.T) {
	t.Setenv("TRACEPARENT", "")
	spec := core.RunSpec{
		Command: []string{"sh", "-c", `printf '%s\n' "${TRACEPARENT:-NONE}"`},
		Tags:    map[string]string{"test.run.id": "run-tp"},
		RunID:   "run-tp",
	}
	res, err := NewShell().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(res.Output.Stdout); got != "NONE" {
		t.Fatalf("shell driver must not inject traceparent; TRACEPARENT=%q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it passes (and is meaningful)**

Run: `go test ./internal/driver/ -run TestShellInjectsNoTraceparent -v`
Expected: PASS — the shell driver never sets `TRACEPARENT`, so the child prints `NONE`. (This is a pinning test for an existing guarantee, so it passes immediately; sanity-check it actually exercises the driver by temporarily adding `cmd.Env = append(cmd.Env, "TRACEPARENT=present")` to `shell.go::Run`, re-running to see it FAIL with `TRACEPARENT="present"`, then reverting. This confirms the test would catch a regression.)

- [ ] **Step 3: Document `PrimaryTraceID`**

In `internal/core/core.go`, replace the `RunResult` struct (`:62-66`):

```go
type RunResult struct {
	RunID string
	// PrimaryTraceID is reserved for a future traceparent complement (spec §5):
	// a clean primary trace id for when a SUT adopts an injected traceparent. It
	// is intentionally left unset under the baggage-only correlation path that
	// ships today — baggage tag-first correlation is the invariant (it survives
	// the SUT rooting its own trace, which traceparent alone cannot), and nothing
	// in correlate.Resolve consumes this field. A second correlator (the
	// traceparent complement) will populate it and add a fast-path in Resolve;
	// until that consumer exists, injecting it would be a feature with no reader
	// (YAGNI).
	PrimaryTraceID string
	Output         Output
}
```

- [ ] **Step 4: Verify build, format, vet**

Run:
```bash
go build ./... && go test ./internal/driver/ && gofmt -l internal/core/ internal/driver/ && go vet ./internal/core/ ./internal/driver/
```
Expected: build clean, driver tests pass, `gofmt -l` prints nothing, vet clean. (Coverage for `internal/driver` is unchanged or higher — the doc edit adds no statements.)

- [ ] **Step 5: Commit**

```bash
git add internal/core/core.go internal/driver/shell_test.go
git commit -m "docs(core): reserve PrimaryTraceID; pin shell no-traceparent injection"
```

---

## Task 3: A1 — `schema` result matcher

A new deterministic matcher validating `ev.Output.Body` against a JSON Schema. It ships as a registered `core.Matcher` (the matcher registry landed in commit #9; `result.Compare` already dispatches via `registry.Matcher`).

**Files:**
- Modify: `go.mod`, `go.sum` (add dependency)
- Modify: `internal/comparator/matchers.go`
- Create: `internal/comparator/schema_test.go`
- Modify: `internal/steps/steps.go`
- Modify: `internal/steps/steps_test.go`
- Modify: `features/checkout.feature` (harness-gated demonstration)

**Interfaces:**
- Consumes: `core.Matcher` interface (`internal/core/core.go:96-99`), `registry.RegisterMatcher` (existing), `core.Evidence.Output.Body`.
- Produces:
  - matcher named `"schema"`, registered by `RegisterBuiltinMatchers`.
  - step `^the response body matches schema:$` (docstring) → `ResultExpectation{Matcher: "schema", Want: <docstring>}`.

### Dependency setup

- [ ] **Step 1: Add the jsonschema dependency**

Run:
```bash
go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.2
go mod tidy
```
Expected: `go.mod` gains `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2` as a **direct** require (no `// indirect`). `go.sum` updated.

Verify: `grep jsonschema go.mod` shows the line without `// indirect`.

### The matcher (TDD)

- [ ] **Step 2: Write the failing matcher test**

Create `internal/comparator/schema_test.go`. (`TestMain` in `matchers_test.go` already calls `RegisterBuiltinMatchers` for the whole package, so the matcher resolves once registered.)

```go
package comparator

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestSchemaMatcher(t *testing.T) {
	const schema = `{"type":"object","required":["orderId","total"],` +
		`"properties":{"total":{"type":"number"}}}`

	tests := []struct {
		name      string
		body      string
		want      string
		wantPass  bool
		wantErr   bool
		reasonSub string // substring required in a failure reason (when !wantPass)
		errSub    string // substring required in a hard error (when wantErr)
	}{
		{
			name:     "valid body passes",
			body:     `{"orderId":"x","total":4.2}`,
			want:     schema,
			wantPass: true,
		},
		{
			name:      "missing required field fails with reason",
			body:      `{"orderId":"x"}`,
			want:      schema,
			wantPass:  false,
			reasonSub: "total",
		},
		{
			name:      "wrong type fails with reason",
			body:      `{"orderId":"x","total":"nope"}`,
			want:      schema,
			wantPass:  false,
			reasonSub: "want number",
		},
		{
			name:      "empty body fails, not a hard error",
			body:      ``,
			want:      schema,
			wantPass:  false,
			reasonSub: "null",
		},
		{
			name:    "non-JSON body is a hard error",
			body:    `not json`,
			want:    schema,
			wantErr: true,
			errSub:  "not valid JSON",
		},
		{
			name:    "invalid schema is a hard error",
			body:    `{}`,
			want:    `{"type": 123}`,
			wantErr: true,
			errSub:  "invalid JSON Schema",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Body: []byte(tt.body)}}
			v, err := NewResult().Compare(context.Background(), ev,
				ResultExpectation{Matcher: "schema", Want: tt.want})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q missing %q", err.Error(), tt.errSub)
				}
				return
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if !tt.wantPass && tt.reasonSub != "" &&
				!strings.Contains(strings.Join(v.Reasons, " "), tt.reasonSub) {
				t.Fatalf("reasons %v missing %q", v.Reasons, tt.reasonSub)
			}
		})
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/comparator/ -run TestSchemaMatcher -v`
Expected: FAIL — every row errors with `result: unknown matcher "schema"` (the matcher is not registered yet).

- [ ] **Step 4: Implement and register the `schema` matcher**

In `internal/comparator/matchers.go`, extend the imports block:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)
```

Add `schemaMatcher{}` to the registration slice in `RegisterBuiltinMatchers` (`:19-22`):

```go
	for _, m := range []core.Matcher{
		exactMatcher{}, containsMatcher{}, regexMatcher{},
		jsonSubsetMatcher{}, statusMatcher{}, schemaMatcher{},
	} {
		registry.RegisterMatcher(m.Name(), m)
	}
```

Append the matcher and helpers to the file:

```go
type schemaMatcher struct{}

func (schemaMatcher) Name() string { return "schema" }

// Match validates the response body against the JSON Schema in want. The schema
// is compiled fresh per call; an invalid schema is a hard error (never a silent
// pass — invariant 4). An empty body validates as JSON null (a failure with a
// descriptive reason, not an error); a non-empty body that is not valid JSON is
// a hard error, mirroring the CEL `body` decision. Target is not consulted.
func (schemaMatcher) Match(_ context.Context, ev core.Evidence, want, _ string) (core.Verdict, error) {
	sch, err := compileSchema(want)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: schema: invalid JSON Schema: %w", err)
	}
	inst, err := schemaInstance(ev.Output.Body)
	if err != nil {
		return core.Verdict{}, err
	}
	if verr := sch.Validate(inst); verr != nil {
		return core.Verdict{Pass: false, Reasons: schemaReasons(verr)}, nil
	}
	return core.Verdict{Pass: true}, nil
}

// compileSchema compiles the JSON Schema in want. A fixed in-memory resource id
// keeps any compile-error text free of filesystem paths.
func compileSchema(want string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(want))
	if err != nil {
		return nil, err
	}
	if err := c.AddResource("mem:///schema", doc); err != nil {
		return nil, err
	}
	return c.Compile("mem:///schema")
}

// schemaInstance decodes the response body to a JSON value for validation. An
// empty (or whitespace-only) body decodes to nil (JSON null) — validated, not
// errored. A non-empty body that is not valid JSON is a hard error.
func schemaInstance(body []byte) (any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("result: schema: response body is not valid JSON: %w", err)
	}
	return v, nil
}

// schemaReasons renders the validator's per-instance failures as discrete
// reasons (e.g. "result schema: /total: got string, want number"). An error of
// an unexpected type degrades to a single wrapped reason rather than a panic.
func schemaReasons(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{fmt.Sprintf("result schema: %v", err)}
	}
	var reasons []string
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil {
			continue
		}
		loc := u.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		reasons = append(reasons, fmt.Sprintf("result schema: %s: %s", loc, u.Error.String()))
	}
	if len(reasons) == 0 {
		reasons = []string{fmt.Sprintf("result schema: %v", err)}
	}
	return reasons
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/comparator/ -run TestSchemaMatcher -v`
Expected: PASS (all six rows).

- [ ] **Step 6: Document the matcher in `ResultExpectation`**

In `internal/comparator/result.go`, update the doc comment on `ResultExpectation` (`:11-25`) so `schema` is listed alongside `json-subset`/`status` as a `Body`-reading matcher. Change the two `Matcher` references and the structural-matcher note:

```go
// ResultExpectation configures the result comparator.
// Matcher selects the matching strategy: exact | contains | regex | json-subset | status | schema.
// Want is the expected value (a string; for status, parsed as int; for schema, a JSON Schema).
// Target selects which Output field value matchers (exact/contains/regex) read:
//   - "" or "answer" → ev.Output.Answer (default)
//   - "status"       → strconv.Itoa(ev.Output.Status)
//   - any other      → error (no silent fallback)
//
// json-subset and schema always read ev.Output.Body; status always reads
// ev.Output.Status. Target is not consulted for those matchers.
type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status | schema
	Want    string
	Target  string // "answer" (default) or "status"
}
```

- [ ] **Step 7: Run the comparator package + format/vet/coverage**

Run:
```bash
go test ./internal/comparator/ && gofmt -l internal/comparator/ && go vet ./internal/comparator/
bash .claude/skills/coverage/coverage.sh ./internal/comparator/
```
Expected: all comparator tests pass, `gofmt -l` prints nothing, vet clean, coverage ≥80%.

- [ ] **Step 8: Commit the matcher**

```bash
git add go.mod go.sum internal/comparator/matchers.go internal/comparator/schema_test.go internal/comparator/result.go
git commit -m "feat(comparator): add schema result matcher (JSON Schema 2020-12)"
```

### The Gherkin step (TDD)

- [ ] **Step 9: Write the failing step test**

Add to `internal/steps/steps_test.go` (package already imports `godog`, `core`, `testing`; `buildEng` is defined there):

```go
// TestSchemaStep exercises the `the response body matches schema` step. The
// schema matcher reads ev.Output.Body, which the shell driver does not populate,
// so the body is set directly on the world's Evidence (the engine.Build inside
// buildEng registers the schema matcher + result comparator).
func TestSchemaStep(t *testing.T) {
	eng := buildEng(t, happyTrace())
	const schema = `{"type":"object","required":["status"],` +
		`"properties":{"status":{"type":"string"}}}`

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "valid body passes", body: `{"status":"confirmed"}`, wantErr: false},
		{name: "missing field fails", body: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &world{eng: eng}
			w.ev = core.Evidence{Output: core.Output{Body: []byte(tt.body)}}
			err := w.responseBodyMatchesSchema(&godog.DocString{Content: schema})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}

	t.Run("nil docstring is an error", func(t *testing.T) {
		w := &world{eng: eng}
		if err := w.responseBodyMatchesSchema(nil); err == nil {
			t.Fatal("want error for nil docstring, got nil")
		}
	})
}
```

- [ ] **Step 10: Run the test to verify it fails**

Run: `go test ./internal/steps/ -run TestSchemaStep -v`
Expected: FAIL to compile — `w.responseBodyMatchesSchema` is undefined.

- [ ] **Step 11: Register the step and add the handler**

In `internal/steps/steps.go`, add the registration inside `Initializer` (after the `w.responseBodyJSONContains` line at `:47`):

```go
		sc.Step(`^the response body matches schema:$`, w.responseBodyMatchesSchema)
```

And add the handler (next to `responseBodyJSONContains` at `:168`):

```go
func (w *world) responseBodyMatchesSchema(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("the response body matches schema: expected a docstring schema, got none")
	}
	return w.check("result", comparator.ResultExpectation{Matcher: "schema", Want: doc.Content})
}
```

- [ ] **Step 12: Run the test to verify it passes**

Run: `go test ./internal/steps/ -run TestSchemaStep -v`
Expected: PASS (both rows + the nil-docstring sub-test).

- [ ] **Step 13: Add the harness-gated demonstration to checkout.feature**

The http checkout SUT returns `{"status":"confirmed"}` on the happy scenario (orderflow gateway). Append to `features/checkout.feature` (this runs only under `make harness-up` via `mentat run`; `go test ./...` does not execute `features/`):

```gherkin
    And the response body matches schema:
      """
      { "type": "object", "required": ["status"],
        "properties": { "status": { "type": "string" } } }
      """
```

- [ ] **Step 14: Verify the steps package + format/vet/coverage**

Run:
```bash
go test ./internal/steps/ && gofmt -l internal/steps/ && go vet ./internal/steps/
bash .claude/skills/coverage/coverage.sh ./internal/steps/
```
Expected: tests pass, `gofmt -l` prints nothing, vet clean, coverage ≥80%.

- [ ] **Step 15: Commit the step**

```bash
git add internal/steps/steps.go internal/steps/steps_test.go features/checkout.feature
git commit -m "feat(steps): add 'response body matches schema' Gherkin step"
```

---

## Task 4: A2 — cost pricing-table fallback

When no span carries `gen_ai.usage.cost_usd`, derive cost from token counts and a per-model pricing table. `costSum` is shared by `budgets` and the CEL `cost` variable, so both must derive identically (single source of truth, spec §5). This task changes three constructor/function signatures atomically (the comparator package will not compile until every call site is updated), then implements derivation.

**Files:**
- Modify: `internal/genai/keys.go`, `internal/core/core.go`, `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/comparator/budgets.go`, `internal/comparator/cel.go`
- Modify: `internal/comparator/budgets_test.go`, `internal/comparator/cel_test.go`, `internal/comparator/fixtures_test.go`, `internal/comparator/orderflow_fixtures_test.go`
- Modify: `internal/engine/build.go`
- Create: `internal/engine/build_test.go`

**Interfaces:**
- Produces (consumed by later steps in this task and by A-nothing-else):
  - `genai.RequestModel = "gen_ai.request.model"`.
  - `core.ModelRate{InputPerMTok, OutputPerMTok float64}`, `core.Pricing map[string]ModelRate`.
  - `config.ModelRate{InputPerMTok, OutputPerMTok float64}` (yaml `inputPerMTok`/`outputPerMTok`), `config.Pricing map[string]ModelRate`, `Config.Pricing` (yaml `pricing`).
  - `comparator.NewBudgets(pricing core.Pricing) core.Comparator`.
  - `comparator.NewCEL(pricing core.Pricing) core.Comparator`.
  - `costSum(t *trace.Trace, pricing core.Pricing) (float64, error)` (package-internal).
  - `engine`-internal `toPricing(config.Pricing) core.Pricing`.

**Interpretation decision (recorded for review):** spec §4.3 point 3 ("model empty or absent from the table → hard error") and the final paragraph ("when pricing is empty, behaviour is exactly as today") are reconciled by gating derivation on a **non-empty** pricing table: an empty/unconfigured table skips derivation entirely and preserves the legacy emitted-cost-only path verbatim (`"cost not available"` when no span carries cost). The "model not in pricing table" hard error therefore fires only when a table is configured but is missing a model — which is exactly when that message is actionable. Both paths are hard errors, so invariant 4 holds either way; this reading makes both spec sentences literally true.

### Scaffolding the types (no behaviour change)

- [ ] **Step 1: Add the attribute key**

In `internal/genai/keys.go`, add to the `const` block:

```go
	RequestModel = "gen_ai.request.model"
```

(Place it after `CostUSD` so the token/cost keys stay grouped.)

- [ ] **Step 2: Add the `core` pricing value types**

In `internal/core/core.go`, after the `HTTPSpec` type (`:60`), add:

```go
// ModelRate is a per-model price in USD per million tokens (spec §4.1). Mirrors
// config.ModelRate, kept in core so the comparator layer never imports config.
type ModelRate struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Pricing maps a gen_ai.request.model value to its rate. Used to derive cost
// from token counts when a span carries no emitted gen_ai.usage.cost_usd (§4.3).
type Pricing map[string]ModelRate
```

- [ ] **Step 3: Add the config pricing field and YAML types**

In `internal/config/config.go`, add the field to `Config` (`:10-16`):

```go
type Config struct {
	Store        string            `yaml:"store"`
	Tempo        Endpoint          `yaml:"tempo"`
	OTLPEndpoint string            `yaml:"otlpEndpoint"`
	Poll         PollSpec          `yaml:"poll"`
	Targets      map[string]Target `yaml:"targets"`
	Pricing      Pricing           `yaml:"pricing"`
}
```

And after the `HTTP` type (`:40`), add:

```go
// ModelRate is the YAML form of a per-model price (USD per million tokens). The
// engine converts config.Pricing to the transport-free core.Pricing so the
// comparator layer keeps importing only core/genai/trace, never config.
type ModelRate struct {
	InputPerMTok  float64 `yaml:"inputPerMTok"`
	OutputPerMTok float64 `yaml:"outputPerMTok"`
}

// Pricing maps a model name to its rate.
type Pricing map[string]ModelRate
```

- [ ] **Step 4: Write the failing config test**

Add to `internal/config/config_test.go`:

```go
func TestLoadPricing(t *testing.T) {
	data := []byte(`
store: tempo
pricing:
  claude-opus-4-8:   { inputPerMTok: 15.0, outputPerMTok: 75.0 }
  claude-sonnet-4-6: { inputPerMTok: 3.0,  outputPerMTok: 15.0 }
`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	opus, ok := cfg.Pricing["claude-opus-4-8"]
	if !ok {
		t.Fatalf("pricing missing claude-opus-4-8; got %+v", cfg.Pricing)
	}
	if opus.InputPerMTok != 15.0 || opus.OutputPerMTok != 75.0 {
		t.Fatalf("opus rate = %+v, want {15 75}", opus)
	}
	if cfg.Pricing["claude-sonnet-4-6"].OutputPerMTok != 15.0 {
		t.Fatalf("sonnet outputPerMTok = %v, want 15", cfg.Pricing["claude-sonnet-4-6"].OutputPerMTok)
	}
}
```

(If `config_test.go` lacks a `testing` import or `package config` header, mirror the existing test file's header.)

- [ ] **Step 5: Run the config test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadPricing -v`
Expected: PASS — the YAML field parses. (No production logic was needed beyond the struct tags from Step 3, so this is green immediately; it pins the wiring.)

### Change the signatures (atomic; keeps existing behaviour)

- [ ] **Step 6: Add the pricing parameter to `costSum` and `NewBudgets`, threading it unused**

In `internal/comparator/budgets.go`:

Change the `budgets` type and constructor (`:27-31`):

```go
type budgets struct{ pricing core.Pricing }

// NewBudgets returns a Comparator that enforces BudgetExpectation thresholds.
// pricing derives cost from tokens when a span carries no emitted cost (§4.3);
// a nil/empty table preserves the emitted-cost-only behaviour.
func NewBudgets(pricing core.Pricing) core.Comparator { return budgets{pricing: pricing} }
func (budgets) Name() string                          { return "budgets" }
```

Change the `Compare` receiver and the `costSum` call (`:33`, `:56`):

```go
func (b budgets) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
```
```go
		cost, err := costSum(ev.Trace, b.pricing)
```

Change the `costSum` signature (`:111`) — body unchanged for now (the `pricing` parameter is accepted but not yet used):

```go
func costSum(t *trace.Trace, pricing core.Pricing) (float64, error) {
	_ = pricing // derivation added in Step 10
	cost := 0.0
	// ... existing body unchanged ...
```

- [ ] **Step 7: Add the pricing parameter to `NewCEL` and thread it into `bindVars`/`costSum`**

In `internal/comparator/cel.go`:

Add the field to `celComparator` (`:19-23`):

```go
type celComparator struct {
	engine   *celengine.Engine
	pricing  core.Pricing
	mu       sync.RWMutex
	programs map[string]*celengine.Program
}
```

Change the constructor (`:27-35`):

```go
// NewCEL returns the standalone, trace-aware CEL comparator (Name() == "cel").
// pricing is reused by the `cost` variable so it derives identically to budgets
// (§5, single source of truth).
func NewCEL(pricing core.Pricing) core.Comparator {
	engine, err := celengine.NewEngine()
	if err != nil {
		panic(fmt.Sprintf("cel: static schema failed to build: %v", err))
	}
	return &celComparator{engine: engine, pricing: pricing, programs: map[string]*celengine.Program{}}
}
```

In `Compare`, pass pricing to `bindVars` (`:88`):

```go
	vars, err := bindVars(refs, ev, c.pricing)
```

Change `bindVars`' signature (`:115`) and the `VarCost` binding (`:139`):

```go
func bindVars(refs []string, ev core.Evidence, pricing core.Pricing) (map[string]any, error) {
```
```go
		case celengine.VarCost:
			v, err := costSum(ev.Trace, pricing)
```

- [ ] **Step 8: Update every constructor call site (composition root + tests)**

In `internal/engine/build.go` (`:23`, `:25`) — add a converter and pass it. Replace the two `RegisterComparator` lines:

```go
	pricing := toPricing(cfg.Pricing)
	registry.RegisterComparator("sequence", comparator.NewSequence())
	registry.RegisterComparator("budgets", comparator.NewBudgets(pricing))
	registry.RegisterComparator("result", comparator.NewResult())
	registry.RegisterComparator("cel", comparator.NewCEL(pricing))
```

Add the converter at the end of `internal/engine/build.go`:

```go
// toPricing converts the YAML pricing table into the transport-free core.Pricing
// the comparator layer consumes. An empty/absent table maps to nil, which the
// comparators treat as "no derivation" (legacy emitted-cost-only behaviour).
func toPricing(p config.Pricing) core.Pricing {
	if len(p) == 0 {
		return nil
	}
	out := make(core.Pricing, len(p))
	for model, r := range p {
		out[model] = core.ModelRate{InputPerMTok: r.InputPerMTok, OutputPerMTok: r.OutputPerMTok}
	}
	return out
}
```

Update the test call sites (pass `nil` — these tests need no derivation):
- `internal/comparator/budgets_test.go`: lines `:107`, `:115`, `:125`, `:136`, `:303` → `NewBudgets(nil)`.
- `internal/comparator/fixtures_test.go`: line `:106` → `NewBudgets(nil)`.
- `internal/comparator/orderflow_fixtures_test.go`: line `:98` → `NewBudgets(nil)`.
- `internal/comparator/cel_test.go`: lines `:17`, `:23`, `:48`, `:94`, `:115`, `:134`, `:146`, `:175` → `NewCEL(nil)`.

(Use a find/replace within each file: `NewBudgets()` → `NewBudgets(nil)` and `NewCEL()` → `NewCEL(nil)`. Verify no other occurrences remain with `grep -rn "NewBudgets()\|NewCEL()" internal/`.)

- [ ] **Step 9: Verify the package compiles and existing behaviour is unchanged**

Run:
```bash
grep -rn "NewBudgets()\|NewCEL()" internal/ || echo "no bare calls remain"
go build ./... && go test ./internal/comparator/ ./internal/engine/ ./internal/config/
```
Expected: no bare `NewBudgets()`/`NewCEL()` calls remain; build clean; all tests pass (behaviour is unchanged — `pricing` is accepted but not yet used).

- [ ] **Step 10: Commit the scaffolding**

```bash
git add internal/genai/keys.go internal/core/core.go internal/config/config.go internal/config/config_test.go internal/engine/build.go internal/comparator/budgets.go internal/comparator/cel.go internal/comparator/budgets_test.go internal/comparator/cel_test.go internal/comparator/fixtures_test.go internal/comparator/orderflow_fixtures_test.go
git commit -m "refactor(comparator): thread core.Pricing through budgets/cel constructors"
```

### Implement derivation (TDD)

- [ ] **Step 11: Write the failing derivation test**

Add to `internal/comparator/budgets_test.go` a trace builder and a focused test. The builder produces a span with tokens + model but **no** emitted cost:

```go
// derivableTrace builds a trace whose single LLM span carries input/output tokens
// and a request model but NO emitted cost_usd, so cost must be derived.
func derivableTrace(in, out int, model string) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "chat",
		Attrs: map[string]string{
			genai.Op:           genai.OpChat,
			genai.InTokens:     strconv.Itoa(in),
			genai.OutTokens:    strconv.Itoa(out),
			genai.RequestModel: model,
		},
	}}}
}

func TestCostSumDerivesFromTokens(t *testing.T) {
	// 1,000,000 in @ $10/MTok + 1,000,000 out @ $20/MTok = $30.00
	pricing := core.Pricing{"m": {InputPerMTok: 10, OutputPerMTok: 20}}

	tests := []struct {
		name     string
		tr       *trace.Trace
		pricing  core.Pricing
		wantCost float64
		wantErr  bool
		errSub   string
	}{
		{
			name:     "derives from tokens when model is priced",
			tr:       derivableTrace(1_000_000, 1_000_000, "m"),
			pricing:  pricing,
			wantCost: 30.0,
		},
		{
			name:    "model absent from configured table is a hard error",
			tr:      derivableTrace(1_000_000, 0, "unpriced"),
			pricing: pricing,
			wantErr: true,
			errSub:  "not in pricing table",
		},
		{
			name:    "token span with no cost and empty table is unavailable",
			tr:      derivableTrace(1_000_000, 0, "m"),
			pricing: nil,
			wantErr: true,
			errSub:  "cost not available",
		},
		{
			name:     "emitted cost wins over derivation",
			tr:       costTrace(0.05), // existing helper: carries cost_usd, no tokens
			pricing:  pricing,
			wantCost: 0.05,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := costSum(tt.tr, tt.pricing)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q missing %q", err.Error(), tt.errSub)
				}
				return
			}
			if got != tt.wantCost {
				t.Fatalf("costSum = %v, want %v", got, tt.wantCost)
			}
		})
	}
}
```

(`strconv`, `strings`, `core`, `genai`, `trace` are already imported by `budgets_test.go`.)

- [ ] **Step 12: Run the test to verify it fails**

Run: `go test ./internal/comparator/ -run TestCostSumDerivesFromTokens -v`
Expected: FAIL — "derives from tokens when model is priced" errors with `cost not available` (the token-bearing span carries no `cost_usd`, and derivation is not implemented yet); "emitted cost wins" passes.

- [ ] **Step 13: Implement derivation in `costSum`**

In `internal/comparator/budgets.go`, replace the entire `costSum` function (and remove the temporary `_ = pricing`) with:

```go
// costSum returns the total gen_ai cost in USD across all spans, applying the
// per-span precedence in spec §4.3: an emitted gen_ai.usage.cost_usd always wins;
// otherwise a token-bearing span (an LLM call) derives cost from its tokens and
// the per-model pricing table; spans with neither cost nor tokens contribute 0.
// Absent cost across all spans is a hard error (the cel comparator inherits this
// via the shared function — §5). A malformed/out-of-range value is a hard error.
// When the pricing table is empty, derivation is skipped and the legacy
// emitted-cost-only behaviour applies verbatim.
func costSum(t *trace.Trace, pricing core.Pricing) (float64, error) {
	cost := 0.0
	seen := false
	for i, s := range t.Spans {
		raw := s.Attr(genai.CostUSD)
		if raw != "" {
			c, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return 0, fmt.Errorf("budgets: span[%d] (%q) invalid %s=%q: %w", i, s.Name, genai.CostUSD, raw, err)
			}
			if c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
				return 0, fmt.Errorf("budgets: span[%d] (%q) %s=%q out of range: must be a finite value >= 0", i, s.Name, genai.CostUSD, raw)
			}
			cost += c
			seen = true
			continue
		}
		// No emitted cost. With no pricing table, only emitted cost_usd counts
		// (§4.3 final paragraph) — preserve the legacy behaviour exactly.
		if len(pricing) == 0 {
			continue
		}
		in, inOK, err := tokenAttr(s, genai.InTokens)
		if err != nil {
			return 0, fmt.Errorf("budgets: span[%d] (%q) %w", i, s.Name, err)
		}
		out, outOK, err := tokenAttr(s, genai.OutTokens)
		if err != nil {
			return 0, fmt.Errorf("budgets: span[%d] (%q) %w", i, s.Name, err)
		}
		if !inOK && !outOK {
			continue // not an LLM call (e.g. a tool/service span) — contributes 0
		}
		model := s.Attr(genai.RequestModel)
		rate, ok := pricing[model]
		if model == "" || !ok {
			return 0, fmt.Errorf("budgets: span[%d] (%q): cannot derive cost: model %q not in pricing table", i, s.Name, model)
		}
		cost += float64(in)/1e6*rate.InputPerMTok + float64(out)/1e6*rate.OutputPerMTok
		seen = true
	}
	if !seen {
		return 0, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
	}
	return cost, nil
}

// tokenAttr parses a non-negative integer token attribute. ok is false when the
// attribute is absent. A malformed or negative value is an error (mirrors
// tokenSum's domain check).
func tokenAttr(s *trace.Span, key string) (int, bool, error) {
	raw := s.Attr(key)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("invalid %s=%q: %w", key, raw, err)
	}
	if n < 0 {
		return 0, false, fmt.Errorf("%s=%q out of range: must be a value >= 0", key, raw)
	}
	return n, true, nil
}
```

- [ ] **Step 14: Run the derivation test to verify it passes**

Run: `go test ./internal/comparator/ -run TestCostSumDerivesFromTokens -v`
Expected: PASS (all four rows).

- [ ] **Step 15: Write the CEL/budgets parity test**

Add to `internal/comparator/cel_test.go` a test proving the `cost` CEL variable derives identically to `budgets` for the same trace + pricing:

```go
// TestCostParityBudgetsAndCEL pins spec §5: the cel `cost` variable and budgets
// derive the same value from the same trace + pricing table. The trace carries
// tokens + model but no emitted cost, so both must derive 30.0.
func TestCostParityBudgetsAndCEL(t *testing.T) {
	pricing := core.Pricing{"m": {InputPerMTok: 10, OutputPerMTok: 20}}
	tr := derivableTrace(1_000_000, 1_000_000, "m") // derives to $30.00
	ev := core.Evidence{Trace: tr}

	// budgets: $30.00 is within a $30.00 budget but over a $29.99 budget.
	within := floatPtr(30.0)
	over := floatPtr(29.99)
	if v, err := NewBudgets(pricing).Compare(context.Background(), ev, BudgetExpectation{MaxCostUSD: within}); err != nil || !v.Pass {
		t.Fatalf("budgets within: pass=%v err=%v", v.Pass, err)
	}
	if v, err := NewBudgets(pricing).Compare(context.Background(), ev, BudgetExpectation{MaxCostUSD: over}); err != nil || v.Pass {
		t.Fatalf("budgets over: pass=%v err=%v", v.Pass, err)
	}

	// cel: the same derived value satisfies `cost == 30.0`.
	if v, err := NewCEL(pricing).Compare(context.Background(), ev, CELExpectation{Expr: `cost == 30.0`}); err != nil || !v.Pass {
		t.Fatalf("cel cost==30.0: pass=%v err=%v reasons=%v", v.Pass, err, v.Reasons)
	}
}
```

(`context`, `core`, `floatPtr` (from `budgets_test.go`, same package), and `derivableTrace` are available in-package.)

- [ ] **Step 16: Run the parity test**

Run: `go test ./internal/comparator/ -run TestCostParityBudgetsAndCEL -v`
Expected: PASS.

- [ ] **Step 17: Write the engine `toPricing` test**

Create `internal/engine/build_test.go`:

```go
package engine

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
)

func TestToPricing(t *testing.T) {
	t.Run("empty maps to nil", func(t *testing.T) {
		if got := toPricing(nil); got != nil {
			t.Fatalf("toPricing(nil) = %v, want nil", got)
		}
		if got := toPricing(config.Pricing{}); got != nil {
			t.Fatalf("toPricing(empty) = %v, want nil", got)
		}
	})
	t.Run("converts rates", func(t *testing.T) {
		in := config.Pricing{"m": {InputPerMTok: 3, OutputPerMTok: 15}}
		got := toPricing(in)
		r, ok := got["m"]
		if !ok {
			t.Fatalf("missing model m in %v", got)
		}
		if r.InputPerMTok != 3 || r.OutputPerMTok != 15 {
			t.Fatalf("rate = %+v, want {3 15}", r)
		}
	})
}
```

- [ ] **Step 18: Run the engine test**

Run: `go test ./internal/engine/ -run TestToPricing -v`
Expected: PASS.

- [ ] **Step 19: Verify all touched packages + format/vet/coverage**

Run:
```bash
go test ./internal/comparator/ ./internal/engine/ ./internal/config/ ./internal/genai/ ./internal/core/
gofmt -l internal/comparator/ internal/engine/ internal/config/ internal/genai/ internal/core/
go vet ./internal/comparator/ ./internal/engine/ ./internal/config/ ./internal/genai/ ./internal/core/
bash .claude/skills/coverage/coverage.sh ./internal/comparator/
bash .claude/skills/coverage/coverage.sh ./internal/engine/
bash .claude/skills/coverage/coverage.sh ./internal/config/
```
Expected: all tests pass, `gofmt -l` prints nothing, vet clean, each package coverage ≥80%.

- [ ] **Step 20: Commit derivation**

```bash
git add internal/comparator/budgets.go internal/comparator/budgets_test.go internal/comparator/cel_test.go internal/engine/build_test.go
git commit -m "feat(comparator): derive cost from tokens via per-model pricing table"
```

---

## Task 5: A3 — `mentatctl service` (full mirror)

Refactor the single-domain dispatcher into `mentatctl <agent|service> <verb>`. Shared verbs (`run`, `trace`, `replay`, `diff`) reuse adapter-agnostic engine/ctl paths; only the domain-specific listing verb (`tools` vs `services`) and `diff`'s selection differ. The service path reuses the sequence comparator's `Kind:"service"` selection (single source of truth with the `services` CEL variable) by exporting `comparator.ServiceSequence`.

**Files:**
- Modify: `internal/comparator/sequence.go`
- Modify: `internal/ctl/format.go`, `internal/ctl/diff.go`
- Modify: `internal/ctl/format_test.go`, `internal/ctl/diff_test.go`
- Modify: `cmd/mentatctl/main.go`
- Create: `cmd/mentatctl/main_test.go`

**Interfaces:**
- Consumes: `comparator` package (new import in `ctl`; no cycle — `comparator` imports neither `ctl` nor `engine`), `ctl.Resolve`, `ctl.FormatTools`, `ctl.Diff`, `ctl.Run`, `ctl.ReplayFeature` (existing).
- Produces:
  - `comparator.ServiceSequence(t *trace.Trace) ([]string, error)` — exported wrapper over the existing `serviceSequence`.
  - `ctl.FormatServices(tr *trace.Trace, w io.Writer) error`.
  - `ctl.DiffServices(ctx, cor, st, idA, idB string, w io.Writer) error`.
  - `main.splitDomainVerb(args []string) (domain, sub string, rest []string, err error)`.

### Export the service selection

- [ ] **Step 1: Export `ServiceSequence` from the comparator**

In `internal/comparator/sequence.go`, add an exported wrapper directly above the unexported `serviceSequence` (`:106`):

```go
// ServiceSequence returns the distinct services in first-seen call order. It is
// the exported entry point for the ctl service-format/diff paths, so they share
// the sequence comparator's selection — single source of truth with the
// `services` CEL variable.
func ServiceSequence(t *trace.Trace) ([]string, error) { return serviceSequence(t) }
```

- [ ] **Step 2: Verify the comparator still builds and tests pass**

Run: `go test ./internal/comparator/ -run TestOrderflowSequenceService -v && go build ./internal/comparator/`
Expected: PASS, build clean (the wrapper is exercised indirectly now; a ctl test will cover it directly in Step 5).

### `FormatServices` (TDD)

- [ ] **Step 3: Write the failing `FormatServices` test**

Add to `internal/ctl/format_test.go` (package already imports `bytes`, `errors`, `strings`, `testing`, `genai`, `trace`). First a service-forest helper, then the test:

```go
// serviceForest builds a trace where each span carries a service.name resource
// attr and a real Start, so ServiceSequence orders them first-seen.
func serviceForest(run string, services ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	base := time.Unix(0, 0)
	for i, name := range services {
		s := &trace.Span{
			ID:    run + string(rune('a'+i)),
			Name:  "POST",
			Start: base.Add(time.Duration(i) * time.Millisecond),
			Attrs: map[string]string{"service.name": name},
		}
		tr.Spans = append(tr.Spans, s)
	}
	return tr
}

func TestFormatServices(t *testing.T) {
	tests := []struct {
		name     string
		tr       *trace.Trace
		wantSubs []string
		wantErr  bool
	}{
		{
			name:     "nil trace prints marker",
			tr:       nil,
			wantSubs: []string{"(no trace)"},
		},
		{
			name:     "lists distinct services in first-seen order",
			tr:       serviceForest("r1", "auth", "inventory", "payment", "notify"),
			wantSubs: []string{"4 service call", "1. auth", "2. inventory", "3. payment", "4. notify"},
		},
		{
			name:    "span missing service.name is a hard error",
			tr:      &trace.Trace{RunID: "r2", Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{}}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			err := FormatServices(tt.tr, &b)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FormatServices err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(b.String(), want) {
					t.Fatalf("output missing %q in:\n%s", want, b.String())
				}
			}
		})
	}
}
```

Add `"time"` to the `format_test.go` import block.

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestFormatServices -v`
Expected: FAIL to compile — `FormatServices` is undefined.

- [ ] **Step 5: Implement `FormatServices`**

In `internal/ctl/format.go`, add the `comparator` import:

```go
import (
	"fmt"
	"io"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)
```

Append the function:

```go
// FormatServices lists the distinct services in first-seen call order, mirroring
// FormatTools for the service domain. It reuses the sequence comparator's service
// selection (single source of truth with the `services` CEL variable).
func FormatServices(tr *trace.Trace, w io.Writer) error {
	if tr == nil {
		if _, err := fmt.Fprintln(w, "(no trace)"); err != nil {
			return fmt.Errorf("ctl: format services no-trace line: %w", err)
		}
		return nil
	}
	svcs, err := comparator.ServiceSequence(tr)
	if err != nil {
		return fmt.Errorf("ctl: format services run %s: %w", tr.RunID, err)
	}
	if _, err := fmt.Fprintf(w, "Run %s: %d service call(s)\n", tr.RunID, len(svcs)); err != nil {
		return fmt.Errorf("ctl: format services header run %s: %w", tr.RunID, err)
	}
	for i, s := range svcs {
		if _, err := fmt.Fprintf(w, "%2d. %s\n", i+1, s); err != nil {
			return fmt.Errorf("ctl: format service line %d run %s: %w", i+1, tr.RunID, err)
		}
	}
	return nil
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/ctl/ -run TestFormatServices -v`
Expected: PASS (all three rows).

### `DiffServices` (TDD)

- [ ] **Step 7: Write the failing `DiffServices` test**

Add to `internal/ctl/diff_test.go` (package already imports `bytes`, `context`, `errors`, `strings`, `testing`, `gomock`, `core`, `mocks`, `genai`, `trace`). The existing `newTestCorrelator`/`newFastCorrelator` helpers are reused; add a service-forest helper local to the test that carries `service.name`:

```go
func svcForest(run string, services ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	base := time.Unix(0, 0)
	for i, name := range services {
		tr.Spans = append(tr.Spans, &trace.Span{
			ID:    run + string(rune('a'+i)),
			Start: base.Add(time.Duration(i) * time.Millisecond),
			Attrs: map[string]string{"service.name": name},
		})
	}
	return tr
}

func TestDiffServices(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			if id == "A" {
				return svcForest("A", "auth", "inventory", "payment"), nil
			}
			return svcForest("B", "auth", "payment", "inventory"), nil
		}).AnyTimes()

	var buf bytes.Buffer
	if err := DiffServices(context.Background(), newTestCorrelator(), st, "A", "B", &buf); err != nil {
		t.Fatalf("DiffServices: %v", err)
	}
	// Position 2 differs: A has inventory, B has payment.
	for _, want := range []string{"inventory", "payment", "≠"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("output missing %q in:\n%s", want, buf.String())
		}
	}
}
```

(`diff_test.go` already imports `"time"` — used by `newTestCorrelator` — so no new import is needed.)

- [ ] **Step 8: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestDiffServices -v`
Expected: FAIL to compile — `DiffServices` is undefined.

- [ ] **Step 9: Refactor `Diff` to share a selection-parameterised core, add `DiffServices`**

In `internal/ctl/diff.go`, add the `comparator` import and refactor. Replace the file's `Diff` function (`:21-53`) and `toolSeq` (`:13-19`) with a shared `diffWith` plus thin wrappers:

```go
package ctl

import (
	"context"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// seqFunc selects an ordered identity sequence from a run's trace. Tool and
// service domains differ only in which selector they pass to diffWith.
type seqFunc func(*trace.Trace) ([]string, error)

func toolSeq(tr *trace.Trace) ([]string, error) {
	var out []string
	for _, s := range tr.ByOp(genai.OpExecuteTool) {
		out = append(out, s.Attr(genai.ToolName))
	}
	return out, nil
}

// Diff compares the ordered tool sequences of two runs, position by position.
func Diff(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error {
	return diffWith(ctx, cor, st, idA, idB, w, toolSeq, "tool")
}

// DiffServices compares the ordered service sequences of two runs (the service
// domain), reusing the sequence comparator's Kind:"service" selection.
func DiffServices(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error {
	return diffWith(ctx, cor, st, idA, idB, w, comparator.ServiceSequence, "service")
}

func diffWith(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer, sel seqFunc, noun string) error {
	ta, err := Resolve(ctx, cor, st, idA)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idA, err)
	}
	tb, err := Resolve(ctx, cor, st, idB)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idB, err)
	}
	a, err := sel(ta)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idA, err)
	}
	b, err := sel(tb)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idB, err)
	}
	if _, err := fmt.Fprintf(w, "A=%s  B=%s\n", idA, idB); err != nil {
		return fmt.Errorf("diff: write header: %w", err)
	}
	if equalSeq(a, b) {
		if _, err := fmt.Fprintf(w, "%s sequences identical\n", noun); err != nil {
			return fmt.Errorf("diff: write identical line: %w", err)
		}
		return nil
	}
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		av, bv := at(a, i), at(b, i)
		mark := " "
		if av != bv {
			mark = "≠"
		}
		if _, err := fmt.Fprintf(w, "%2d %s A:%-15s B:%s\n", i+1, mark, av, bv); err != nil {
			return fmt.Errorf("diff: write line %d: %w", i+1, err)
		}
	}
	return nil
}
```

Keep `equalSeq` and `at` (`:55-72`) unchanged.

> Note: the identical-line text changed from `"tool sequences identical"` to `"<noun> sequences identical"`. The existing `TestDiff` "identical_sequences" row asserts the substring `"identical"`, which still matches — no test edit needed. Confirm in Step 11.

- [ ] **Step 10: Run the `DiffServices` test to verify it passes**

Run: `go test ./internal/ctl/ -run TestDiffServices -v`
Expected: PASS.

- [ ] **Step 11: Run the whole ctl package (no regressions)**

Run: `go test ./internal/ctl/ -v`
Expected: PASS — including the existing `TestDiff` (the `toolSeq` signature change to `([]string, error)` is internal; `TestDiff` still sees `"identical"` and the differing-position output).

### Dispatcher (TDD)

- [ ] **Step 12: Write the failing dispatcher-parse test**

Create `cmd/mentatctl/main_test.go`:

```go
package main

import "testing"

func TestSplitDomainVerb(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantDomain string
		wantSub    string
		wantRest   []string
		wantErr    bool
	}{
		{name: "agent run", args: []string{"agent", "run", "--target", "x"}, wantDomain: "agent", wantSub: "run", wantRest: []string{"--target", "x"}},
		{name: "service services with id", args: []string{"service", "services", "id1"}, wantDomain: "service", wantSub: "services", wantRest: []string{"id1"}},
		{name: "service diff", args: []string{"service", "diff", "a", "b"}, wantDomain: "service", wantSub: "diff", wantRest: []string{"a", "b"}},
		{name: "unknown domain errors", args: []string{"bogus", "run"}, wantErr: true},
		{name: "missing verb errors", args: []string{"agent"}, wantErr: true},
		{name: "no args errors", args: []string{}, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			domain, sub, rest, err := splitDomainVerb(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if domain != tt.wantDomain || sub != tt.wantSub {
				t.Fatalf("got (%q,%q), want (%q,%q)", domain, sub, tt.wantDomain, tt.wantSub)
			}
			if len(rest) != len(tt.wantRest) {
				t.Fatalf("rest=%v want=%v", rest, tt.wantRest)
			}
			for i := range rest {
				if rest[i] != tt.wantRest[i] {
					t.Fatalf("rest[%d]=%q want %q", i, rest[i], tt.wantRest[i])
				}
			}
		})
	}
}
```

- [ ] **Step 13: Run the test to verify it fails**

Run: `go test ./cmd/mentatctl/ -run TestSplitDomainVerb -v`
Expected: FAIL to compile — `splitDomainVerb` is undefined.

- [ ] **Step 14: Refactor `main`/`dispatch` for domain dispatch**

In `cmd/mentatctl/main.go`, replace `main` (`:18-28`) and `dispatch`'s signature and verb handling. New `main`:

```go
func main() {
	domain, sub, rest, err := splitDomainVerb(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: mentatctl <agent|service> <run|trace|tools|services|replay|diff> [flags]")
		os.Exit(2)
	}
	if err := dispatch(domain, sub, rest); err != nil {
		fmt.Fprintln(os.Stderr, "mentatctl:", err)
		os.Exit(1)
	}
}

// splitDomainVerb parses `mentatctl <domain> <verb> [rest...]`. domain must be
// "agent" or "service"; an unknown domain or a missing verb is an error.
func splitDomainVerb(args []string) (domain, sub string, rest []string, err error) {
	if len(args) < 2 {
		return "", "", nil, fmt.Errorf("need <domain> <verb>")
	}
	domain = args[0]
	if domain != "agent" && domain != "service" {
		return "", "", nil, fmt.Errorf("unknown domain %q (want agent or service)", domain)
	}
	return domain, args[1], args[2:], nil
}
```

Change `dispatch`'s signature (`:30`) to take `domain`:

```go
func dispatch(domain, sub string, rest []string) error {
```

Replace the `tools` case (`:84-93`) with domain-aware `tools`/`services`:

```go
	case "tools":
		if domain != "agent" {
			return fmt.Errorf("verb %q is only valid for the agent domain", sub)
		}
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		return ctl.FormatTools(tr, os.Stdout)
	case "services":
		if domain != "service" {
			return fmt.Errorf("verb %q is only valid for the service domain", sub)
		}
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		return ctl.FormatServices(tr, os.Stdout)
```

Make `diff` domain-aware (`:107-111`):

```go
	case "diff":
		if len(args) < 2 {
			return fmt.Errorf("diff: need two run ids")
		}
		if domain == "service" {
			return ctl.DiffServices(ctx, cor, st, args[0], args[1], os.Stdout)
		}
		return ctl.Diff(ctx, cor, st, args[0], args[1], os.Stdout)
```

(The `run`, `trace`, `replay` cases are unchanged — they are adapter-agnostic and work for both domains; the http `checkout` target already drives via the engine.)

- [ ] **Step 15: Run the dispatcher test + build the binary**

Run: `go test ./cmd/mentatctl/ -run TestSplitDomainVerb -v && go build ./cmd/mentatctl/`
Expected: PASS, build clean.

- [ ] **Step 16: Verify ctl + comparator + cmd, format, vet, coverage**

Run:
```bash
go test ./internal/ctl/ ./internal/comparator/ ./cmd/mentatctl/
gofmt -l internal/ctl/ internal/comparator/ cmd/mentatctl/
go vet ./internal/ctl/ ./internal/comparator/ ./cmd/mentatctl/
bash .claude/skills/coverage/coverage.sh ./internal/ctl/
bash .claude/skills/coverage/coverage.sh ./internal/comparator/
```
Expected: tests pass, `gofmt -l` prints nothing, vet clean, `internal/ctl` and `internal/comparator` coverage ≥80% (`cmd/mentatctl` is coverage-exempt).

- [ ] **Step 17: Commit**

```bash
git add internal/comparator/sequence.go internal/ctl/format.go internal/ctl/format_test.go internal/ctl/diff.go internal/ctl/diff_test.go cmd/mentatctl/main.go cmd/mentatctl/main_test.go
git commit -m "feat(mentatctl): add service domain mirror (services/diff)"
```

---

## Final Verification

- [ ] **Run the full hermetic suite + lint:**

```bash
go build ./...
go test ./... -race
gofmt -l .
go vet ./...
make lint        # golangci-lint run ./...
make cover       # per-package floor; all touched packages ≥80% (cmd/* and mocks exempt)
```
Expected: build clean, all tests pass under `-race`, `gofmt -l` prints nothing, vet + lint clean, coverage gate green.

- [ ] **(Optional, requires `make harness-up`) Live demonstration of A1 + A3:**

```bash
make harness-up
go run ./cmd/mentat run features/checkout.feature   # exercises the new schema step
go run ./cmd/mentatctl service services --last       # exercises FormatServices
go test ./e2e/... -tags e2e                          # existing meta red-on-bad suite
make harness-down
```
Expected: `checkout.feature` passes (schema satisfied by `{"status":"confirmed"}`); `service services` lists `auth, inventory, payment, notify`; the e2e meta suite stays green (Mentat still goes red on the bad scenarios).

---

## Sequencing & Independence

- Tasks 1, 2, 3, 4, 5 are independent and may land in any order. Task 4 (A2) is the most invasive (three signature changes, atomic); Tasks 1 (A4) and 2 (A5) are the smallest. The recommended order above (A4 → A5 → A1 → A2 → A3) front-loads the small, low-risk changes.
- A1 (Task 3) ships as a registered `core.Matcher` because the matcher registry (Spec B) already landed in commit `056acc8` (#9). No `switch`-case fallback is needed.
- Each task ends with an independently committable, independently testable deliverable; a reviewer can approve or reject any one task without blocking its neighbours.

---

## Self-Review (run against the spec)

**Spec coverage:**
- A1 `schema` result matcher → **Task 3** (matcher + helpers + step + harness demo). ✔ Reads `Body`; invalid schema and non-JSON body are hard errors; empty body fails with a reason; reasons carry per-instance validator errors.
- A2 cost pricing-table fallback → **Task 4**. ✔ `config.Pricing` + `core.Pricing` mirror; `genai.RequestModel`; emitted-cost-wins precedence; model-not-in-table hard error; empty-table legacy path; `costSum`/`NewBudgets`/`NewCEL` plumbing; CEL parity; `engine.Build` conversion.
- A3 `mentatctl service` → **Task 5**. ✔ `<agent|service>` dispatch; shared `run/trace/replay`; `tools`↔`services`; domain-aware `diff`; shared selection via exported `comparator.ServiceSequence`.
- A4 `regex` Gherkin step → **Task 1**. ✔ Step + green/red godog test.
- A5 traceparent document-and-reserve → **Task 2**. ✔ `PrimaryTraceID` doc + shell no-traceparent test (http already covered).
- New dependency `santhosh-tekuri/jsonschema/v6` → **Task 3, Step 1** (pinned `v6.0.2`). ✔
- Testing matrix (spec §8) → covered per task; hermetic unit/comparator tests; e2e meta suite untouched and referenced in Final Verification.

**Placeholder scan:** No `TBD`/`add error handling`/`similar to`/`write tests for the above` — every code step shows complete code and every run step shows the exact command and expected outcome. ✔

**Type consistency:** `core.Pricing`/`core.ModelRate` (transport-free) vs `config.Pricing`/`config.ModelRate` (yaml) are distinct by package and converted in `engine.toPricing`. `costSum(t, pricing)`, `NewBudgets(pricing)`, `NewCEL(pricing)`, `bindVars(refs, ev, pricing)` signatures are consistent across Task 4. `ServiceSequence`, `FormatServices`, `DiffServices`, `splitDomainVerb` names are consistent across Task 5 and the dispatcher. `seqFunc` is `func(*trace.Trace) ([]string, error)` and both `toolSeq` and `comparator.ServiceSequence` match it. ✔

**One flagged interpretation:** Task 4 records the empty-pricing reconciliation (skip derivation when the table is empty) as an explicit decision for Q to confirm at review — both readings are hard errors, so invariant 4 holds regardless.
