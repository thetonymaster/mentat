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
	drivers = map[string]core.Driver{}
	matchers = map[string]core.Matcher{}
	stores = map[string]StoreFactory{}
	t.Cleanup(func() {
		comparators = map[string]core.Comparator{}
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

func TestMatcherRegistryRoundTrip(t *testing.T) {
	resetRegistries(t)
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
	resetRegistries(t)
	if _, ok := Matcher("nope-not-registered"); ok {
		t.Fatal("Matcher(unregistered) returned ok=true, want false")
	}
}

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
