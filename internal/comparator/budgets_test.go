package comparator

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// tokenTrace builds a trace with a single invoke_agent span carrying the given
// input and output token counts.
func tokenTrace(in, out int) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "invoke_agent",
		Attrs: map[string]string{
			genai.Op:        genai.OpInvokeAgent,
			genai.InTokens:  strconv.Itoa(in),
			genai.OutTokens: strconv.Itoa(out),
		},
	}}}
}

// rawAttrTrace builds a trace with a single invoke_agent span carrying a single
// raw (possibly non-numeric) attribute under the given key. Used to exercise the
// present-but-malformed code paths.
func rawAttrTrace(key, raw string) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "invoke_agent",
		Attrs: map[string]string{
			genai.Op: genai.OpInvokeAgent,
			key:      raw,
		},
	}}}
}

// costTrace builds a trace whose single span carries a cost_usd attribute.
func costTrace(cost float64) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "invoke_agent",
		Attrs: map[string]string{
			genai.Op:      genai.OpInvokeAgent,
			genai.CostUSD: strconv.FormatFloat(cost, 'f', 6, 64),
		},
	}}}
}

// costAndRawTrace builds a trace with one span carrying a valid cost and a
// second span carrying a raw (non-numeric) cost value, to prove a malformed
// value is not silently masked by a valid one.
func costAndRawTrace(validCost float64, raw string) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{
		{
			Name: "invoke_agent",
			Attrs: map[string]string{
				genai.Op:      genai.OpInvokeAgent,
				genai.CostUSD: strconv.FormatFloat(validCost, 'f', 6, 64),
			},
		},
		{
			Name: "invoke_agent",
			Attrs: map[string]string{
				genai.Op:      genai.OpInvokeAgent,
				genai.CostUSD: raw,
			},
		},
	}}
}

// latencyTrace builds a trace whose spans have real Start/End so Envelope() is
// non-zero.
func latencyTrace(d time.Duration) *trace.Trace {
	now := time.Now()
	return &trace.Trace{Spans: []*trace.Span{{
		Name:  "invoke_agent",
		Start: now,
		End:   now.Add(d),
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent},
	}}}
}

// errorTrace builds a trace with the given number of spans whose Status is "Error".
func errorTrace(errCount int) *trace.Trace {
	tr := &trace.Trace{}
	for i := 0; i < errCount; i++ {
		tr.Spans = append(tr.Spans, &trace.Span{
			Name:   "invoke_agent",
			Status: "Error",
			Attrs:  map[string]string{genai.Op: genai.OpInvokeAgent},
		})
	}
	return tr
}

// floatPtr returns a pointer to the given float64 value.
func floatPtr(f float64) *float64 { return &f }

// durPtr returns a pointer to the given time.Duration value.
func durPtr(d time.Duration) *time.Duration { return &d }

func TestBudgetsPassesUnderTokenCap(t *testing.T) {
	ev := core.Evidence{Trace: tokenTrace(1200, 600)}
	v, err := NewBudgets().Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(5000)})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
}

func TestBudgetsFailsOverTokenCap(t *testing.T) {
	ev := core.Evidence{Trace: tokenTrace(9000, 4000)}
	v, err := NewBudgets().Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(5000)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Pass {
		t.Fatal("want fail over token cap")
	}
}

func TestBudgetsName(t *testing.T) {
	if got := NewBudgets().Name(); got != "budgets" {
		t.Fatalf("Name() = %q, want %q", got, "budgets")
	}
}

func TestBudgetsCompare(t *testing.T) {
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      core.Expectation
		wantPass bool
		wantErr  bool
	}{
		// --- MaxTokens ---
		{
			name:     "tokens: under budget passes",
			ev:       core.Evidence{Trace: tokenTrace(1200, 600)},
			exp:      BudgetExpectation{MaxTokens: IntPtr(5000)},
			wantPass: true,
		},
		{
			name:     "tokens: at budget passes (equal is not over)",
			ev:       core.Evidence{Trace: tokenTrace(3000, 2000)},
			exp:      BudgetExpectation{MaxTokens: IntPtr(5000)},
			wantPass: true,
		},
		{
			name:     "tokens: over budget fails",
			ev:       core.Evidence{Trace: tokenTrace(9000, 4000)},
			exp:      BudgetExpectation{MaxTokens: IntPtr(5000)},
			wantPass: false,
		},
		{
			name:    "tokens: malformed input_tokens returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.InTokens, "abc")},
			exp:     BudgetExpectation{MaxTokens: IntPtr(5000)},
			wantErr: true,
		},
		{
			name:    "tokens: malformed output_tokens returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.OutTokens, "1.5")},
			exp:     BudgetExpectation{MaxTokens: IntPtr(5000)},
			wantErr: true,
		},
		{
			// A malformed cost_usd alongside a valid one must error, not be
			// silently dropped (which would let `seen` mask the bad value and
			// undercount the total to a false pass).
			name:    "cost: malformed cost_usd alongside valid returns error",
			ev:      core.Evidence{Trace: costAndRawTrace(0.03, "free")},
			exp:     BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
			wantErr: true,
		},
		// --- MaxCostUSD ---
		{
			name:     "cost: under budget passes",
			ev:       core.Evidence{Trace: costTrace(0.03)},
			exp:      BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
			wantPass: true,
		},
		{
			name:     "cost: over budget fails",
			ev:       core.Evidence{Trace: costTrace(0.15)},
			exp:      BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
			wantPass: false,
		},
		{
			name: "cost: no cost attribute returns error",
			ev:   core.Evidence{Trace: tokenTrace(100, 50)}, // no CostUSD attr
			exp:  BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
			// MaxCostUSD set but no span has the attribute → error, not false pass
			wantErr: true,
		},
		// --- MaxLatency ---
		{
			name:     "latency: under budget passes",
			ev:       core.Evidence{Trace: latencyTrace(500 * time.Millisecond)},
			exp:      BudgetExpectation{MaxLatency: durPtr(2 * time.Second)},
			wantPass: true,
		},
		{
			name:     "latency: over budget fails",
			ev:       core.Evidence{Trace: latencyTrace(5 * time.Second)},
			exp:      BudgetExpectation{MaxLatency: durPtr(2 * time.Second)},
			wantPass: false,
		},
		// --- MaxErrors ---
		{
			name:     "errors: under budget passes",
			ev:       core.Evidence{Trace: errorTrace(1)},
			exp:      BudgetExpectation{MaxErrors: IntPtr(2)},
			wantPass: true,
		},
		{
			name:     "errors: over budget fails",
			ev:       core.Evidence{Trace: errorTrace(5)},
			exp:      BudgetExpectation{MaxErrors: IntPtr(2)},
			wantPass: false,
		},
		// --- Error paths ---
		{
			name:    "nil Trace returns error",
			ev:      core.Evidence{Trace: nil},
			exp:     BudgetExpectation{MaxTokens: IntPtr(1000)},
			wantErr: true,
		},
		{
			name:    "wrong expectation type returns error",
			ev:      core.Evidence{Trace: tokenTrace(100, 50)},
			exp:     "not a BudgetExpectation",
			wantErr: true,
		},
		{
			name:    "nil expectation type returns error",
			ev:      core.Evidence{Trace: tokenTrace(100, 50)},
			exp:     nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewBudgets().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v; reasons = %v", got.Pass, tt.wantPass, got.Reasons)
			}
			if tt.wantErr && got.Pass {
				t.Fatalf("wantErr=true but got Pass=true; err=%v", err)
			}
		})
	}
}
