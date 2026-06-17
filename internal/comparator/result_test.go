package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestResultName asserts the comparator self-identifies as "result".
func TestResultName(t *testing.T) {
	if got := NewResult().Name(); got != "result" {
		t.Fatalf("Name() = %q, want %q", got, "result")
	}
}

// TestResultWrongExpectationType asserts that passing a non-ResultExpectation
// returns a non-nil error and Pass==false (no silent fallback).
func TestResultWrongExpectationType(t *testing.T) {
	v, err := NewResult().Compare(context.Background(), core.Evidence{}, "not a ResultExpectation")
	if err == nil {
		t.Fatal("want error for non-ResultExpectation, got nil")
	}
	if v.Pass {
		t.Fatalf("want Pass=false on type error, got Pass=true")
	}
}

// TestResultJSONSubsetObjectVsArray asserts that a json-subset where Want is a
// JSON object and ev.Output.Body is a JSON array returns Pass=false, err=nil.
// This exercises the subset non-map-got branch in subset().
func TestResultJSONSubsetObjectVsArray(t *testing.T) {
	ev := core.Evidence{Output: core.Output{Body: []byte(`[1,2,3]`)}}
	exp := ResultExpectation{Matcher: "json-subset", Want: `{"a":1}`}
	v, err := NewResult().Compare(context.Background(), ev, exp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Pass {
		t.Fatal("want Pass=false when Want is object but Body is array")
	}
}

// TestResultContainsPassesAndFails is kept verbatim from the brief.
func TestResultContainsPassesAndFails(t *testing.T) {
	pass := core.Evidence{Output: core.Output{Answer: "Q3 revenue grew 12%"}}
	v, err := NewResult().Compare(context.Background(), pass, ResultExpectation{Matcher: "contains", Want: "Q3 revenue"})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
	fail := core.Evidence{Output: core.Output{Answer: "I could not find any information."}}
	v, err = NewResult().Compare(context.Background(), fail, ResultExpectation{Matcher: "contains", Want: "Q3 revenue"})
	if err != nil {
		t.Fatalf("unexpected error on contains-miss: %v", err)
	}
	if v.Pass {
		t.Fatal("want fail when substring absent")
	}
}

// TestResultStatusMatcher is kept verbatim from the brief.
func TestResultStatusMatcher(t *testing.T) {
	ev := core.Evidence{Output: core.Output{Status: 201}}
	v, err := NewResult().Compare(context.Background(), ev, ResultExpectation{Matcher: "status", Want: "201"})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
}

// TestResultUnknownMatcherErrors is kept verbatim from the brief.
func TestResultUnknownMatcherErrors(t *testing.T) {
	_, err := NewResult().Compare(context.Background(), core.Evidence{}, ResultExpectation{Matcher: "telepathy", Want: "x"})
	if err == nil {
		t.Fatal("want error for unknown matcher")
	}
}

// TestResultCompare is the comprehensive table-driven test covering every matcher
// and error branch. Coverage target: 100% of result.go.
func TestResultCompare(t *testing.T) {
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      ResultExpectation
		wantPass bool
		wantErr  bool
	}{
		// ── exact matcher ──────────────────────────────────────────────────────
		{
			name:     "exact: match passes",
			ev:       core.Evidence{Output: core.Output{Answer: "hello world"}},
			exp:      ResultExpectation{Matcher: "exact", Want: "hello world"},
			wantPass: true,
		},
		{
			name:     "exact: no match fails",
			ev:       core.Evidence{Output: core.Output{Answer: "hello world"}},
			exp:      ResultExpectation{Matcher: "exact", Want: "goodbye"},
			wantPass: false,
		},

		// ── contains matcher ───────────────────────────────────────────────────
		{
			name:     "contains: substring present passes",
			ev:       core.Evidence{Output: core.Output{Answer: "Q3 revenue grew 12%"}},
			exp:      ResultExpectation{Matcher: "contains", Want: "Q3 revenue"},
			wantPass: true,
		},
		{
			name:     "contains: substring absent fails",
			ev:       core.Evidence{Output: core.Output{Answer: "nothing useful"}},
			exp:      ResultExpectation{Matcher: "contains", Want: "Q3 revenue"},
			wantPass: false,
		},

		// ── regex matcher ──────────────────────────────────────────────────────
		{
			name:     "regex: match passes",
			ev:       core.Evidence{Output: core.Output{Answer: "order-123"}},
			exp:      ResultExpectation{Matcher: "regex", Want: `order-\d+`},
			wantPass: true,
		},
		{
			name:     "regex: no match fails",
			ev:       core.Evidence{Output: core.Output{Answer: "no digits here"}},
			exp:      ResultExpectation{Matcher: "regex", Want: `order-\d+`},
			wantPass: false,
		},
		{
			name:    "regex: invalid pattern is error",
			ev:      core.Evidence{Output: core.Output{Answer: "anything"}},
			exp:     ResultExpectation{Matcher: "regex", Want: `[invalid`},
			wantErr: true,
		},

		// ── json-subset matcher ────────────────────────────────────────────────
		{
			name:     "json-subset: subset passes",
			ev:       core.Evidence{Output: core.Output{Body: []byte(`{"a":1,"b":2}`)}},
			exp:      ResultExpectation{Matcher: "json-subset", Want: `{"a":1}`},
			wantPass: true,
		},
		{
			name:     "json-subset: non-subset fails",
			ev:       core.Evidence{Output: core.Output{Body: []byte(`{"a":1}`)}},
			exp:      ResultExpectation{Matcher: "json-subset", Want: `{"a":2}`},
			wantPass: false,
		},
		{
			name:    "json-subset: malformed Body is error",
			ev:      core.Evidence{Output: core.Output{Body: []byte(`not-json`)}},
			exp:     ResultExpectation{Matcher: "json-subset", Want: `{"a":1}`},
			wantErr: true,
		},
		{
			name:    "json-subset: malformed Want is error",
			ev:      core.Evidence{Output: core.Output{Body: []byte(`{"a":1}`)}},
			exp:     ResultExpectation{Matcher: "json-subset", Want: `not-json`},
			wantErr: true,
		},

		// ── status matcher ─────────────────────────────────────────────────────
		{
			name:     "status: matching int passes",
			ev:       core.Evidence{Output: core.Output{Status: 200}},
			exp:      ResultExpectation{Matcher: "status", Want: "200"},
			wantPass: true,
		},
		{
			name:     "status: mismatched int fails",
			ev:       core.Evidence{Output: core.Output{Status: 404}},
			exp:      ResultExpectation{Matcher: "status", Want: "200"},
			wantPass: false,
		},
		{
			name:    "status: non-int Want is error",
			ev:      core.Evidence{Output: core.Output{Status: 200}},
			exp:     ResultExpectation{Matcher: "status", Want: "ok"},
			wantErr: true,
		},

		// ── Target field routing ───────────────────────────────────────────────
		{
			name:     `Target "answer" routes to Answer (explicit)`,
			ev:       core.Evidence{Output: core.Output{Answer: "hello", Status: 999}},
			exp:      ResultExpectation{Matcher: "contains", Want: "hello", Target: "answer"},
			wantPass: true,
		},
		{
			name:     `Target "" defaults to Answer`,
			ev:       core.Evidence{Output: core.Output{Answer: "world", Status: 999}},
			exp:      ResultExpectation{Matcher: "contains", Want: "world", Target: ""},
			wantPass: true,
		},
		{
			name:     `Target "status" routes to Status string`,
			ev:       core.Evidence{Output: core.Output{Answer: "irrelevant", Status: 202}},
			exp:      ResultExpectation{Matcher: "contains", Want: "202", Target: "status"},
			wantPass: true,
		},
		{
			name:    `Target unsupported value is error`,
			ev:      core.Evidence{Output: core.Output{Answer: "x"}},
			exp:     ResultExpectation{Matcher: "contains", Want: "x", Target: "body"},
			wantErr: true,
		},

		// ── unknown matcher ────────────────────────────────────────────────────
		{
			name:    "unknown matcher is error",
			ev:      core.Evidence{},
			exp:     ResultExpectation{Matcher: "telepathy", Want: "x"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewResult().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return // error path: no verdict to check
			}
			if got.Pass != tt.wantPass {
				t.Errorf("Pass=%v wantPass=%v, reasons=%v", got.Pass, tt.wantPass, got.Reasons)
			}
			if !got.Pass && len(got.Reasons) == 0 {
				t.Error("failing verdict must carry at least one Reason")
			}
		})
	}
}
