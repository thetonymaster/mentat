package report

import (
	"fmt"
	"strings"
	"sync"

	"github.com/thetonymaster/mentat/internal/core"
)

// JudgeCost prices one judge-usage ledger row in USD, mirroring the SUT-cost rules
// (comparator.deriveCost / §4.3): with NO pricing table configured only emitted cost
// counts, and judge usage carries none, so the cost is 0 (the pre-US6 behaviour); a
// row that made no call is likewise free. But once a pricing table IS configured, a
// real call (Calls > 0) whose model is empty (ambiguous — cannot attribute a rate) or
// absent from the table (unknown) is a HARD ERROR, never a silent $0.00 for a real
// call (judge-ledger contract, Constitution IV).
func JudgeCost(u core.JudgeUsage, pricing core.Pricing) (float64, error) {
	if u.Calls == 0 {
		return 0, nil
	}
	if len(pricing) == 0 {
		return 0, nil
	}
	if strings.TrimSpace(u.Model) == "" {
		return 0, fmt.Errorf("judge cost: %d call(s) carry no model; cannot attribute a rate (ambiguous)", u.Calls)
	}
	rate, ok := pricing[u.Model]
	if !ok {
		return 0, fmt.Errorf("judge cost: model %q not in pricing table", u.Model)
	}
	return float64(u.InputTokens)/1e6*rate.InputPerMTok + float64(u.OutputTokens)/1e6*rate.OutputPerMTok, nil
}

// Price fills the CostUsd of every scenario's judge ledger and the suite JudgeTotal,
// using the pricing table. It is the render/budget boundary where a pricing hard
// error is legal (unlike report.Derive, which never fails a scenario, audit A8): an
// unknown/ambiguous judge model is returned as a wrapped error naming the offending
// scenario, so the caller emits nothing rather than a report with a fabricated $0.
// A run with no judge usage is a no-op — Price leaves the report byte-identical.
func Price(rep *core.RunReport, pricing core.Pricing) error {
	var totalCost float64
	for i := range rep.Scenarios {
		j := rep.Scenarios[i].Judge
		if j == nil {
			continue
		}
		cost, err := JudgeCost(*j, pricing)
		if err != nil {
			return fmt.Errorf("pricing judge ledger for scenario %q: %w", rep.Scenarios[i].Name, err)
		}
		j.CostUsd = cost
		totalCost += cost
	}
	if rep.JudgeTotal != nil {
		rep.JudgeTotal.CostUsd = totalCost
	}
	return nil
}

// Budget accumulates completed judge spend across a run and trips once it exceeds a
// configured ceiling (US6). It is the unit-testable core of the post-scenario budget
// check, deliberately independent of godog: the composition root calls Add once per
// completed scenario (in the After hook) and, on a non-nil error, cancels the suite
// context so no NEW scenario starts — in-flight votes finish, matching the contract's
// "completed calls only, checked after each scenario" accounting. A max <= 0 means
// unlimited: Add is a no-op, so a run without a budget behaves exactly as before.
// Safe for the concurrent scenarios godog may run under -concurrency.
type Budget struct {
	mu      sync.Mutex
	max     float64
	pricing core.Pricing
	spent   float64
	err     error
}

// NewBudget builds a Budget with a USD ceiling and the pricing table used to price
// each scenario's judge usage. maxUSD <= 0 disables accounting (unlimited).
func NewBudget(maxUSD float64, pricing core.Pricing) *Budget {
	return &Budget{max: maxUSD, pricing: pricing}
}

// Add prices sr's completed judge usage, accumulates it, and returns a hard error
// when the running total crosses the ceiling (naming spent, budget, and the crossing
// scenario) or when the usage cannot be priced (ambiguous/unknown model). The FIRST
// error is retained (see Err) and further Adds return it unchanged, so a scenario that
// slips through after the abort signal cannot mask the original cause — but their
// completed cost is STILL folded into Spent(), so the accounting never underreports
// actual usage once the budget has tripped.
func (b *Budget) Add(sr core.ScenarioResult) error {
	if b.max <= 0 {
		return nil // unlimited: no accounting, no cost computed (pre-US6 behaviour)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if sr.Judge != nil {
		cost, err := JudgeCost(*sr.Judge, b.pricing)
		if err != nil {
			if b.err == nil {
				b.err = fmt.Errorf("judge budget: scenario %q: %w", sr.Name, err)
			}
			return b.err
		}
		b.spent += cost
	}
	if b.spent > b.max && b.err == nil {
		b.err = fmt.Errorf("judge budget exceeded: spent $%.4f exceeds budget $%.4f at scenario %q", b.spent, b.max, sr.Name)
	}
	return b.err
}

// Err returns the retained trip/pricing error (nil until the budget is crossed or a
// usage cannot be priced). The composition root reads it after the suite to decide the
// exit code and print the reason, while still emitting the reports.
func (b *Budget) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Spent returns the accumulated completed judge spend in USD.
func (b *Budget) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}
