package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
)

type world struct {
	eng    *engine.Engine
	target string
	ev     core.Evidence
}

// Initializer binds the v1 grammar; a fresh world is created per scenario.
func Initializer(eng *engine.Engine) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &world{eng: eng}

		sc.Step(`^the (?:agent|service) target "([^"]+)"$`, w.target_)
		sc.Step(`^I run scenario "([^"]+)"$`, w.runScenario)
		sc.Step(`^I run the agent with prompt "([^"]*)"$`, w.runPrompt)
		sc.Step(`^the agent calls tools in order:$`, w.toolsInOrder)
		sc.Step(`^the tool "([^"]+)" is never called$`, w.toolNeverCalled)
		sc.Step(`^total tokens are under (\d+)$`, w.tokensUnder)
		sc.Step(`^total cost is under ([0-9.]+) USD$`, w.costUnder)
		sc.Step(`^total latency is under (\d+) ms$`, w.latencyUnder)
		sc.Step(`^no span has status "ERROR"$`, w.noErrorSpans)
		sc.Step(`^the result contains "([^"]*)"$`, w.resultContains)
		sc.Step(`^the result equals "([^"]*)"$`, w.resultEquals)
		sc.Step(`^the response status is (\d+)$`, w.responseStatus)
	}
}

func (w *world) target_(name string) error { w.target = name; return nil }

func (w *world) runScenario(name string) error { return w.drive([]string{"--scenario", name}) }
func (w *world) runPrompt(p string) error      { return w.drive([]string{"--prompt", p}) }

func (w *world) drive(args []string) error {
	if w.target == "" {
		return fmt.Errorf("no target set; use a Given ... target step first")
	}
	ev, err := w.eng.Drive(context.Background(), w.target, args)
	if err != nil {
		return err
	}
	w.ev = ev
	return nil
}

func (w *world) check(name string, exp core.Expectation) error {
	c, ok := w.eng.Comparator(name)
	if !ok {
		return fmt.Errorf("no comparator %q", name)
	}
	v, err := c.Compare(context.Background(), w.ev, exp)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !v.Pass {
		return fmt.Errorf("%s failed: %s", name, strings.Join(v.Reasons, "; "))
	}
	return nil
}

func (w *world) toolsInOrder(tbl *godog.Table) error {
	var order []string
	for i, row := range tbl.Rows {
		if len(row.Cells) == 0 {
			return fmt.Errorf("tools-in-order: table row %d has no cells", i)
		}
		order = append(order, strings.TrimSpace(row.Cells[0].Value))
	}
	return w.check("sequence", comparator.SequenceExpectation{Order: order})
}

func (w *world) toolNeverCalled(name string) error {
	return w.check("sequence", comparator.SequenceExpectation{Forbidden: []string{name}})
}

func (w *world) tokensUnder(n int) error {
	return w.check("budgets", comparator.BudgetExpectation{MaxTokens: &n})
}

func (w *world) costUnder(c float64) error {
	return w.check("budgets", comparator.BudgetExpectation{MaxCostUSD: &c})
}

func (w *world) latencyUnder(ms int) error {
	d := time.Duration(ms) * time.Millisecond
	return w.check("budgets", comparator.BudgetExpectation{MaxLatency: &d})
}

func (w *world) noErrorSpans() error {
	zero := 0
	return w.check("budgets", comparator.BudgetExpectation{MaxErrors: &zero})
}

func (w *world) resultContains(s string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "contains", Want: s})
}

func (w *world) resultEquals(s string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "exact", Want: s})
}

func (w *world) responseStatus(code int) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "status", Want: fmt.Sprintf("%d", code)})
}
