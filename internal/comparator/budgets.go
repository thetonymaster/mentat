package comparator

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// BudgetExpectation holds optional upper-bound budgets for tokens, cost, latency
// and error-span count. Nil fields are skipped.
type BudgetExpectation struct {
	MaxTokens  *int
	MaxCostUSD *float64
	MaxLatency *time.Duration
	MaxErrors  *int
}

// IntPtr is a convenience helper: returns a pointer to i.
func IntPtr(i int) *int { return &i }

type budgets struct{ pricing core.Pricing }

// NewBudgets returns a Comparator that enforces BudgetExpectation thresholds.
// pricing derives cost from tokens when a span carries no emitted cost (§4.3);
// a nil/empty table preserves the emitted-cost-only behaviour.
func NewBudgets(pricing core.Pricing) core.Comparator { return budgets{pricing: pricing} }
func (budgets) Name() string                          { return "budgets" }

func (b budgets) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
	exp, ok := e.(BudgetExpectation)
	if !ok {
		return core.Verdict{}, fmt.Errorf("budgets: expectation must be BudgetExpectation, got %T", e)
	}
	if ev.Trace == nil {
		return core.Verdict{}, fmt.Errorf("budgets: Evidence.Trace is nil")
	}

	v := core.Verdict{Pass: true}

	if exp.MaxTokens != nil {
		total, err := tokenSum(ev.Trace)
		if err != nil {
			return core.Verdict{}, err
		}
		if total > *exp.MaxTokens {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total tokens %d exceed budget %d", total, *exp.MaxTokens))
		}
	}

	if exp.MaxCostUSD != nil {
		cost, err := costSum(ev.Trace, b.pricing)
		if err != nil {
			return core.Verdict{}, err
		}
		if cost > *exp.MaxCostUSD {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total cost $%.4f exceeds budget $%.4f", cost, *exp.MaxCostUSD))
		}
	}

	if exp.MaxLatency != nil {
		if env := ev.Trace.Envelope(); env > *exp.MaxLatency {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("run latency %v exceeds budget %v", env, *exp.MaxLatency))
		}
	}

	if exp.MaxErrors != nil {
		if errs := errorCount(ev.Trace); errs > *exp.MaxErrors {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%d error spans exceed budget %d", errs, *exp.MaxErrors))
		}
	}

	return v, nil
}

// tokenSum returns the total gen_ai input+output tokens across all spans. A
// non-integer or negative token attribute is a hard error (no silent fallback).
// Shared by the budgets and cel comparators so they never disagree (§5).
func tokenSum(t *trace.Trace) (int, error) {
	total := 0
	for i, s := range t.Spans {
		for _, key := range []string{genai.InTokens, genai.OutTokens} {
			raw := s.Attr(key)
			if raw == "" {
				continue
			}
			n, err := strconv.Atoi(raw)
			if err != nil {
				return 0, fmt.Errorf("budgets: span[%d] (%q) invalid %s=%q: %w", i, s.Name, key, raw, err)
			}
			if n < 0 {
				return 0, fmt.Errorf("budgets: span[%d] (%q) %s=%q out of range: must be a value >= 0", i, s.Name, key, raw)
			}
			total += n
		}
	}
	return total, nil
}

// costSum returns the total gen_ai cost in USD across all spans, applying the
// per-span precedence in spec §4.3: an emitted gen_ai.usage.cost_usd always wins;
// otherwise a token-bearing span (an LLM call) derives cost from its tokens and
// the per-model pricing table; spans with neither cost nor tokens contribute 0.
// Absent cost across all spans is a hard error (the cel comparator inherits this
// via the shared function — §5). A malformed/out-of-range value is a hard error.
// When the pricing table is empty, derivation is skipped and the legacy
// emitted-cost-only behaviour applies verbatim.
func costSum(t *trace.Trace, pricing core.Pricing) (float64, error) {
	cost := 0.0
	seen := false
	for i, s := range t.Spans {
		raw := s.Attr(genai.CostUSD)
		if raw != "" {
			c, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return 0, fmt.Errorf("budgets: span[%d] (%q) invalid %s=%q: %w", i, s.Name, genai.CostUSD, raw, err)
			}
			if c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
				return 0, fmt.Errorf("budgets: span[%d] (%q) %s=%q out of range: must be a finite value >= 0", i, s.Name, genai.CostUSD, raw)
			}
			cost += c
			seen = true
			continue
		}
		// No emitted cost. With no pricing table, only emitted cost_usd counts
		// (§4.3 final paragraph) — preserve the legacy behaviour exactly.
		if len(pricing) == 0 {
			continue
		}
		in, inOK, err := tokenAttr(s, genai.InTokens)
		if err != nil {
			return 0, fmt.Errorf("budgets: span[%d] (%q) %w", i, s.Name, err)
		}
		out, outOK, err := tokenAttr(s, genai.OutTokens)
		if err != nil {
			return 0, fmt.Errorf("budgets: span[%d] (%q) %w", i, s.Name, err)
		}
		if !inOK && !outOK {
			continue // not an LLM call (e.g. a tool/service span) — contributes 0
		}
		model := s.Attr(genai.RequestModel)
		rate, ok := pricing[model]
		if model == "" || !ok {
			return 0, fmt.Errorf("budgets: span[%d] (%q): cannot derive cost: model %q not in pricing table", i, s.Name, model)
		}
		cost += float64(in)/1e6*rate.InputPerMTok + float64(out)/1e6*rate.OutputPerMTok
		seen = true
	}
	if !seen {
		return 0, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
	}
	return cost, nil
}

// tokenAttr parses a non-negative integer token attribute. ok is false when the
// attribute is absent. A malformed or negative value is an error (mirrors
// tokenSum's domain check).
func tokenAttr(s *trace.Span, key string) (int, bool, error) {
	raw := s.Attr(key)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, fmt.Errorf("invalid %s=%q: %w", key, raw, err)
	}
	if n < 0 {
		return 0, false, fmt.Errorf("%s=%q out of range: must be a value >= 0", key, raw)
	}
	return n, true, nil
}

// errorCount returns the number of spans whose Status is "Error".
func errorCount(t *trace.Trace) int {
	errs := 0
	for _, s := range t.Spans {
		if s.Status == "Error" {
			errs++
		}
	}
	return errs
}
