package comparator

import (
	"context"
	"strconv"
	"strings"
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
	v, err := NewBudgets(nil).Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(5000)})
	if err != nil || !v.Pass {
		t.Fatalf("want pass, got %+v err=%v", v, err)
	}
}

func TestBudgetsFailsOverTokenCap(t *testing.T) {
	ev := core.Evidence{Trace: tokenTrace(9000, 4000)}
	v, err := NewBudgets(nil).Compare(context.Background(), ev, BudgetExpectation{MaxTokens: IntPtr(5000)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Pass {
		t.Fatal("want fail over token cap")
	}
}

func TestBudgetsName(t *testing.T) {
	if got := NewBudgets(nil).Name(); got != "budgets" {
		t.Fatalf("Name() = %q, want %q", got, "budgets")
	}
}

// A malformed cost_usd as the ONLY cost-bearing span must surface the parse
// error ("invalid"), not fall through to the "cost not available" guard: the
// ParseFloat runs before the seen-check, so the bad value is caught first.
// Pins the precise error the malformed value produces.
func TestBudgetsMalformedOnlyCostIsInvalidNotUnavailable(t *testing.T) {
	ev := core.Evidence{Trace: rawAttrTrace(genai.CostUSD, "free")}
	_, err := NewBudgets(nil).Compare(context.Background(), ev, BudgetExpectation{MaxCostUSD: floatPtr(0.10)})
	if err == nil {
		t.Fatal("want error for malformed-only cost_usd, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") || !strings.Contains(err.Error(), genai.CostUSD) {
		t.Fatalf("want an 'invalid %s' error, got %q", genai.CostUSD, err.Error())
	}
	if strings.Contains(err.Error(), "cost not available") {
		t.Fatalf("malformed-only cost wrongly reported as unavailable: %q", err.Error())
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
			// A negative input_tokens parses cleanly but is out of domain
			// (token counts are >= 0); left unchecked it reduces the total and
			// can mask an over-budget run as a false pass.
			name:    "tokens: negative input_tokens returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.InTokens, "-100000")},
			exp:     BudgetExpectation{MaxTokens: IntPtr(5000)},
			wantErr: true,
		},
		{
			name:    "tokens: negative output_tokens returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.OutTokens, "-100000")},
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
		{
			// A negative cost parses cleanly but is out of domain (cost >= 0);
			// unchecked it reduces the total toward a false pass.
			name:    "cost: negative cost_usd returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.CostUSD, "-0.5")},
			exp:     BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
			wantErr: true,
		},
		{
			// ParseFloat("NaN") succeeds with no error; NaN > threshold is
			// ALWAYS false in Go, so an unchecked NaN cost silently PASSES any
			// budget. This row pins that NaN now hard-errors instead.
			name:    "cost: NaN cost_usd returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.CostUSD, "NaN")},
			exp:     BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
			wantErr: true,
		},
		{
			name:    "cost: +Inf cost_usd returns error",
			ev:      core.Evidence{Trace: rawAttrTrace(genai.CostUSD, "+Inf")},
			exp:     BudgetExpectation{MaxCostUSD: floatPtr(0.10)},
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
			got, err := NewBudgets(nil).Compare(context.Background(), tt.ev, tt.exp)
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

// derivableTrace builds a trace whose single LLM span carries input/output tokens
// and a request model but NO emitted cost_usd, so cost must be derived.
func derivableTrace(in, out int, model string) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "chat",
		Attrs: map[string]string{
			genai.Op:           genai.OpChat,
			genai.InTokens:     strconv.Itoa(in),
			genai.OutTokens:    strconv.Itoa(out),
			genai.RequestModel: model,
		},
	}}}
}

// splitSpanTrace builds a trace in the OTel-GenAI split-span layout:
// one span carries input/output tokens but NO gen_ai.request.model (the
// aggregated invoke_agent), and one sibling span carries gen_ai.request.model
// but no tokens and no cost (a child chat span). Neither span carries cost_usd.
func splitSpanTrace(in, out int, model string) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{
		{
			Name: "invoke_agent",
			Attrs: map[string]string{
				genai.Op:        genai.OpInvokeAgent,
				genai.InTokens:  strconv.Itoa(in),
				genai.OutTokens: strconv.Itoa(out),
				// intentionally NO gen_ai.request.model
			},
		},
		{
			Name: "chat",
			Attrs: map[string]string{
				genai.Op:           genai.OpChat,
				genai.RequestModel: model,
				// intentionally NO tokens, NO cost_usd
			},
		},
	}}
}

// costAndTokenTrace builds a trace with a single span carrying BOTH a
// gen_ai.usage.cost_usd AND input/output tokens with a named model, to
// verify that emitted cost wins (cost is not added to derived).
func costAndTokenTrace(cost float64, in, out int, model string) *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name: "chat",
		Attrs: map[string]string{
			genai.Op:           genai.OpChat,
			genai.CostUSD:      strconv.FormatFloat(cost, 'f', 6, 64),
			genai.InTokens:     strconv.Itoa(in),
			genai.OutTokens:    strconv.Itoa(out),
			genai.RequestModel: model,
		},
	}}}
}

func TestCostSumDerivesFromTokens(t *testing.T) {
	// 1,000,000 in @ $10/MTok + 1,000,000 out @ $20/MTok = $30.00
	pricing := core.Pricing{"m": {InputPerMTok: 10, OutputPerMTok: 20}}

	tests := []struct {
		name     string
		tr       *trace.Trace
		pricing  core.Pricing
		wantCost float64
		wantErr  bool
		errSub   string
	}{
		{
			name:     "derives from tokens when model is priced",
			tr:       derivableTrace(1_000_000, 1_000_000, "m"),
			pricing:  pricing,
			wantCost: 30.0,
		},
		{
			name:    "model absent from configured table is a hard error",
			tr:      derivableTrace(1_000_000, 0, "unpriced"),
			pricing: pricing,
			wantErr: true,
			errSub:  "not in pricing table",
		},
		{
			name:    "token span with no cost and empty table is unavailable",
			tr:      derivableTrace(1_000_000, 0, "m"),
			pricing: nil,
			wantErr: true,
			errSub:  "cost not available",
		},
		{
			name:     "emitted cost wins over derivation",
			tr:       costTrace(0.05), // existing helper: carries cost_usd, no tokens
			pricing:  pricing,
			wantCost: 0.05,
		},
		// --- split-span layout (OTel-GenAI aggregated invoke_agent) ---
		{
			// Token span has no model; the trace has one distinct model on the
			// sibling span → fallback resolves it; cost is derived.
			name:     "split-span single model derives via trace fallback",
			tr:       splitSpanTrace(1_000_000, 1_000_000, "m"),
			pricing:  pricing,
			wantCost: 30.0,
		},
		{
			// Per-span model wins: token span has its own model m1; the trace
			// also contains model m2 on another span; cost derives from m1.
			name: "per-span model wins over trace fallback",
			tr: func() *trace.Trace {
				return &trace.Trace{Spans: []*trace.Span{
					{
						Name: "chat",
						Attrs: map[string]string{
							genai.Op:           genai.OpChat,
							genai.InTokens:     "1000000",
							genai.OutTokens:    "1000000",
							genai.RequestModel: "m", // per-span model → must be used
						},
					},
					{
						Name: "other",
						Attrs: map[string]string{
							genai.Op:           genai.OpChat,
							genai.RequestModel: "other-model", // trace also has a different model
						},
					},
				}}
			}(),
			pricing:  pricing, // only "m" is priced; "other-model" is not
			wantCost: 30.0,
		},
		{
			// emitted cost wins over derivation even when BOTH cost_usd AND
			// tokens+model are present on the same span (cost-wins; §4.3).
			name:     "emitted cost wins when span carries both cost and tokens",
			tr:       costAndTokenTrace(0.07, 1_000_000, 1_000_000, "m"),
			pricing:  pricing,
			wantCost: 0.07, // must be 0.07, NOT 0.07+30.0
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := costSum(tt.tr, tt.pricing)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q missing %q", err.Error(), tt.errSub)
				}
				return
			}
			if got != tt.wantCost {
				t.Fatalf("costSum = %v, want %v", got, tt.wantCost)
			}
		})
	}
}

func TestCostOrZero(t *testing.T) {
	tests := []struct {
		name    string
		trace   *trace.Trace
		pricing core.Pricing
		want    float64
		wantErr bool
	}{
		{"nil trace -> 0", nil, nil, 0, false},
		{"no cost, no pricing -> 0", tokenTrace(100, 50), nil, 0, false},
		{"emitted cost", costTrace(0.0030), nil, 0.0030, false},
		{"malformed cost -> err", rawAttrTrace(genai.CostUSD, "abc"), nil, 0, true},
		// Populated pricing + a token-bearing span whose model is absent from the
		// table → CostOrZero surfaces deriveCost's hard error (no silent 0). The
		// reporter must not paper over an unpriced model.
		{"populated pricing, unpriced model -> err", derivableTrace(1_000_000, 0, "unpriced"), core.Pricing{"m": {InputPerMTok: 10, OutputPerMTok: 20}}, 0, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := CostOrZero(tt.trace, tt.pricing)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCostSumSplitSpanAmbiguous verifies that when a token-bearing span has
// no per-span model but the trace contains two or more distinct models,
// costSum returns a hard error naming "multiple distinct models".
func TestCostSumSplitSpanAmbiguous(t *testing.T) {
	pricing := core.Pricing{
		"a": {InputPerMTok: 10, OutputPerMTok: 20},
		"b": {InputPerMTok: 5, OutputPerMTok: 10},
	}
	// Token span (no model) + two sibling spans with distinct models → ambiguous.
	tr := &trace.Trace{Spans: []*trace.Span{
		{
			Name: "invoke_agent",
			Attrs: map[string]string{
				genai.Op:        genai.OpInvokeAgent,
				genai.InTokens:  "1000000",
				genai.OutTokens: "1000000",
				// intentionally NO gen_ai.request.model
			},
		},
		{
			Name: "chat-a",
			Attrs: map[string]string{
				genai.Op:           genai.OpChat,
				genai.RequestModel: "a",
			},
		},
		{
			Name: "chat-b",
			Attrs: map[string]string{
				genai.Op:           genai.OpChat,
				genai.RequestModel: "b",
			},
		},
	}}

	_, err := costSum(tr, pricing)
	if err == nil {
		t.Fatal("want error for ambiguous trace models, got nil")
	}
	if !strings.Contains(err.Error(), "multiple distinct models") {
		t.Fatalf("want 'multiple distinct models' in error, got %q", err.Error())
	}
}
