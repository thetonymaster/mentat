package registry

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/store"
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

// resetRegistries wipes all global maps before a test and restores them after,
// so every test function starts from a clean, isolated registry regardless of
// execution order.
func resetRegistries(t *testing.T) {
	t.Helper()
	comparators = map[string]core.Comparator{}
	aggregateComparators = map[string]core.AggregateComparator{}
	drivers = map[string]core.Driver{}
	matchers = map[string]core.Matcher{}
	stores = map[string]StoreFactory{}
	t.Cleanup(func() {
		comparators = map[string]core.Comparator{}
		aggregateComparators = map[string]core.AggregateComparator{}
		drivers = map[string]core.Driver{}
		matchers = map[string]core.Matcher{}
		stores = map[string]StoreFactory{}
	})
}

func TestRegisterAndResolveComparator(t *testing.T) {
	resetRegistries(t)
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			RegisterComparator(tt.regName, fakeCmp{})
			c, ok := Comparator(tt.lookup)
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
	resetRegistries(t)
	RegisterComparator("listed", fakeCmp{})
	names := Comparators()
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

func TestRegisterAndResolveDriver(t *testing.T) {
	resetRegistries(t)
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			resetRegistries(t)
			RegisterDriver(tt.scheme, registered)
			d, ok := Driver(tt.lookup)
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
	resetRegistries(t)
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			RegisterMatcher(tt.regName, fakeMatcher{name: tt.regName})
			got, ok := Matcher(tt.lookup)
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
	resetRegistries(t)
	RegisterAggregateComparator("fake-agg", fakeAggCmp{})
	tests := []struct {
		name   string
		lookup string
		wantOK bool
	}{
		{name: "registered name hits", lookup: "fake-agg", wantOK: true},
		{name: "unknown name misses", lookup: "missing-agg", wantOK: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := AggregateComparator(tt.lookup); ok != tt.wantOK {
				t.Fatalf("AggregateComparator(%q) ok=%v, want %v", tt.lookup, ok, tt.wantOK)
			}
		})
	}
}

func TestStoreRegistry(t *testing.T) {
	resetRegistries(t)
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			RegisterStore(tt.regName, func(config.Config) (core.TraceStore, error) { return want, nil })
			f, ok := Store(tt.lookup)
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
