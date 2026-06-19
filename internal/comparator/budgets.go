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

type budgets struct{}

// NewBudgets returns a Comparator that enforces BudgetExpectation thresholds.
func NewBudgets() core.Comparator { return budgets{} }
func (budgets) Name() string      { return "budgets" }

func (budgets) Compare(_ context.Context, ev core.Evidence, e core.Expectation) (core.Verdict, error) {
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
		cost, err := costSum(ev.Trace)
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

// costSum returns the total gen_ai cost in USD across all spans. Absent cost (no
// span carries the attribute) is a hard error — the behavior the cel comparator
// inherits (§5, cost-absent decision). A malformed or out-of-range value is also
// a hard error.
func costSum(t *trace.Trace) (float64, error) {
	cost := 0.0
	seen := false
	for i, s := range t.Spans {
		raw := s.Attr(genai.CostUSD)
		if raw == "" {
			continue
		}
		c, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("budgets: span[%d] (%q) invalid %s=%q: %w", i, s.Name, genai.CostUSD, raw, err)
		}
		if c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
			return 0, fmt.Errorf("budgets: span[%d] (%q) %s=%q out of range: must be a finite value >= 0", i, s.Name, genai.CostUSD, raw)
		}
		cost += c
		seen = true
	}
	if !seen {
		return 0, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
	}
	return cost, nil
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
