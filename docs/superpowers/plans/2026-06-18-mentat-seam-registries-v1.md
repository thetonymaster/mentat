# Mentat Seam Registries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-seam registries for the **matcher** and **store** seams (architecture invariant #3), migrating the `result` matchers off a hard-coded `switch` and the Tempo store off hard-coded construction, while leaving the working comparator/driver registries and the hermetic test path untouched.

**Architecture:** Two seams, two registries. The **matcher** seam gains a `core.Matcher` interface and an *instance*-based registry (matchers are stateless, like comparators); `result.Compare` becomes a thin dispatcher and the five built-ins register at the composition root. The **store** seam gains a *factory*-based registry (stores are stateful); a new `store:` config field selects the store and a new `engine.BuildStore` is the store composition root, with `engine.Build` keeping its existing `TraceStore` parameter so hermetic tests still inject `InMemStore` directly.

**Tech Stack:** Go, `github.com/cucumber/godog` (BDD), `go.uber.org/mock` (mocks where interfaces are mocked), standard `testing` (table-driven).

## Global Constraints

- Module path: `github.com/thetonymaster/mentat`.
- `gofmt -l .` clean and `go vet ./...` clean before every commit; run `golangci-lint run` (a `.golangci.yml` exists).
- No silent fallbacks: a function that cannot do its job returns a wrapped `error` (`%w`), never a zero-value success or guessed result.
- Interfaces are small and defined by the consumer; seams wired at the single composition root.
- Tests are table-driven by default; coverage floor is **80% per package** (`cmd/` and `mocks/` are exempt).
- Conventional Commits (`feat:`, `fix:`, `test:`, `refactor:`, `chore:`); `git add .` is forbidden — stage files individually; **no AI attribution** in commits.
- This plan is **behavior-preserving** for `result`: every existing `internal/comparator/result_test.go` case must stay green.
- Sequencing note: this plan is a prerequisite for Spec A's `schema` matcher (`docs/superpowers/specs/2026-06-18-mentat-v1-gap-closure-design.md` §3) — once Task 2 lands, `schema` registers through `RegisterMatcher` with no change to `result`.

---

## File Structure

**Matcher seam:**
- `internal/core/core.go` — add the `Matcher` interface beside `Comparator`/`Driver`/`TraceStore`/`Correlator`.
- `internal/registry/registry.go` — add the instance-based matcher registry.
- `internal/comparator/matchers.go` *(new)* — the five built-in `core.Matcher` types, their shared helpers (`targetString`, `jsonSubset`, `subset`), and `RegisterBuiltinMatchers()`.
- `internal/comparator/result.go` — `Compare` becomes a registry dispatcher; `ResultExpectation` stays.
- `internal/comparator/matchers_test.go` *(new)* — `TestMain` that registers built-ins for the package, plus a dispatch test proving a custom matcher is invoked.
- `internal/registry/registry_test.go` — add matcher registry round-trip tests.
- `internal/engine/build.go` — call `comparator.RegisterBuiltinMatchers()` at the composition root.

**Store seam:**
- `internal/registry/registry.go` — add the factory-based store registry.
- `internal/config/config.go` — add `Store string` field, default `"tempo"`.
- `internal/engine/store.go` *(new)* — `BuildStore(cfg)`: registers the built-in Tempo factory and resolves `cfg.Store`.
- `internal/engine/store_test.go` *(new)* — `BuildStore` resolves `tempo`, errors on unknown.
- `internal/registry/registry_test.go` — add store registry round-trip tests.
- `internal/config/config_test.go` — add the `Store` default test.
- `cmd/mentat/main.go` and `cmd/mentatctl/main.go` — select the store via `engine.BuildStore` instead of `store.NewTempo`.

---

## Task 1: Matcher seam — `core.Matcher` interface + registry

**Files:**
- Modify: `internal/core/core.go` (add interface after `Correlator`, ~`core.go:87-90`)
- Modify: `internal/registry/registry.go` (add after the comparator block, `registry.go:14`)
- Test: `internal/registry/registry_test.go` (append; match the file's existing `package` clause)

**Interfaces:**
- Consumes: `core.Evidence`, `core.Verdict` (existing).
- Produces:
  - `core.Matcher` interface: `Name() string` and `Match(ctx context.Context, ev core.Evidence, want, target string) (core.Verdict, error)`.
  - `registry.RegisterMatcher(name string, m core.Matcher)` and `registry.Matcher(name string) (core.Matcher, bool)`.

- [ ] **Step 1: Write the failing registry test**

Append to `internal/registry/registry_test.go`:

```go
// fakeMatcher is a minimal core.Matcher for registry round-trip tests.
type fakeMatcher struct{ name string }

func (f fakeMatcher) Name() string { return f.name }
func (f fakeMatcher) Match(_ context.Context, _ core.Evidence, _, _ string) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

func TestMatcherRegistryRoundTrip(t *testing.T) {
	RegisterMatcher("fake", fakeMatcher{name: "fake"})
	got, ok := Matcher("fake")
	if !ok {
		t.Fatal("Matcher(\"fake\") not found after RegisterMatcher")
	}
	if got.Name() != "fake" {
		t.Fatalf("Name() = %q, want %q", got.Name(), "fake")
	}
}

func TestMatcherRegistryMissReturnsFalse(t *testing.T) {
	if _, ok := Matcher("nope-not-registered"); ok {
		t.Fatal("Matcher(unregistered) returned ok=true, want false")
	}
}
```

If `registry_test.go` does not already import `"context"` and `core`, add them to its import block.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/registry/ -run TestMatcherRegistry -v`
Expected: FAIL — `undefined: RegisterMatcher` / `undefined: Matcher` (and `core.Matcher` undefined).

- [ ] **Step 3: Add the `core.Matcher` interface**

In `internal/core/core.go`, after the `Correlator` interface (`core.go:87-90`):

```go
// Matcher is one strategy inside the result comparator. It reads the run's
// Output (selected by target for value matchers; Body/Status for structural
// matchers) and returns a Verdict. Matchers are stateless and registered as
// shared instances at the composition root.
type Matcher interface {
	Name() string
	Match(ctx context.Context, ev Evidence, want, target string) (Verdict, error)
}
```

- [ ] **Step 4: Add the matcher registry**

In `internal/registry/registry.go`, extend the `var` block and add the two functions after the comparator helpers (`registry.go:14`):

```go
var matchers = map[string]core.Matcher{}

// RegisterMatcher registers a result Matcher under the given name.
func RegisterMatcher(name string, m core.Matcher) { matchers[name] = m }

// Matcher resolves a registered Matcher by name.
func Matcher(name string) (core.Matcher, bool) { m, ok := matchers[name]; return m, ok }
```

(Add `matchers` to the existing `var (...)` group at `registry.go:5-8`.)

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/registry/ -run TestMatcherRegistry -v`
Expected: PASS (both cases).

- [ ] **Step 6: gofmt + vet, then commit**

```bash
gofmt -w internal/core/core.go internal/registry/registry.go internal/registry/registry_test.go
go vet ./internal/core/ ./internal/registry/
git add internal/core/core.go internal/registry/registry.go internal/registry/registry_test.go
git commit -m "feat(registry): core.Matcher interface and matcher registry"
```

---

## Task 2: Migrate the `result` matchers onto the seam

Behavior-preserving refactor: extract the five matchers into `core.Matcher` types, make `result.Compare` dispatch via the registry, and register the built-ins at the composition root. The existing `result_test.go` is the safety net; a new dispatch test proves pluggability.

**Files:**
- Create: `internal/comparator/matchers.go`
- Create: `internal/comparator/matchers_test.go`
- Modify: `internal/comparator/result.go` (replace the `switch` in `Compare`, `result.go:44-54`; move helpers out)
- Modify: `internal/engine/build.go` (`build.go:22`, register built-ins)

**Interfaces:**
- Consumes: `core.Matcher`, `registry.RegisterMatcher`, `registry.Matcher` (Task 1); `core.Evidence`, `core.Verdict`.
- Produces:
  - Unexported matcher types in `comparator`: `exactMatcher`, `containsMatcher`, `regexMatcher`, `jsonSubsetMatcher`, `statusMatcher` (each `core.Matcher`).
  - `comparator.RegisterBuiltinMatchers()` — registers all five by `Name()`.
  - `result.Compare` resolves `exp.Matcher` from `registry.Matcher`; unknown → `result: unknown matcher %q` (unchanged error text).

- [ ] **Step 1: Write the failing dispatch test**

Create `internal/comparator/matchers_test.go`:

```go
package comparator

import (
	"context"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// TestMain registers the built-in matchers once for the whole comparator test
// package, since result.Compare now resolves matchers from the registry.
func TestMain(m *testing.M) {
	RegisterBuiltinMatchers()
	os.Exit(m.Run())
}

// recordingMatcher proves result.Compare dispatches to a registered matcher
// rather than a hard-coded switch.
type recordingMatcher struct{ called *bool }

func (recordingMatcher) Name() string { return "recording" }
func (r recordingMatcher) Match(_ context.Context, _ core.Evidence, _, _ string) (core.Verdict, error) {
	*r.called = true
	return core.Verdict{Pass: true}, nil
}

func TestResultDispatchesToRegisteredMatcher(t *testing.T) {
	called := false
	registry.RegisterMatcher("recording", recordingMatcher{called: &called})

	v, err := NewResult().Compare(context.Background(), core.Evidence{}, ResultExpectation{Matcher: "recording"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("result.Compare did not dispatch to the registered matcher")
	}
	if !v.Pass {
		t.Fatalf("want Pass=true from recording matcher, got %+v", v)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/comparator/ -run TestResultDispatchesToRegisteredMatcher -v`
Expected: FAIL — `undefined: RegisterBuiltinMatchers`, and (once that compiles) the matcher would be unknown because `Compare` still switches.

- [ ] **Step 3: Create `matchers.go` with the five matcher types, helpers, and registration**

Create `internal/comparator/matchers.go`:

```go
package comparator

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// RegisterBuiltinMatchers registers the deterministic result matchers. Called
// at the composition root (engine.Build) and in test setup.
func RegisterBuiltinMatchers() {
	for _, m := range []core.Matcher{
		exactMatcher{}, containsMatcher{}, regexMatcher{},
		jsonSubsetMatcher{}, statusMatcher{},
	} {
		registry.RegisterMatcher(m.Name(), m)
	}
}

type exactMatcher struct{}

func (exactMatcher) Name() string { return "exact" }
func (exactMatcher) Match(_ context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	if got == want {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result exact: want %q, got %q", want, got),
	}}, nil
}

type containsMatcher struct{}

func (containsMatcher) Name() string { return "contains" }
func (containsMatcher) Match(_ context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	if strings.Contains(got, want) {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result contains: want %q, got %q", want, got),
	}}, nil
}

type regexMatcher struct{}

func (regexMatcher) Name() string { return "regex" }
func (regexMatcher) Match(_ context.Context, ev core.Evidence, want, target string) (core.Verdict, error) {
	got, err := targetString(target, ev)
	if err != nil {
		return core.Verdict{}, err
	}
	re, err := regexp.Compile(want)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: bad regex %q: %w", want, err)
	}
	if re.MatchString(got) {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result regex: want %q, got %q", want, got),
	}}, nil
}

type jsonSubsetMatcher struct{}

func (jsonSubsetMatcher) Name() string { return "json-subset" }
func (jsonSubsetMatcher) Match(_ context.Context, ev core.Evidence, want, _ string) (core.Verdict, error) {
	ok, err := jsonSubset([]byte(want), ev.Output.Body)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: json-subset: %w", err)
	}
	if ok {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result json-subset: want %q not a subset of got %q", want, ev.Output.Body),
	}}, nil
}

type statusMatcher struct{}

func (statusMatcher) Name() string { return "status" }
func (statusMatcher) Match(_ context.Context, ev core.Evidence, want, _ string) (core.Verdict, error) {
	w, err := strconv.Atoi(want)
	if err != nil {
		return core.Verdict{}, fmt.Errorf("result: status want must be int, got %q", want)
	}
	got := ev.Output.Status
	if got == w {
		return core.Verdict{Pass: true}, nil
	}
	return core.Verdict{Pass: false, Reasons: []string{
		fmt.Sprintf("result status: want %d, got %d", w, got),
	}}, nil
}

// targetString resolves which Output field a value matcher reads.
func targetString(target string, ev core.Evidence) (string, error) {
	switch target {
	case "", "answer":
		return ev.Output.Answer, nil
	case "status":
		return strconv.Itoa(ev.Output.Status), nil
	default:
		return "", fmt.Errorf("result: unsupported Target %q (want \"answer\" or \"status\")", target)
	}
}

// jsonSubset reports whether every key/value in want appears in got.
func jsonSubset(want, got []byte) (bool, error) {
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		return false, fmt.Errorf("want: %w", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		return false, fmt.Errorf("got: %w", err)
	}
	return subset(w, g), nil
}

func subset(w, g any) bool {
	switch wt := w.(type) {
	case map[string]any:
		gt, ok := g.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range wt {
			gv, ok := gt[k]
			if !ok || !subset(wv, gv) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(w, g)
	}
}
```

- [ ] **Step 4: Rewrite `result.go` as a dispatcher and remove the moved helpers**

Replace the body of `Compare` and delete the now-duplicated functions (`valueMatch`, `targetString`, `jsonSubsetMatch`, `statusMatch`, `jsonSubset`, `subset`) from `result.go`. The file becomes:

```go
package comparator

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// ResultExpectation configures the result comparator.
// Matcher selects the matching strategy: exact | contains | regex | json-subset | status.
// Want is the expected value (a string; for status, parsed as int).
// Target selects which Output field value matchers (exact/contains/regex) read:
//   - "" or "answer" → ev.Output.Answer (default)
//   - "status"       → strconv.Itoa(ev.Output.Status)
//   - any other      → error (no silent fallback)
//
// json-subset always reads ev.Output.Body; status always reads ev.Output.Status.
// Target is not consulted for those matchers.
type ResultExpectation struct {
	Matcher string // exact | contains | regex | json-subset | status
	Want    string
	Target  string // "answer" (default) or "status"
}

type result struct{}

// NewResult returns a Comparator that evaluates driver Output using registered
// deterministic matchers. It reads only ev.Output; it never touches ev.Trace.
func NewResult() core.Comparator { return result{} }
func (result) Name() string      { return "result" }

func (result) Compare(ctx context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(ResultExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: expectation must be ResultExpectation, got %T", e)
	}
	m, ok := registry.Matcher(exp.Matcher)
	if !ok {
		return core.Verdict{}, fmt.Errorf("result: unknown matcher %q", exp.Matcher)
	}
	return m.Match(ctx, ev, exp.Want, exp.Target)
}
```

- [ ] **Step 5: Run the comparator package tests (dispatch + existing safety net)**

Run: `go test ./internal/comparator/ -v`
Expected: PASS — `TestResultDispatchesToRegisteredMatcher` passes and every existing `result_test.go` case stays green (behavior preserved; reason strings unchanged).

- [ ] **Step 6: Register the built-ins at the composition root**

In `internal/engine/build.go`, after the comparator registrations (`build.go:22-25`), add:

```go
	comparator.RegisterBuiltinMatchers()
```

- [ ] **Step 7: Run engine + comparator tests and the full unit suite**

Run: `go test ./internal/...`
Expected: PASS. (The engine wires `result` and now also registers matchers; nothing else changes.)

- [ ] **Step 8: gofmt + vet + lint, then commit**

```bash
gofmt -w internal/comparator/matchers.go internal/comparator/matchers_test.go internal/comparator/result.go internal/engine/build.go
go vet ./internal/comparator/ ./internal/engine/
golangci-lint run ./internal/comparator/... ./internal/engine/...
git add internal/comparator/matchers.go internal/comparator/matchers_test.go internal/comparator/result.go internal/engine/build.go
git commit -m "refactor(comparator): dispatch result matchers via the matcher registry"
```

---

## Task 3: Store seam — factory registry, `store:` config field, `engine.BuildStore`

**Files:**
- Modify: `internal/registry/registry.go` (add the store factory registry)
- Modify: `internal/config/config.go` (add `Store` field + default)
- Create: `internal/engine/store.go`
- Create: `internal/engine/store_test.go`
- Test: `internal/registry/registry_test.go` (append), `internal/config/config_test.go` (append)

**Interfaces:**
- Consumes: `core.TraceStore` (existing), `config.Config` (existing), `store.NewTempo(endpoint string, hc *http.Client) *store.Tempo`.
- Produces:
  - `registry.StoreFactory` = `func(cfg config.Config) (core.TraceStore, error)`.
  - `registry.RegisterStore(name string, f registry.StoreFactory)` and `registry.Store(name string) (registry.StoreFactory, bool)`.
  - `config.Config.Store string` (yaml `store`), defaulting to `"tempo"`.
  - `engine.BuildStore(cfg config.Config) (core.TraceStore, error)` — registers the built-in `tempo` factory, then resolves `cfg.Store`; unknown name → `unknown store %q`.

- [ ] **Step 1: Write the failing store-registry test**

Append to `internal/registry/registry_test.go`:

```go
func TestStoreRegistryRoundTrip(t *testing.T) {
	want := store.NewInMemStore(nil)
	RegisterStore("inmem-test", func(config.Config) (core.TraceStore, error) { return want, nil })

	f, ok := Store("inmem-test")
	if !ok {
		t.Fatal("Store(\"inmem-test\") not found after RegisterStore")
	}
	got, err := f(config.Config{})
	if err != nil {
		t.Fatalf("factory error: %v", err)
	}
	if got != want {
		t.Fatalf("factory returned %p, want %p", got, want)
	}
}

func TestStoreRegistryMissReturnsFalse(t *testing.T) {
	if _, ok := Store("nope-not-registered"); ok {
		t.Fatal("Store(unregistered) returned ok=true, want false")
	}
}
```

Add `"github.com/thetonymaster/mentat/internal/config"` and `"github.com/thetonymaster/mentat/internal/store"` to the test imports if not present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/registry/ -run TestStoreRegistry -v`
Expected: FAIL — `undefined: RegisterStore` / `undefined: Store` / `undefined: StoreFactory`.

- [ ] **Step 3: Add the store factory registry**

In `internal/registry/registry.go`, add the `config` import and the factory registry:

```go
import (
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// StoreFactory builds a TraceStore from config. Stores are stateful (endpoints,
// clients), so the store seam registers factories rather than shared instances.
type StoreFactory func(cfg config.Config) (core.TraceStore, error)

var stores = map[string]StoreFactory{}

// RegisterStore registers a TraceStore factory under the given name.
func RegisterStore(name string, f StoreFactory) { stores[name] = f }

// Store resolves a registered TraceStore factory by name.
func Store(name string) (StoreFactory, bool) { f, ok := stores[name]; return f, ok }
```

Document the instance-vs-factory asymmetry with a one-line comment above the `matchers`/`comparators` instance maps (e.g. `// Stateless seams register instances; the stateful store seam registers factories (see StoreFactory).`).

- [ ] **Step 4: Run the store-registry test to verify it passes**

Run: `go test ./internal/registry/ -run TestStoreRegistry -v`
Expected: PASS.

- [ ] **Step 5: Write the failing config-default test**

Append to `internal/config/config_test.go`:

```go
func TestLoadDefaultsStoreToTempo(t *testing.T) {
	c, err := Load([]byte("tempo:\n  endpoint: http://localhost:3200\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Store != "tempo" {
		t.Fatalf("Store = %q, want %q", c.Store, "tempo")
	}
}

func TestLoadKeepsExplicitStore(t *testing.T) {
	c, err := Load([]byte("store: jaeger\ntempo:\n  endpoint: http://localhost:3200\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Store != "jaeger" {
		t.Fatalf("Store = %q, want %q", c.Store, "jaeger")
	}
}
```

- [ ] **Step 6: Run the config test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad...Store -v`
Expected: FAIL — `c.Store undefined`.

- [ ] **Step 7: Add the `Store` field and its default**

In `internal/config/config.go`, add the field to `Config` (`config.go:10-15`):

```go
	Store string `yaml:"store"`
```

In `Load`, after `yaml.Unmarshal` succeeds and before the targets loop (`config.go:46`):

```go
	if c.Store == "" {
		c.Store = "tempo"
	}
```

- [ ] **Step 8: Run the config test to verify it passes**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: PASS (new defaults tests and all existing `Load` tests).

- [ ] **Step 9: Write the failing `BuildStore` test**

Create `internal/engine/store_test.go`:

```go
package engine

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
)

func TestBuildStoreResolvesTempo(t *testing.T) {
	st, err := BuildStore(config.Config{Store: "tempo", Tempo: config.Endpoint{Endpoint: "http://localhost:3200"}})
	if err != nil {
		t.Fatalf("BuildStore: %v", err)
	}
	if st == nil {
		t.Fatal("BuildStore returned nil store for tempo")
	}
}

func TestBuildStoreUnknownErrors(t *testing.T) {
	_, err := BuildStore(config.Config{Store: "telepathy"})
	if err == nil {
		t.Fatal("want error for unknown store, got nil")
	}
}
```

- [ ] **Step 10: Run the test to verify it fails**

Run: `go test ./internal/engine/ -run TestBuildStore -v`
Expected: FAIL — `undefined: BuildStore`.

- [ ] **Step 11: Implement `engine.BuildStore`**

Create `internal/engine/store.go`:

```go
package engine

import (
	"fmt"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/store"
)

// BuildStore is the store composition root: it registers the built-in store
// factories, then resolves the store named by cfg.Store. Unknown names are a
// hard error (no silent fallback). Engine.Build keeps taking a built TraceStore
// so hermetic tests can inject store.NewInMemStore directly.
func BuildStore(cfg config.Config) (core.TraceStore, error) {
	registry.RegisterStore("tempo", func(c config.Config) (core.TraceStore, error) {
		return store.NewTempo(c.Tempo.Endpoint, nil), nil
	})
	f, ok := registry.Store(cfg.Store)
	if !ok {
		return nil, fmt.Errorf("unknown store %q", cfg.Store)
	}
	return f(cfg)
}
```

- [ ] **Step 12: Run the `BuildStore` test to verify it passes**

Run: `go test ./internal/engine/ -run TestBuildStore -v`
Expected: PASS (both cases).

- [ ] **Step 13: Run the full unit suite and check coverage of touched packages**

Run: `go test ./internal/... && go test ./internal/registry/ ./internal/config/ ./internal/engine/ -coverprofile=cover.out && go tool cover -func=cover.out | tail -n 1`
Expected: PASS; total ≥ 80% (registry/config/engine all carry tests for the new code).

- [ ] **Step 14: gofmt + vet + lint, then commit**

```bash
gofmt -w internal/registry/registry.go internal/registry/registry_test.go internal/config/config.go internal/config/config_test.go internal/engine/store.go internal/engine/store_test.go
go vet ./internal/registry/ ./internal/config/ ./internal/engine/
golangci-lint run ./internal/registry/... ./internal/config/... ./internal/engine/...
git add internal/registry/registry.go internal/registry/registry_test.go internal/config/config.go internal/config/config_test.go internal/engine/store.go internal/engine/store_test.go
git commit -m "feat(registry): factory-based store registry, store: config, engine.BuildStore"
```

---

## Task 4: Wire the entrypoints to the store registry

Mechanical swap of hard-coded `store.NewTempo` for `engine.BuildStore`. `cmd/` is coverage-exempt; verification is `go build`, `go vet`, and the unchanged default (`store: tempo`) preserving e2e behavior.

**Files:**
- Modify: `cmd/mentat/main.go:52` (and its `store` import, `main.go:15`)
- Modify: `cmd/mentatctl/main.go:128` (and its `store` import, `main.go:17`)

**Interfaces:**
- Consumes: `engine.BuildStore(cfg) (core.TraceStore, error)` (Task 3).

- [ ] **Step 1: Swap the store construction in `cmd/mentat/main.go`**

Replace `main.go:52`:

```go
	st := store.NewTempo(cfg.Tempo.Endpoint, nil)
```

with:

```go
	st, err := engine.BuildStore(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}
```

Then remove the now-unused `"github.com/thetonymaster/mentat/internal/store"` import (`main.go:15`). `engine` is already imported.

- [ ] **Step 2: Swap the store construction in `cmd/mentatctl/main.go`**

In `deps` (`main.go:128`), replace:

```go
	st := store.NewTempo(cfg.Tempo.Endpoint, (*http.Client)(nil))
```

with:

```go
	st, err := engine.BuildStore(cfg)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("mentatctl: build store: %w", err)
	}
```

Remove the now-unused `"github.com/thetonymaster/mentat/internal/store"` import (`main.go:17`). If `"net/http"` is no longer referenced elsewhere in the file, remove it too; otherwise leave it. (`engine` is already imported.)

- [ ] **Step 3: Build and vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: no errors; no "imported and not used" complaints.

- [ ] **Step 4: Run the full unit suite**

Run: `go test ./...`
Expected: PASS. (Non-e2e packages; the `//go:build e2e` suite is unaffected because default `store: tempo` resolves to the same Tempo client.)

- [ ] **Step 5: gofmt + lint, then commit**

```bash
gofmt -w cmd/mentat/main.go cmd/mentatctl/main.go
golangci-lint run ./cmd/...
git add cmd/mentat/main.go cmd/mentatctl/main.go
git commit -m "refactor(cmd): select the trace store via engine.BuildStore"
```

- [ ] **Step 6 (optional, requires harness): smoke the e2e happy path**

Run: `make harness-up && go test -tags e2e ./e2e/ -run TestHappyScenarioPasses -v ; make harness-down`
Expected: PASS — the default-store path behaves exactly as before the refactor.

---

## Self-Review

**Spec coverage (Spec B sections → tasks):**
- §3 matcher registry (interface, registry, dispatcher, built-ins at root, test impact) → Tasks 1 & 2. ✓
- §4 store registry (factory, `store:` config, entrypoint selection, tests bypass) → Tasks 3 & 4. ✓
- §5 two registration patterns (documented asymmetry) → Task 3 Step 3 (comment). ✓
- §6 deferred seams (correlator/reporter/judge) → out of scope by design; no task, intentionally. ✓
- §2 "leave comparator/driver registries untouched" → no task modifies them. ✓
- §3.4 schema/semantic ride the seam → covered by the header's sequencing note (implemented under Spec A, not here). ✓

**Placeholder scan:** No `TBD`/`TODO`/"add error handling"/"similar to Task N" — every code step shows complete code. ✓

**Type consistency:** `core.Matcher.Match(ctx, ev, want, target)` is identical in Task 1 (interface), Task 2 (impls + dispatcher call), and the fakes. `registry.StoreFactory = func(config.Config)(core.TraceStore,error)` is identical across Task 3 (def, test) and Task 4 (consumption via `engine.BuildStore`). `RegisterBuiltinMatchers` name matches in Task 2 (def, TestMain) and Task 2 Step 6 (engine.Build call). `config.Config.Store` matches across config (Task 3) and `BuildStore` (Task 3) and entrypoints (Task 4). ✓
