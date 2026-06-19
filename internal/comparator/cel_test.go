package comparator

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/store"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestCELName(t *testing.T) {
	if got := NewCEL(nil).Name(); got != "cel" {
		t.Fatalf("Name() = %q, want %q", got, "cel")
	}
}

func TestCELWrongExpectationType(t *testing.T) {
	_, err := NewCEL(nil).Compare(context.Background(), core.Evidence{}, "not a CELExpectation")
	if err == nil {
		t.Fatal("want error for wrong expectation type, got nil")
	}
}

func TestCELOutputVars(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		out       core.Output
		wantPass  bool
		reasonSub string // substring required in the failure reason (when !wantPass)
	}{
		{name: "status pass", expr: `status == 201`, out: core.Output{Status: 201}, wantPass: true},
		{name: "status fail shows value", expr: `status == 200`, out: core.Output{Status: 201}, wantPass: false, reasonSub: "status=201"},
		{name: "exitCode pass", expr: `exitCode == 0`, out: core.Output{ExitCode: 0}, wantPass: true},
		{name: "answer contains macro", expr: `answer.contains("revenue")`, out: core.Output{Answer: "Q3 revenue up"}, wantPass: true},
		{name: "bodyText startsWith", expr: `bodyText.startsWith("{")`, out: core.Output{Body: []byte(`{"a":1}`)}, wantPass: true},
		{name: "compound fail shows offending value", expr: `status == 201 && exitCode == 0`, out: core.Output{Status: 500, ExitCode: 0}, wantPass: false, reasonSub: "status=500"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: tt.out}
			v, err := NewCEL(nil).Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Compare(%q): %v", tt.expr, err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if tt.reasonSub != "" {
				if len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], tt.reasonSub) {
					t.Fatalf("want reason containing %q, got %v", tt.reasonSub, v.Reasons)
				}
			}
		})
	}
}

func TestCELTraceVars(t *testing.T) {
	tests := []struct {
		name      string
		dir       string
		fixture   string
		expr      string
		wantPass  bool
		reasonSub string
	}{
		{name: "tokens under cap (researchbot happy 1800)", dir: "researchbot", fixture: "happy.json", expr: `tokens < 5000`, wantPass: true},
		{name: "tokens over cap (over_budget) shows value", dir: "researchbot", fixture: "over_budget.json", expr: `tokens < 5000`, wantPass: false, reasonSub: "tokens="},
		{name: "cost under cap (researchbot happy 0.018)", dir: "researchbot", fixture: "happy.json", expr: `cost < 1.0`, wantPass: true},
		{name: "tools present via macro", dir: "researchbot", fixture: "happy.json", expr: `"search" in tools && "summarize" in tools`, wantPass: true},
		{name: "errors zero (researchbot happy)", dir: "researchbot", fixture: "happy.json", expr: `errors == 0`, wantPass: true},
		{name: "services exclude legacy (orderflow happy)", dir: "orderflow", fixture: "happy.json", expr: `!("legacy-pricing" in services)`, wantPass: true},
		{name: "services include legacy (orderflow legacy_path) red", dir: "orderflow", fixture: "legacy_path.json", expr: `!("legacy-pricing" in services)`, wantPass: false, reasonSub: "services="},
		{name: "errors present (orderflow payment_decline)", dir: "orderflow", fixture: "payment_decline.json", expr: `errors > 0`, wantPass: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile("../../testdata/traces/" + tt.dir + "/" + tt.fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			tr, err := store.LoadFixture(data)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			ev := core.Evidence{Trace: tr}
			v, err := NewCEL(nil).Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Compare(%q): %v", tt.expr, err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if tt.reasonSub != "" && (len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], tt.reasonSub)) {
				t.Fatalf("want reason containing %q, got %v", tt.reasonSub, v.Reasons)
			}
		})
	}
}

// TestCELCostAbsentHardError pins decision 1: referencing cost on a trace with
// no cost_usd is a hard error (reuses budgets' costSum), never a 0.0 fallback.
func TestCELCostAbsentHardError(t *testing.T) {
	tr := &trace.Trace{Spans: []*trace.Span{{
		Name:  "invoke_agent",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "100"},
	}}}
	_, err := NewCEL(nil).Compare(context.Background(), core.Evidence{Trace: tr}, CELExpectation{Expr: `cost < 1.0`})
	if err == nil {
		t.Fatal("want hard error for cost-absent trace, got nil")
	}
	if !strings.Contains(err.Error(), "cost not available") {
		t.Fatalf("want 'cost not available' error, got %v", err)
	}
}

// TestCELLatencyCraftedTrace: goldens carry no timestamps (latencyMs==0), so
// latency is exercised with a hand-built trace that has real Start/End.
func TestCELLatencyCraftedTrace(t *testing.T) {
	now := time.Now()
	tr := &trace.Trace{Spans: []*trace.Span{{
		Name:  "invoke_agent",
		Start: now,
		End:   now.Add(50 * time.Millisecond),
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent},
	}}}
	v, err := NewCEL(nil).Compare(context.Background(), core.Evidence{Trace: tr}, CELExpectation{Expr: `latencyMs >= 50`})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !v.Pass {
		t.Fatalf("want pass for latencyMs>=50; reasons=%v", v.Reasons)
	}
}

// TestCELNilTraceReferencedError: a referenced trace var with a nil Trace is a
// descriptive error, not a panic (no silent fallback).
func TestCELNilTraceReferencedError(t *testing.T) {
	_, err := NewCEL(nil).Compare(context.Background(), core.Evidence{}, CELExpectation{Expr: `tokens < 5000`})
	if err == nil {
		t.Fatal("want error when a trace var is referenced but Trace is nil, got nil")
	}
	if !strings.Contains(err.Error(), "evidence has no trace") {
		t.Fatalf("want error containing %q, got %v", "evidence has no trace", err)
	}
}

func TestCELBodyHandling(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		body     []byte
		wantPass bool
		wantErr  bool
		errSub   string
	}{
		{name: "valid json field match", expr: `body.status == "confirmed"`, body: []byte(`{"status":"confirmed"}`), wantPass: true},
		{name: "valid json field mismatch", expr: `body.status == "confirmed"`, body: []byte(`{"status":"declined"}`), wantPass: false},
		{name: "empty body binds null", expr: `body == null`, body: []byte(``), wantPass: true},
		{name: "invalid json referenced is hard error", expr: `body.x == 1`, body: []byte(`not json`), wantErr: true, errSub: "not valid JSON"},
		{name: "invalid json unreferenced is no error", expr: `status == 201`, body: []byte(`not json`), wantPass: true},
		{name: "numeric body field as double", expr: `body.count == 3.0`, body: []byte(`{"count":3}`), wantPass: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Status: 201, Body: tt.body}}
			v, err := NewCEL(nil).Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.errSub != "" && (err == nil || !strings.Contains(err.Error(), tt.errSub)) {
				t.Fatalf("want error containing %q, got %v", tt.errSub, err)
			}
			if !tt.wantErr && v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}
