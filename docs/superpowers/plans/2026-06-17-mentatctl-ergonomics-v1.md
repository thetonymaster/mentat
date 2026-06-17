# mentatctl Ergonomics v1 (Plan 2c) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `mentatctl` — the manual/exploratory driver — over the same engine /
driver / correlate / store / comparator libraries: `agent run`, `trace`, `tools`,
`replay`, `diff`, with `--target`/`--last`/`--save`/`--json` conveniences.

**Architecture:** Command *logic* lives in a testable `internal/ctl` package (unit
tested with uber gomock + golden fixtures, no CLI framework). `cmd/mentatctl` is a
thin stdlib-`flag` shell. `replay` adds a small engine "pinned-run" mode so a feature
can be re-evaluated against a stored trace without re-driving the SUT.

**Tech Stack:** Go 1.23+, Plan 2a/2b packages, stdlib (`flag`, `os`, `io`,
`encoding/json`, `path/filepath`), `go.uber.org/mock` (tests), `github.com/cucumber/godog`
(replay reuses the runner). No new third-party deps.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat`.
- **Prerequisites:** Plans 1, 2a, 2b complete (engine, store, correlate, comparators,
  steps, the `mentat run` path, and `internal/core/mocks`).
- **`mentatctl` does not drive via `mentat run`** — it calls the same libraries
  directly. The product BDD path stays `mentat run`.
- **`--save` writes the Plan-1 fixture schema** (`{runScenario,spans:[{name,op,parentIndex,attrs,status}]}`)
  so saved live traces feed L1 unit tests, round-tripping through `store.LoadFixture`.
- **No silent fallbacks**; tests table-driven; gomock for `core` interfaces; touched
  packages ≥80%. Conventional Commits; files added individually; no AI attribution.
- **Shell completion is deferred** (a later nicety); not in this plan.

## File Structure

```
internal/ctl/ctl.go        --last state (~/.mentat/last) + resolve helper + RunOpts
internal/ctl/ctl_test.go
internal/ctl/format.go      FormatForest + FormatTools (Go renderers over trace.Trace)
internal/ctl/format_test.go
internal/ctl/run.go         Run: drive via engine, print summary, save --last
internal/ctl/run_test.go
internal/ctl/save.go        WriteFixture: core.Trace forest -> Plan-1 fixture JSON
internal/ctl/save_test.go
internal/ctl/replay.go      ReplayFeature: run a feature against a pinned stored run
internal/ctl/replay_test.go
internal/ctl/diff.go        Diff: compare tool sequences of two runs
internal/ctl/diff_test.go
internal/engine/engine.go   (modify) add PinRun + Drive honoring a pinned run id
cmd/mentatctl/main.go       stdlib-flag shell: agent run|trace|tools|replay|diff
```

---

### Task 1: ctl foundation — `--last` state + resolve helper

**Files:**
- Create: `internal/ctl/ctl.go`
- Test: `internal/ctl/ctl_test.go`

**Interfaces:**
- Consumes: `core.TraceStore`/`Correlator` (2a), `trace.Trace` (2a).
- Produces: `func LastPath() string`; `func SaveLast(runID string) error`;
  `func ReadLast() (string, error)`; `func Resolve(ctx, cor core.Correlator, st core.TraceStore, runID string) (*trace.Trace, error)`;
  `type RunOpts struct{ Target, Scenario, Prompt string; JSON, Quiet bool; Save string }`.

- [ ] **Step 1: Write the failing test**

Create `internal/ctl/ctl_test.go`:
```go
package ctl

import (
	"os"
	"testing"
)

func TestSaveAndReadLastRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // LastPath lives under $HOME/.mentat/last
	if err := SaveLast("run-123"); err != nil {
		t.Fatalf("SaveLast: %v", err)
	}
	got, err := ReadLast()
	if err != nil {
		t.Fatalf("ReadLast: %v", err)
	}
	if got != "run-123" {
		t.Fatalf("ReadLast = %q, want run-123", got)
	}
}

func TestReadLastErrorsWhenAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := ReadLast(); err == nil {
		t.Fatal("expected error when no last run recorded")
	}
}

func TestLastPathUnderHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/example")
	if LastPath() != "/tmp/example/.mentat/last" {
		t.Fatalf("LastPath = %q", LastPath())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestSaveAndReadLast -v`
Expected: FAIL — `undefined: SaveLast`.

- [ ] **Step 3: Write the implementation**

Create `internal/ctl/ctl.go`:
```go
package ctl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// RunOpts is the parsed input for `mentatctl agent run`.
type RunOpts struct {
	Target   string
	Scenario string
	Prompt   string
	JSON     bool
	Quiet    bool
	Save     string // fixture name; empty = don't save
}

// LastPath is where the most recent interactive run id is cached. Used by --last.
// It is for interactive single runs only — never read by the `mentat` suite runner
// (see CLAUDE.md known limitations).
func LastPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mentat", "last")
}

func SaveLast(runID string) error {
	p := LastPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("ctl: mkdir %s: %w", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(runID+"\n"), 0o644); err != nil {
		return fmt.Errorf("ctl: write last: %w", err)
	}
	return nil
}

func ReadLast() (string, error) {
	b, err := os.ReadFile(LastPath())
	if err != nil {
		return "", fmt.Errorf("ctl: no recent run (run `mentatctl agent run` first): %w", err)
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("ctl: recorded last run is empty")
	}
	return id, nil
}

// Resolve fetches and merges a run's trace forest by run id (no driving).
func Resolve(ctx context.Context, cor core.Correlator, st core.TraceStore, runID string) (*trace.Trace, error) {
	return cor.Resolve(ctx, st, runID)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ctl/ -run 'TestSaveAndReadLast|TestReadLast|TestLastPath' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ctl/ctl.go internal/ctl/ctl_test.go
git commit -m "feat(ctl): --last run-id state + resolve helper"
```

---

### Task 2: Forest + tools renderers

**Files:**
- Create: `internal/ctl/format.go`
- Test: `internal/ctl/format_test.go`

**Interfaces:**
- Consumes: `trace.Trace`/`Span` (2a), `genai` (2a).
- Produces: `func FormatForest(tr *trace.Trace, w io.Writer)`;
  `func FormatTools(tr *trace.Trace, w io.Writer)`.

- [ ] **Step 1: Write the failing test**

Create `internal/ctl/format_test.go`:
```go
package ctl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func sampleForest() *trace.Trace {
	root := &trace.Span{ID: "1", Name: "invoke_agent researchbot",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "1200", genai.OutTokens: "600"}}
	t1 := &trace.Span{ID: "2", ParentID: "1", Name: "execute_tool search",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"}}
	t2 := &trace.Span{ID: "3", ParentID: "1", Name: "execute_tool summarize",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "summarize"}}
	return &trace.Trace{RunID: "r1", Roots: []*trace.Span{root}, Spans: []*trace.Span{root, t1, t2}}
}

func TestFormatForestShowsRootAndChildren(t *testing.T) {
	var b bytes.Buffer
	FormatForest(sampleForest(), &b)
	out := b.String()
	for _, want := range []string{"invoke_agent researchbot", "execute_tool search", "execute_tool summarize"} {
		if !strings.Contains(out, want) {
			t.Fatalf("forest missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatToolsListsSequence(t *testing.T) {
	var b bytes.Buffer
	FormatTools(sampleForest(), &b)
	out := b.String()
	if !strings.Contains(out, "1. search") || !strings.Contains(out, "2. summarize") {
		t.Fatalf("tools sequence wrong:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestFormat -v`
Expected: FAIL — `undefined: FormatForest`.

- [ ] **Step 3: Write the implementation**

Create `internal/ctl/format.go`:
```go
package ctl

import (
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// FormatForest renders the span forest as an indented tree, highlighting gen_ai attrs.
func FormatForest(tr *trace.Trace, w io.Writer) {
	fmt.Fprintf(w, "Run %s (%d spans, %d root trace(s))\n\n", tr.RunID, len(tr.Spans), len(tr.Roots))
	byParent := map[string][]*trace.Span{}
	for _, s := range tr.Spans {
		byParent[s.ParentID] = append(byParent[s.ParentID], s)
	}
	var emit func(s *trace.Span, depth int)
	emit = func(s *trace.Span, depth int) {
		indent := ""
		for i := 0; i < depth; i++ {
			indent += "  "
		}
		extra := ""
		if n, ok := s.AttrInt(genai.InTokens); ok {
			extra += fmt.Sprintf(" in=%d", n)
		}
		if n, ok := s.AttrInt(genai.OutTokens); ok {
			extra += fmt.Sprintf(" out=%d", n)
		}
		if tn := s.Attr(genai.ToolName); tn != "" {
			extra += " tool=" + tn
		}
		fmt.Fprintf(w, "%s+- %s%s\n", indent, s.Name, extra)
		for _, c := range byParent[s.ID] {
			emit(c, depth+1)
		}
	}
	for _, r := range tr.Roots {
		emit(r, 0)
	}
}

// FormatTools lists the execute_tool spans in start order.
func FormatTools(tr *trace.Trace, w io.Writer) {
	tools := tr.ByOp(genai.OpExecuteTool)
	fmt.Fprintf(w, "Run %s: %d tool call(s)\n", tr.RunID, len(tools))
	for i, s := range tools {
		fmt.Fprintf(w, "%2d. %s\n", i+1, s.Attr(genai.ToolName))
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ctl/ -run TestFormat -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ctl/format.go internal/ctl/format_test.go
git commit -m "feat(ctl): forest + tool-sequence renderers"
```

---

### Task 3: `run` — drive + summary + save --last

**Files:**
- Create: `internal/ctl/run.go`
- Test: `internal/ctl/run_test.go`

**Interfaces:**
- Consumes: `engine.Engine` (2b), `core.Evidence` (2a), `genai` (2a), Task 1/2 helpers.
- Produces: `func Run(ctx context.Context, eng *engine.Engine, opts RunOpts, w io.Writer) (core.Evidence, error)`
  — builds drive args from opts (`--scenario`/`--prompt`), drives, prints a compact
  summary (or `--json`/`--quiet`), and records `--last`.

- [ ] **Step 1: Write the failing test**

Create `internal/ctl/run_test.go`:
```go
package ctl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestRunDrivesPrintsSummaryAndSavesLast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	tr := &trace.Trace{RunID: "run-1", Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "run-1"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, _ := engine.Build(cfg, st, cor)

	var b bytes.Buffer
	ev, err := Run(context.Background(), eng, RunOpts{Target: "bot", Scenario: "happy"}, &b)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ev.Output.Answer != "hi" {
		t.Fatalf("answer = %q", ev.Output.Answer)
	}
	if !strings.Contains(b.String(), "run-1") || !strings.Contains(b.String(), "hi") {
		t.Fatalf("summary missing run id/answer:\n%s", b.String())
	}
	if got, _ := ReadLast(); got != "run-1" {
		t.Fatalf("last not saved: %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestRunDrives -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the implementation**

Create `internal/ctl/run.go`:
```go
package ctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
)

func Run(ctx context.Context, eng *engine.Engine, opts RunOpts, w io.Writer) (core.Evidence, error) {
	var args []string
	switch {
	case opts.Scenario != "":
		args = []string{"--scenario", opts.Scenario}
	case opts.Prompt != "":
		args = []string{"--prompt", opts.Prompt}
	}
	ev, err := eng.Drive(ctx, opts.Target, args)
	if err != nil {
		return core.Evidence{}, err
	}
	if err := SaveLast(ev.RunID); err != nil {
		return ev, err // surface; do not swallow
	}

	switch {
	case opts.Quiet:
		fmt.Fprintln(w, ev.Output.Answer)
	case opts.JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"runID":  ev.RunID,
			"answer": ev.Output.Answer,
			"tools":  toolNames(ev),
			"spans":  len(ev.Trace.Spans),
		})
	default:
		fmt.Fprintf(w, "run %s\n", ev.RunID)
		fmt.Fprintf(w, "tools: %v\n", toolNames(ev))
		fmt.Fprintf(w, "spans: %d\n", len(ev.Trace.Spans))
		fmt.Fprintf(w, "answer: %s\n", ev.Output.Answer)
	}
	return ev, nil
}

func toolNames(ev core.Evidence) []string {
	var names []string
	for _, s := range ev.Trace.ByOp(genai.OpExecuteTool) {
		names = append(names, s.Attr(genai.ToolName))
	}
	return names
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/ctl/ -run TestRunDrives -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ctl/run.go internal/ctl/run_test.go
git commit -m "feat(ctl): agent run — drive, summary, save --last"
```

---

### Task 4: `--save` — write a live trace as a golden fixture

**Files:**
- Create: `internal/ctl/save.go`
- Test: `internal/ctl/save_test.go`

**Interfaces:**
- Consumes: `trace.Trace`/`Span` (2a), `genai` (2a), `store.LoadFixture` (2a, for the round-trip test).
- Produces: `func WriteFixture(tr *trace.Trace, path string) error` — emits the Plan-1
  fixture schema (`runScenario`, `spans[]` with `name`/`op`/`parentIndex`/`attrs`/`status`).

- [ ] **Step 1: Write the failing test**

Create `internal/ctl/save_test.go`:
```go
package ctl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/store"
)

func TestWriteFixtureRoundTripsThroughLoadFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "happy.json")
	if err := WriteFixture(sampleForest(), path); err != nil {
		t.Fatalf("WriteFixture: %v", err)
	}
	data, _ := os.ReadFile(path)
	tr, err := store.LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(tr.Roots) != 1 || tr.Roots[0].Name != "invoke_agent researchbot" {
		t.Fatalf("round-trip root wrong: %+v", tr.Roots)
	}
	if len(tr.ByOp(genai.OpExecuteTool)) != 2 {
		t.Fatalf("round-trip tool count wrong")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestWriteFixture -v`
Expected: FAIL — `undefined: WriteFixture`.

- [ ] **Step 3: Write the implementation**

Create `internal/ctl/save.go`:
```go
package ctl

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

type fixtureSpan struct {
	Name        string            `json:"name"`
	Op          string            `json:"op"`
	ParentIndex int               `json:"parentIndex"`
	Attrs       map[string]string `json:"attrs"`
	Status      string            `json:"status"`
}

type fixture struct {
	RunScenario string        `json:"runScenario"`
	Spans       []fixtureSpan `json:"spans"`
}

// WriteFixture serializes a live trace forest into the Plan-1 fixture schema so it
// can feed L1 unit tests via store.LoadFixture. Parentage is by index; root first.
func WriteFixture(tr *trace.Trace, path string) error {
	idx := map[string]int{}
	ordered := append([]*trace.Span{}, tr.Roots...)
	for _, s := range tr.Spans {
		isRoot := false
		for _, r := range tr.Roots {
			if r == s {
				isRoot = true
			}
		}
		if !isRoot {
			ordered = append(ordered, s)
		}
	}
	for i, s := range ordered {
		idx[s.ID] = i
	}
	out := fixture{RunScenario: tr.RunID}
	for _, s := range ordered {
		parent := -1
		if pi, ok := idx[s.ParentID]; ok && s.ParentID != "" {
			parent = pi
		}
		out.Spans = append(out.Spans, fixtureSpan{
			Name:        s.Name,
			Op:          s.Attr(genai.Op),
			ParentIndex: parent,
			Attrs:       s.Attrs,
			Status:      s.Status,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/ctl/ -run TestWriteFixture -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ctl/save.go internal/ctl/save_test.go
git commit -m "feat(ctl): --save live trace to Plan-1 fixture schema"
```

---

### Task 5: `replay` — engine pinned-run + re-evaluate a feature

**Files:**
- Modify: `internal/engine/engine.go` (add pinned-run mode)
- Create: `internal/ctl/replay.go`
- Test: `internal/ctl/replay_test.go`

**Interfaces:**
- Produces: `func (e *Engine) PinRun(runID string)` — when set, `Drive` skips inject +
  the driver and resolves the pinned run id instead.
  `func ReplayFeature(ctx context.Context, eng *engine.Engine, runID, featurePath, scenario string, w io.Writer) error`
  — pins the run, runs the feature via godog, writes the report to `w`, returns an
  error if the suite fails.

- [ ] **Step 1: Add the pinned-run mode to the engine**

In `internal/engine/engine.go`, add a field and method, and guard `Drive`:
```go
type Engine struct {
	cfg    config.Config
	cor    core.Correlator
	st     core.TraceStore
	sems   map[string]chan struct{}
	pinned string // when set, Drive resolves this run id instead of driving
}

// PinRun makes subsequent Drive calls resolve runID from the store instead of
// running the SUT — used by `mentatctl agent replay` to re-evaluate a stored run.
func (e *Engine) PinRun(runID string) { e.pinned = runID }
```
At the top of `Drive`, before looking up the target:
```go
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	if e.pinned != "" {
		tr, err := e.cor.Resolve(ctx, e.st, e.pinned)
		if err != nil {
			return core.Evidence{}, err
		}
		return core.Evidence{RunID: e.pinned, Trace: tr}, nil
	}
	// ... existing drive logic ...
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/ctl/replay_test.go`:
```go
package ctl

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
)

func TestReplayFeatureEvaluatesStoredRunWithoutDriving(t *testing.T) {
	// A target whose command would FAIL if driven — proving replay does NOT drive.
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"false"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(sampleForest(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, _ := engine.Build(cfg, st, cor)

	feature := writeTempFeature(t, `Feature: replay
  Scenario: stored run had the right tools
    Given the agent target "bot"
    When I run scenario "ignored"
    Then the agent calls tools in order:
      | search    |
      | summarize |
`)
	var b bytes.Buffer
	if err := ReplayFeature(context.Background(), eng, "r", feature, "", &b); err != nil {
		t.Fatalf("replay should pass against the stored forest: %v\n%s", err, b.String())
	}
}
```
Add this helper at the bottom of `replay_test.go`:
```go
func writeTempFeature(t *testing.T, body string) string {
	t.Helper()
	p := t.TempDir() + "/f.feature"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
```
(import `"os"` in the test.)

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestReplayFeature -v`
Expected: FAIL — `undefined: ReplayFeature`.

- [ ] **Step 4: Write the implementation**

Create `internal/ctl/replay.go`:
```go
package ctl

import (
	"context"
	"fmt"
	"io"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/steps"
)

// ReplayFeature re-evaluates a feature against a STORED run (no driving). It pins the
// engine to runID, then runs the feature through the same godog step grammar.
func ReplayFeature(ctx context.Context, eng *engine.Engine, runID, featurePath, scenario string, w io.Writer) error {
	eng.PinRun(runID)
	opts := godog.Options{
		Format: "pretty",
		Paths:  []string{featurePath},
		Output: w,
		Tags:   scenario, // empty = all scenarios in the file
	}
	suite := godog.TestSuite{ScenarioInitializer: steps.Initializer(eng), Options: &opts}
	if status := suite.Run(); status != 0 {
		return fmt.Errorf("replay: feature failed against run %s (status %d)", runID, status)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests (engine + ctl) to verify they pass**

Run: `go test ./internal/engine/ ./internal/ctl/ -run 'Drive|Replay' -v`
Expected: PASS — replay passes using the stored forest, and the `false` command was
never executed (no driving).

- [ ] **Step 6: Commit**

```bash
git add internal/engine/engine.go internal/ctl/replay.go internal/ctl/replay_test.go
git commit -m "feat(ctl): replay a feature against a pinned stored run (no re-drive)"
```

---

### Task 6: `diff` — compare two runs' tool sequences

**Files:**
- Create: `internal/ctl/diff.go`
- Test: `internal/ctl/diff_test.go`

**Interfaces:**
- Consumes: `core.TraceStore`/`Correlator` (2a), Task 1 `Resolve`, `genai` (2a).
- Produces: `func Diff(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error`
  — resolves both runs, compares the ordered tool-name sequences, prints a per-position
  diff and returns nil (the diff itself is the output; identical sequences print "identical").

- [ ] **Step 1: Write the failing test**

Create `internal/ctl/diff_test.go`:
```go
package ctl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func toolForest(run string, tools ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	for i, name := range tools {
		s := &trace.Span{ID: run + string(rune('a'+i)),
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: name}}
		tr.Spans = append(tr.Spans, s)
	}
	return tr
}

func TestDiffMarksDifferingPositions(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			if id == "A" {
				return toolForest("A", "search", "summarize"), nil
			}
			return toolForest("B", "search", "delete_record"), nil
		}).AnyTimes()
	cor := correlate.New(func() string { return "" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	var b bytes.Buffer
	if err := Diff(context.Background(), cor, st, "A", "B", &b); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(b.String(), "summarize") || !strings.Contains(b.String(), "delete_record") {
		t.Fatalf("diff did not surface the differing tools:\n%s", b.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/ctl/ -run TestDiff -v`
Expected: FAIL — `undefined: Diff`.

- [ ] **Step 3: Write the implementation**

Create `internal/ctl/diff.go`:
```go
package ctl

import (
	"context"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func toolSeq(tr *trace.Trace) []string {
	var out []string
	for _, s := range tr.ByOp(genai.OpExecuteTool) {
		out = append(out, s.Attr(genai.ToolName))
	}
	return out
}

// Diff compares the ordered tool sequences of two runs, position by position.
func Diff(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error {
	ta, err := Resolve(ctx, cor, st, idA)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idA, err)
	}
	tb, err := Resolve(ctx, cor, st, idB)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idB, err)
	}
	a, b := toolSeq(ta), toolSeq(tb)
	fmt.Fprintf(w, "A=%s  B=%s\n", idA, idB)
	if equalSeq(a, b) {
		fmt.Fprintln(w, "tool sequences identical")
		return nil
	}
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := at(a, i), at(b, i)
		mark := " "
		if av != bv {
			mark = "≠"
		}
		fmt.Fprintf(w, "%2d %s A:%-15s B:%s\n", i+1, mark, av, bv)
	}
	return nil
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "—"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ctl/ -run TestDiff -v`
Expected: PASS.

- [ ] **Step 5: Coverage gate for the package**

Run: `go test ./internal/ctl/ -cover`
Expected: PASS at ≥80% (add table rows if below).

- [ ] **Step 6: Commit**

```bash
git add internal/ctl/diff.go internal/ctl/diff_test.go
git commit -m "feat(ctl): diff two runs' tool sequences"
```

---

### Task 7: `cmd/mentatctl` shell

**Files:**
- Create: `cmd/mentatctl/main.go`

**Interfaces:**
- Consumes: `config` (2b), `store.NewTempo` (2b), `correlate.New` (2a), `engine.Build` (2b),
  all `ctl` functions (Tasks 1–6).
- Produces: the `mentatctl` binary:
  `mentatctl agent run|trace|tools|replay|diff [flags]`.

- [ ] **Step 1: Write the shell**

Create `cmd/mentatctl/main.go`:
```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/ctl"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/store"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "agent" {
		fmt.Fprintln(os.Stderr, "usage: mentatctl agent <run|trace|tools|replay|diff> [flags]")
		os.Exit(2)
	}
	sub, rest := os.Args[2], os.Args[3:]
	if err := dispatch(sub, rest); err != nil {
		fmt.Fprintln(os.Stderr, "mentatctl:", err)
		os.Exit(1)
	}
}

func dispatch(sub string, rest []string) error {
	ctx := context.Background()
	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	cfgPath := fs.String("config", "mentat.yaml", "config file")
	target := fs.String("target", "", "named target from mentat.yaml")
	scenario := fs.String("scenario", "", "harness scenario")
	prompt := fs.String("prompt", "", "prompt")
	last := fs.Bool("last", false, "use the most recent run id")
	asJSON := fs.Bool("json", false, "machine output")
	quiet := fs.Bool("quiet", false, "answer only")
	save := fs.String("save", "", "save the run's trace as a fixture at this path")
	feature := fs.String("feature", "", "feature file (replay)")
	_ = fs.Parse(rest)
	args := fs.Args()

	cfg, st, cor, err := deps(*cfgPath)
	if err != nil {
		return err
	}

	idArg := func() (string, error) {
		if *last {
			return ctl.ReadLast()
		}
		if len(args) == 0 {
			return "", fmt.Errorf("%s: need a run id (or --last)", sub)
		}
		return args[0], nil
	}

	switch sub {
	case "run":
		eng, _ := engine.Build(cfg, st, cor)
		ev, err := ctl.Run(ctx, eng, ctl.RunOpts{Target: *target, Scenario: *scenario, Prompt: *prompt, JSON: *asJSON, Quiet: *quiet, Save: *save}, os.Stdout)
		if err != nil {
			return err
		}
		if *save != "" {
			return ctl.WriteFixture(ev.Trace, *save)
		}
		return nil
	case "trace":
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		ctl.FormatForest(tr, os.Stdout)
		return nil
	case "tools":
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		ctl.FormatTools(tr, os.Stdout)
		return nil
	case "replay":
		id, err := idArg()
		if err != nil {
			return err
		}
		if *feature == "" {
			return fmt.Errorf("replay: --feature is required to re-evaluate a run")
		}
		eng, _ := engine.Build(cfg, st, cor)
		return ctl.ReplayFeature(ctx, eng, id, *feature, "", os.Stdout)
	case "diff":
		if len(args) < 2 {
			return fmt.Errorf("diff: need two run ids")
		}
		return ctl.Diff(ctx, cor, st, args[0], args[1], os.Stdout)
	default:
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

func deps(cfgPath string) (config.Config, core.TraceStore, core.Correlator, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	cfg, err := config.Load(data)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	st := store.NewTempo(cfg.Tempo.Endpoint, nil)
	pc := correlate.PollConfig{
		Interval:  mustDur(cfg.Poll.Interval, 200*time.Millisecond),
		Timeout:   mustDur(cfg.Poll.Timeout, 30*time.Second),
		StableFor: orDefault(cfg.Poll.StableFor, 3),
	}
	cor := correlate.New(func() string { return uuid.NewString() }, pc)
	return cfg, st, cor, nil
}

func mustDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}
func orDefault(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
```
- [ ] **Step 2: Build the binary**

Run: `go build ./cmd/mentatctl`
Expected: builds `mentatctl`.

- [ ] **Step 3: Smoke the help/usage path**

Run: `go run ./cmd/mentatctl 2>&1 | head -1`
Expected: the usage line.

- [ ] **Step 4: Commit**

```bash
git add cmd/mentatctl/main.go
git commit -m "feat(cmd): mentatctl agent run/trace/tools/replay/diff shell"
```

---

## Done criteria for Plan 2c

- `go test ./internal/ctl/ ./internal/engine/` passes; `internal/ctl` ≥ 80% coverage.
- `go build ./cmd/mentatctl` builds.
- `mentatctl agent run --target research-agent --scenario happy` prints a summary and
  records `--last` (with `make harness-up` running); `trace`/`tools`/`diff`/`replay`
  operate on stored runs. `replay --feature f.feature <runID>` re-evaluates without
  re-driving.

This completes the **full v1 surface** (Plans 1, 2a, 2b, 2c). Remaining work is ops/CI
glue (separate plan) and the post-v1 phases (microservices, shape, semantic, breadth).
