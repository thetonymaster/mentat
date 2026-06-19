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
