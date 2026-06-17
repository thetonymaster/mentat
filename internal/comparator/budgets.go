package comparator

import (
	"context"
	"fmt"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
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
		total := 0
		for _, s := range ev.Trace.Spans {
			if n, ok := s.AttrInt(genai.InTokens); ok {
				total += n
			}
			if n, ok := s.AttrInt(genai.OutTokens); ok {
				total += n
			}
		}
		if total > *exp.MaxTokens {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("total tokens %d exceed budget %d", total, *exp.MaxTokens))
		}
	}

	if exp.MaxCostUSD != nil {
		cost := 0.0
		seen := false
		for _, s := range ev.Trace.Spans {
			if c, ok := s.AttrFloat(genai.CostUSD); ok {
				cost += c
				seen = true
			}
		}
		if !seen {
			return core.Verdict{}, fmt.Errorf("budgets: cost not available (no %s attribute); add a pricing table or drop the cost assertion", genai.CostUSD)
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
		errs := 0
		for _, s := range ev.Trace.Spans {
			if s.Status == "Error" {
				errs++
			}
		}
		if errs > *exp.MaxErrors {
			v.Pass = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%d error spans exceed budget %d", errs, *exp.MaxErrors))
		}
	}

	return v, nil
}
