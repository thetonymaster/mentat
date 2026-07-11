package steps

import (
	"os"
	"strings"
	"testing"
)

// TestNoBackgroundContextInSteps guards the audit-B2 fix (feature 003, FR-004): no
// non-test file in this package may call context.Background(). Scenario-scoped work
// (drive, resolve, compare, aggregate, judge) must run under the scenario context
// captured in world.ctx, so one budget/cancellation bounds it all. A stray
// background context would silently revive the discarded-scenario-context bug where
// SUT runs, trace polling, and judge calls all ran unbounded.
func TestNoBackgroundContextInSteps(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	const banned = "context.Background("
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		if strings.Contains(string(data), banned) {
			t.Errorf("%s calls %s); scenario-scoped work must use world.ctx (FR-004)", name, banned)
		}
	}
	if scanned == 0 {
		t.Fatal("guard scanned no non-test source files; the check is not actually running")
	}
}
