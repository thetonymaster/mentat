package steps

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
)

var (
	reSatisfiesInline = regexp.MustCompile(`^the run satisfies "([^"]*)"$`)
	reSatisfiesDoc    = regexp.MustCompile(`^the run satisfies:$`)
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
		sc.Step(`^the result matches regex "([^"]*)"$`, w.resultMatchesRegex)
		sc.Step(`^the services are called in order:$`, w.servicesInOrder)
		sc.Step(`^the service "([^"]+)" is never called$`, w.serviceNeverCalled)
		sc.Step(`^the response body json-contains:$`, w.responseBodyJSONContains)
		sc.Step(`^the run satisfies "([^"]*)"$`, w.runSatisfies)
		sc.Step(`^the run satisfies:$`, w.runSatisfiesDoc)

		// §7: compile every CEL expression in the scenario before any step runs,
		// so a malformed expectation fails before an expensive SUT is driven.
		sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
			if err := w.precompileScenario(scenario.Steps); err != nil {
				return ctx, err
			}
			return ctx, nil
		})
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
		tool := strings.TrimSpace(row.Cells[0].Value)
		if tool == "" {
			return fmt.Errorf("tools-in-order: table row %d has empty tool name", i)
		}
		order = append(order, tool)
	}
	if len(order) == 0 {
		return fmt.Errorf("tools-in-order: at least one tool is required")
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

func (w *world) resultMatchesRegex(re string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "regex", Want: re})
}

func (w *world) responseStatus(code int) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "status", Want: fmt.Sprintf("%d", code)})
}

func (w *world) servicesInOrder(tbl *godog.Table) error {
	var order []string
	for i, row := range tbl.Rows {
		if len(row.Cells) == 0 {
			return fmt.Errorf("services-in-order: table row %d has no cells", i)
		}
		svc := strings.TrimSpace(row.Cells[0].Value)
		if svc == "" {
			return fmt.Errorf("services-in-order: table row %d has empty service name", i)
		}
		order = append(order, svc)
	}
	if len(order) == 0 {
		return fmt.Errorf("services-in-order: at least one service is required")
	}
	return w.check("sequence", comparator.SequenceExpectation{Kind: "service", Order: order})
}

func (w *world) serviceNeverCalled(name string) error {
	return w.check("sequence", comparator.SequenceExpectation{Kind: "service", Forbidden: []string{name}})
}

func (w *world) responseBodyJSONContains(doc *godog.DocString) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "json-subset", Want: doc.Content})
}

func (w *world) runSatisfies(expr string) error {
	return w.check("cel", comparator.CELExpectation{Expr: expr})
}

func (w *world) runSatisfiesDoc(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("the run satisfies: expected a docstring expression, got none")
	}
	return w.check("cel", comparator.CELExpectation{Expr: doc.Content})
}

// precompileScenario compiles every "the run satisfies" expression in the
// scenario before any step executes (§7). A syntax/type/unknown-var error fails
// the scenario at init, before the SUT is driven.
func (w *world) precompileScenario(steps []*messages.PickleStep) error {
	for _, st := range steps {
		expr, ok := satisfiesExpr(st)
		if !ok {
			continue
		}
		c, ok := w.eng.Comparator("cel")
		if !ok {
			return fmt.Errorf("scenario-init: 'the run satisfies' requires the cel comparator, which is not registered")
		}
		pc, ok := c.(interface{ Compile(string) error })
		if !ok {
			return fmt.Errorf("scenario-init: cel comparator %T does not support pre-compilation", c)
		}
		if err := pc.Compile(expr); err != nil {
			return fmt.Errorf("scenario-init: %w", err)
		}
	}
	return nil
}

// satisfiesExpr extracts a CEL expression from a "the run satisfies" step, in
// either the inline quoted form or the trailing docstring.
func satisfiesExpr(st *messages.PickleStep) (string, bool) {
	if m := reSatisfiesInline.FindStringSubmatch(st.Text); m != nil {
		return m[1], true
	}
	if reSatisfiesDoc.MatchString(st.Text) && st.Argument != nil && st.Argument.DocString != nil {
		return st.Argument.DocString.Content, true
	}
	return "", false
}
