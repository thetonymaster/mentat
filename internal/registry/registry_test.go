package registry

import (
	"context"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/result"
	"github.com/thetonymaster/mentat/internal/store"
	"go.uber.org/mock/gomock"
)

type fakeCmp struct{}

func (fakeCmp) Name() string { return "fake" }
func (fakeCmp) Compare(context.Context, core.Evidence, core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

type fakeDriver struct{}

func (fakeDriver) Run(_ context.Context, _ core.RunSpec) (core.RunResult, error) {
	return core.RunResult{}, nil
}

func TestRegisterAndResolveComparator(t *testing.T) {
	tests := []struct {
		name    string
		regName string
		lookup  string
		wantOK  bool
	}{
		{name: "found", regName: "fake", lookup: "fake", wantOK: true},
		{name: "missing", regName: "fake", lookup: "missing", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := New()
			reg.RegisterComparator(tt.regName, fakeCmp{})
			c, ok := reg.Comparator(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("Comparator(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
			if ok && c.Name() != tt.regName {
				t.Fatalf("Comparator(%q).Name()=%q, want %q", tt.lookup, c.Name(), tt.regName)
			}
		})
	}
}

func TestComparators(t *testing.T) {
	reg := New()
	reg.RegisterComparator("listed", fakeCmp{})
	names := reg.Comparators()
	found := false
	for _, n := range names {
		if n == "listed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Comparators() = %v, want to contain %q", names, "listed")
	}
}

// TestDrivers proves Drivers() returns the registered driver schemes in sorted
// order (feature 005, D3): engine.Build uses this list to name the registered set
// when it rejects a target whose adapter has no driver. Registration order is
// deliberately unsorted to prove the accessor sorts rather than echoing map order.
func TestDrivers(t *testing.T) {
	reg := New()
	reg.RegisterDriver("shell", fakeDriver{})
	reg.RegisterDriver("http", fakeDriver{})
	reg.RegisterDriver("aaa", fakeDriver{})
	got := reg.Drivers()
	if !sort.StringsAreSorted(got) {
		t.Fatalf("Drivers() = %v, want sorted", got)
	}
	want := []string{"aaa", "http", "shell"}
	if len(got) != len(want) {
		t.Fatalf("Drivers() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drivers() = %v, want %v", got, want)
		}
	}
}

func TestRegisterAndResolveDriver(t *testing.T) {
	registered := fakeDriver{}
	tests := []struct {
		name   string
		scheme string
		lookup string
		wantOK bool
	}{
		{name: "found", scheme: "shell", lookup: "shell", wantOK: true},
		{name: "absent", scheme: "shell", lookup: "http", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := New()
			reg.RegisterDriver(tt.scheme, registered)
			d, ok := reg.Driver(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("Driver(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
			if ok {
				if d == nil {
					t.Fatalf("Driver(%q) returned nil driver, want non-nil", tt.lookup)
				}
				if _, isExpected := d.(fakeDriver); !isExpected {
					t.Fatalf("Driver(%q) returned wrong type %T, want fakeDriver", tt.lookup, d)
				}
			}
		})
	}
}

// fakeMatcher is a minimal core.Matcher for registry round-trip tests.
type fakeMatcher struct{ name string }

func (f fakeMatcher) Name() string { return f.name }
func (f fakeMatcher) Match(_ context.Context, _ core.Evidence, _, _ string) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

func TestMatcherRegistry(t *testing.T) {
	tests := []struct {
		name    string
		regName string
		lookup  string
		wantOK  bool
	}{
		{name: "round-trip", regName: "fake", lookup: "fake", wantOK: true},
		{name: "miss", regName: "fake", lookup: "nope-not-registered", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := New()
			reg.RegisterMatcher(tt.regName, fakeMatcher{name: tt.regName})
			got, ok := reg.Matcher(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("Matcher(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
			if ok && got.Name() != tt.regName {
				t.Fatalf("Matcher(%q).Name()=%q, want %q", tt.lookup, got.Name(), tt.regName)
			}
		})
	}
}

type fakeAggCmp struct{}

func (fakeAggCmp) Name() string { return "fake-agg" }
func (fakeAggCmp) Aggregate(_ context.Context, _ []core.Evidence, _ core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

func TestAggregateComparatorRegistry(t *testing.T) {
	reg := New()
	reg.RegisterAggregateComparator("fake-agg", fakeAggCmp{})
	tests := []struct {
		name   string
		lookup string
		wantOK bool
	}{
		{name: "registered name hits", lookup: "fake-agg", wantOK: true},
		{name: "unknown name misses", lookup: "missing-agg", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := reg.AggregateComparator(tt.lookup); ok != tt.wantOK {
				t.Fatalf("AggregateComparator(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
		})
	}
}

func TestStoreRegistry(t *testing.T) {
	want := store.NewInMemStore(nil)
	tests := []struct {
		name    string
		regName string
		lookup  string
		wantOK  bool
	}{
		{name: "round-trip", regName: "inmem-test", lookup: "inmem-test", wantOK: true},
		{name: "miss", regName: "inmem-test", lookup: "nope-not-registered", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := New()
			reg.RegisterStore(tt.regName, func(config.Config) (core.TraceStore, error) { return want, nil })
			f, ok := reg.Store(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("Store(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			got, err := f(config.Config{})
			if err != nil {
				t.Fatalf("factory error: %v", err)
			}
			if got != want {
				t.Fatalf("factory returned %p, want %p", got, want)
			}
		})
	}
}

func TestJudgeRegistry(t *testing.T) {
	want := mocks.NewMockJudge(gomock.NewController(t))
	tests := []struct {
		name    string
		regName string
		lookup  string
		wantOK  bool
	}{
		{name: "round-trip", regName: "claude-test", lookup: "claude-test", wantOK: true},
		{name: "miss", regName: "claude-test", lookup: "does-not-exist", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := New()
			var registered JudgeFactory = func(config.Config) (core.Judge, error) { return want, nil }
			reg.RegisterJudge(tt.regName, registered)
			f, ok := reg.Judge(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("Judge(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			got, err := f(config.Config{})
			if err != nil {
				t.Fatalf("factory error: %v", err)
			}
			if got != want {
				t.Fatalf("factory returned %p, want %p", got, want)
			}
		})
	}
}

// stubReporter is a trivial value stub, not a gomock mock. This test registers a value
// and looks it up by name; it never asserts a call count or an argument, so per the
// constitution's mocks rule ("trivial value stubs are acceptable only when no call-count
// or argument verification is needed") a stub is the right tool. It also keeps the
// Reporter seam mock-free after feature 010 moved the interface to internal/result — the
// only `go:generate mockgen` directive is sourced from core.go, so a generated
// MockReporter would need a second mocks package for a single call-free registration.
type stubReporter struct{}

func (stubReporter) Report(_ result.Results, _ io.Writer) error { return nil }

// TestReporterRegistry exercises the reporter seam, which became per-engine in feature
// 010 (D4) — it was a package-global map until a per-run WithReporter needed it not to be.
func TestReporterRegistry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		regName string
		lookup  string
		wantOK  bool
	}{
		{name: "found", regName: "fake", lookup: "fake", wantOK: true},
		{name: "not-found", regName: "fake", lookup: "nope", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := New()
			reg.RegisterReporter(tt.regName, stubReporter{})
			_, ok := reg.Reporter(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("Reporter(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
		})
	}
}

// TestReporterRegistryIsPerEngine is the property that made D4 necessary. Two registries
// registering DIFFERENT reporters under the SAME name must not see each other — the
// package-global map this replaced could not offer that, so a per-run WithReporter would
// have raced two concurrent Runs into each other's reporters.
func TestReporterRegistryIsPerEngine(t *testing.T) {
	t.Parallel()

	a, b := New(), New()
	a.RegisterReporter("shared-name", stubReporter{})

	if _, ok := b.Reporter("shared-name"); ok {
		t.Fatal("a registration in one registry is visible in another — reporter state is shared")
	}
	if _, ok := a.Reporter("shared-name"); !ok {
		t.Fatal("registry lost its own registration")
	}
}

// TestReporterRegistrySeals proves reporters obey the same build-once discipline as every
// other seam. Before 010 they were explicitly exempt ("never sealed"), which is exactly
// the hole a post-seal registration would have slipped through.
func TestReporterRegistrySeals(t *testing.T) {
	t.Parallel()

	reg := New()
	reg.Seal()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("RegisterReporter after Seal did not panic; reporters must obey the seal")
		}
	}()
	reg.RegisterReporter("too-late", stubReporter{})
}

// --- Feature 003 (US4): registry sealing -------------------------------------

func TestSealRejectsPostBuildRegistration(t *testing.T) {
	reg := New()
	reg.RegisterComparator("pre-seal", fakeCmp{}) // allowed before the seal
	reg.Seal()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic when registering after Seal")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "sealed at the composition root") {
			t.Fatalf("panic %q lacks the sealed-registry explanation", msg)
		}
	}()
	reg.RegisterComparator("post-seal", fakeCmp{}) // must panic
	t.Fatal("RegisterComparator after Seal did not panic")
}

// TestRegistryConcurrentAccessRaceClean proves concurrent readers and a writer
// (pre-seal) are race-free under -race — the RWMutex removes the audit-B5 data-race
// class outright.
func TestRegistryConcurrentAccessRaceClean(t *testing.T) {
	reg := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = reg.Comparator("x") }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); reg.RegisterComparator("y", fakeCmp{}) }()
	wg.Wait()
}
