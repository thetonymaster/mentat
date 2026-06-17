package comparator

import (
	"context"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/store"
)

// TestFixtureSequence covers the Plan 1 sequence done-criteria matrix:
//
//	happy       → passes  (search before summarize)
//	wrong_order → fails   (summarize before search)
//	extra_tool  → fails   (delete_record is forbidden)
func TestFixtureSequence(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		exp      SequenceExpectation
		wantPass bool
	}{
		{
			name:     "happy passes ordered subsequence",
			fixture:  "happy.json",
			exp:      SequenceExpectation{Order: []string{"search", "summarize"}},
			wantPass: true,
		},
		{
			name:     "wrong_order fails ordered subsequence",
			fixture:  "wrong_order.json",
			exp:      SequenceExpectation{Order: []string{"search", "summarize"}},
			wantPass: false,
		},
		{
			name:     "extra_tool fails forbidden check",
			fixture:  "extra_tool.json",
			exp:      SequenceExpectation{Forbidden: []string{"delete_record"}},
			wantPass: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile("../../testdata/traces/researchbot/" + tt.fixture)
			if err != nil {
				t.Fatalf("read fixture %q: %v", tt.fixture, err)
			}
			tr, err := store.LoadFixture(data)
			if err != nil {
				t.Fatalf("parse fixture %q: %v", tt.fixture, err)
			}

			ev := core.Evidence{Trace: tr}
			v, err := NewSequence().Compare(context.Background(), ev, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

// TestFixtureBudgets covers the Plan 1 budgets done-criteria matrix:
//
//	happy       → passes  (1200+600=1800 ≤ 5000)
//	over_budget → fails   (9000+4000=13000 > 5000)
func TestFixtureBudgets(t *testing.T) {
	tests := []struct {
		name      string
		fixture   string
		maxTokens int
		wantPass  bool
	}{
		{
			name:      "happy passes under token cap",
			fixture:   "happy.json",
			maxTokens: 5000,
			wantPass:  true,
		},
		{
			name:      "over_budget fails over token cap",
			fixture:   "over_budget.json",
			maxTokens: 5000,
			wantPass:  false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile("../../testdata/traces/researchbot/" + tt.fixture)
			if err != nil {
				t.Fatalf("read fixture %q: %v", tt.fixture, err)
			}
			tr, err := store.LoadFixture(data)
			if err != nil {
				t.Fatalf("parse fixture %q: %v", tt.fixture, err)
			}

			ev := core.Evidence{Trace: tr}
			v, err := NewBudgets().Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(tt.maxTokens)})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

// TestFixtureResult covers the Plan 1 result done-criteria matrix.
//
// The result comparator reads ev.Output.Answer, NOT the trace — the trace fixtures
// do not carry the SUT's answer text. The answer strings below are the Plan 1
// scenarios' defined `output` values (what the SUT writes to stdout).
func TestFixtureResult(t *testing.T) {
	tests := []struct {
		name     string
		answer   string // Plan 1 scenario defined output (not loaded from trace JSON)
		exp      ResultExpectation
		wantPass bool
	}{
		{
			// bad_answer scenario output: agent returns a failure message
			name:     "bad_answer fails contains revenue",
			answer:   "I could not find any information.",
			exp:      ResultExpectation{Matcher: "contains", Want: "revenue"},
			wantPass: false,
		},
		{
			// happy scenario output: agent returns the correct revenue summary
			name:     "happy passes contains revenue",
			answer:   "Q3 revenue grew 12% to $4.2M, driven by strong enterprise demand.",
			exp:      ResultExpectation{Matcher: "contains", Want: "revenue"},
			wantPass: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Answer: tt.answer}}
			v, err := NewResult().Compare(context.Background(), ev, tt.exp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}
