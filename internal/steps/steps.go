package steps

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/report"
)

var (
	reSatisfiesInline     = regexp.MustCompile(`^the run satisfies "([^"]*)"$`)
	reSatisfiesDoc        = regexp.MustCompile(`^the run satisfies:$`)
	reRunsSatisfiesInline = regexp.MustCompile(`^the runs satisfy "([^"]*)"$`)
	reRunsSatisfiesDoc    = regexp.MustCompile(`^the runs satisfy:$`)
	reRunsTag             = regexp.MustCompile(`^@runs\((\d+)(?:,(parallel))?\)$`)
	reMatchesShape        = regexp.MustCompile(`^the run matches shape "([^"]*)"$`)
	reSpanOrdinal         = regexp.MustCompile(`^(\d+)(?:st|nd|rd|th)$`)
)

type world struct {
	eng        *engine.Engine
	col        *report.Collector
	target     string
	ev         core.Evidence
	evs        []core.Evidence
	n          int
	parallel   bool
	lastDetail *core.AggregateDetail
}

// Initializer binds the v1 grammar; results go to a discarded collector.
// Existing callers are unaffected; results are not surfaced.
func Initializer(eng *engine.Engine) func(*godog.ScenarioContext) {
	return InitializerWithCollector(eng, report.NewCollector())
}

// InitializerWithCollector binds the v1 grammar and records one ScenarioResult per
// scenario into col. Use this at the composition root to capture run reports.
func InitializerWithCollector(eng *engine.Engine, col *report.Collector) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &world{eng: eng, col: col}

		sc.Step(`^the (?:agent|service) target "([^"]+)"$`, w.target_)
		sc.Step(`^I run scenario "([^"]+)"$`, w.runScenario)
		sc.Step(`^I run the agent with prompt "([^"]*)"$`, w.runPrompt)
		sc.Step(`^the agent calls tools in order:$`, w.toolsInOrder)
		sc.Step(`^the tool "([^"]+)" is never called$`, w.toolNeverCalled)
		sc.Step(`^the tool "([^"]+)" is called at most (\d+) times$`, w.toolCalledAtMost)
		sc.Step(`^total tokens are under (\d+)$`, w.tokensUnder)
		sc.Step(`^total cost is under ([0-9.]+) USD$`, w.costUnder)
		sc.Step(`^total latency is under (\d+) ms$`, w.latencyUnder)
		sc.Step(`^no span has status "ERROR"$`, w.noErrorSpans)
		sc.Step(`^the result contains "([^"]*)"$`, w.resultContains)
		sc.Step(`^the result equals "([^"]*)"$`, w.resultEquals)
		sc.Step(`^the response status is (\d+)$`, w.responseStatus)
		sc.Step(`^the result matches regex "([^"]*)"$`, w.resultMatchesRegex)
		sc.Step(`^the result means "([^"]*)"$`, w.resultMeans)
		sc.Step(`^the result means:$`, w.resultMeansDoc)
		sc.Step(`^the services are called in order:$`, w.servicesInOrder)
		sc.Step(`^the service "([^"]+)" is never called$`, w.serviceNeverCalled)
		sc.Step(`^the response body json-contains:$`, w.responseBodyJSONContains)
		sc.Step(`^the response body matches schema:$`, w.responseBodyMatchesSchema)
		sc.Step(`^the run satisfies "([^"]*)"$`, w.runSatisfies)
		sc.Step(`^the run satisfies:$`, w.runSatisfiesDoc)
		sc.Step(`^the runs satisfy "([^"]*)"$`, w.runsSatisfies)
		sc.Step(`^the runs satisfy:$`, w.runsSatisfiesDoc)

		sc.Step(`^a span matching "([^"]*)" exists$`, w.shapeExists)
		sc.Step(`^no span matching "([^"]*)" exists$`, w.shapeAbsent)
		sc.Step(`^at least (\d+) spans? match(?:es)? "([^"]*)"$`, w.shapeAtLeast)
		sc.Step(`^exactly (\d+) spans? match(?:es)? "([^"]*)"$`, w.shapeExactly)
		sc.Step(`^a span matching "([^"]*)" is a child of a span matching "([^"]*)"$`, w.shapeChildOf)
		sc.Step(`^a span matching "([^"]*)" is a descendant of a span matching "([^"]*)"$`, w.shapeDescendantOf)
		sc.Step(`^a span matching "([^"]*)" has at least (\d+) children matching "([^"]*)"$`, w.shapeFanoutAtLeast)
		sc.Step(`^a span matching "([^"]*)" has exactly (\d+) children matching "([^"]*)"$`, w.shapeFanoutExactly)

		sc.Step(`^the run matches shape "([^"]*)"$`, w.matchesShape)

		// §4.1 span-attribute result source — tool convenience form
		sc.Step(`^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (contains|equals|matches regex) "([^"]*)"$`, w.resultToolValue)
		sc.Step(`^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (json-contains|matches schema):$`, w.resultToolDoc)

		// §4.2 span-attribute result source — general selector form
		sc.Step(`^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?(?:the )?span matching "([^"]+)" (contains|equals|matches regex) "([^"]*)"$`, w.resultAttrValue)
		sc.Step(`^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?(?:the )?span matching "([^"]+)" (json-contains|matches schema):$`, w.resultAttrDoc)

		// §7: compile every CEL expression in the scenario before any step runs,
		// so a malformed expectation fails before an expensive SUT is driven.
		sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
			n, parallel, err := parseRunsTag(scenario.Tags)
			if err != nil {
				return ctx, err
			}
			w.n, w.parallel = n, parallel
			if err := w.precompileScenario(scenario.Steps); err != nil {
				return ctx, err
			}
			if err := w.precheckShapePatterns(scenario.Steps); err != nil {
				return ctx, err
			}
			return ctx, nil
		})

		sc.After(func(ctx context.Context, scenario *godog.Scenario, stepErr error) (context.Context, error) {
			v := core.Verdict{Pass: stepErr == nil, Detail: w.lastDetail}
			if stepErr != nil {
				v.Reasons = []string{stepErr.Error()}
			}
			sr, err := report.Derive(scenario.Name, tagNames(scenario.Tags), v, w.evs, w.eng.Pricing())
			if err != nil {
				return ctx, err
			}
			w.col.Append(sr)
			return ctx, nil
		})
	}
}

// tagNames extracts the Name field from godog PickleTag slice.
func tagNames(tags []*messages.PickleTag) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Name)
	}
	return out
}

func (w *world) target_(name string) error { w.target = name; return nil }

func (w *world) runScenario(name string) error { return w.drive([]string{"--scenario", name}) }
func (w *world) runPrompt(p string) error      { return w.drive([]string{"--prompt", p}) }

func (w *world) drive(args []string) error {
	if w.target == "" {
		return fmt.Errorf("no target set; use a Given ... target step first")
	}
	n := w.n
	if n < 1 {
		n = 1
	}
	evs, err := w.eng.DriveN(context.Background(), w.target, args, n, w.parallel)
	if err != nil {
		return err
	}
	w.evs = evs
	w.ev = evs[0] // single-run comparators evaluate the first run
	return nil
}

func (w *world) check(name string, exp core.Expectation) error {
	if w.n > 1 {
		return fmt.Errorf("single-run step in a @runs(%d) scenario evaluates only the first run; use \"the runs satisfy\" for assertions across all runs", w.n)
	}
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

func (w *world) toolCalledAtMost(name string, n int) error {
	return w.check("retries", comparator.RetriesExpectation{Tool: name, Max: n})
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

// parseSpanSpec maps the captured span-spec slot to a Quant (+1-based index for
// Nth). "" => QuantOne (bare). A trailing "call"/"span" word is ignored; leading
// words: the/first/last/<n>th/every/any.
func parseSpanSpec(slot string) (comparator.Quant, int, error) {
	f := strings.Fields(slot)
	if n := len(f); n > 0 && (f[n-1] == "call" || f[n-1] == "span") {
		f = f[:n-1]
	}
	switch {
	case len(f) == 0:
		return comparator.QuantOne, 0, nil
	case f[0] == "every":
		return comparator.QuantEvery, 0, nil
	case f[0] == "any":
		return comparator.QuantAny, 0, nil
	case len(f) == 2 && f[0] == "the" && f[1] == "first":
		return comparator.QuantFirst, 0, nil
	case len(f) == 2 && f[0] == "the" && f[1] == "last":
		return comparator.QuantLast, 0, nil
	case len(f) == 2 && f[0] == "the":
		if mm := reSpanOrdinal.FindStringSubmatch(f[1]); mm != nil {
			n, _ := strconv.Atoi(mm[1])
			if n < 1 {
				return 0, 0, fmt.Errorf("span ordinal must be >= 1, got %q", f[1])
			}
			return comparator.QuantNth, n, nil
		}
	}
	return 0, 0, fmt.Errorf("unrecognized span selector %q", slot)
}

// verbToMatcher maps a Gherkin matcher verb to a registered matcher name.
func verbToMatcher(verb string) (string, error) {
	switch verb {
	case "contains":
		return "contains", nil
	case "equals":
		return "exact", nil
	case "matches regex":
		return "regex", nil
	case "json-contains":
		return "json-subset", nil
	case "matches schema":
		return "schema", nil
	default:
		return "", fmt.Errorf("unknown result matcher verb %q", verb)
	}
}

// toolSpanSource builds a tool-convenience SpanSource (gen_ai.tool.name selector,
// gen_ai.tool.call.result attribute) from a parsed span-spec slot.
func toolSpanSource(slot, tool string) (*comparator.SpanSource, error) {
	q, idx, err := parseSpanSpec(slot)
	if err != nil {
		return nil, fmt.Errorf("result of tool %q: %w", tool, err)
	}
	return &comparator.SpanSource{
		Selector: comparator.Selector{{Key: genai.ToolName, Value: tool}},
		Attr:     genai.ToolResult,
		Quant:    q,
		Index:    idx,
	}, nil
}

func (w *world) resultToolValue(slot, tool, verb, want string) error {
	src, err := toolSpanSource(slot, tool)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: want, Source: src})
}

func (w *world) resultToolDoc(slot, tool, verb string, doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("result of tool %q %s: expected a docstring, got none", tool, verb)
	}
	src, err := toolSpanSource(slot, tool)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: doc.Content, Source: src})
}

// attrSpanSource builds a general SpanSource from a named attribute, a span-spec
// slot, and a raw k=v selector.
func attrSpanSource(attr, slot, selStr string) (*comparator.SpanSource, error) {
	q, idx, err := parseSpanSpec(slot)
	if err != nil {
		return nil, fmt.Errorf("attribute %q of span matching %q: %w", attr, selStr, err)
	}
	selr, err := comparator.ParseSelector(selStr)
	if err != nil {
		return nil, fmt.Errorf("parse result span selector %q: %w", selStr, err)
	}
	return &comparator.SpanSource{Selector: selr, Attr: attr, Quant: q, Index: idx}, nil
}

func (w *world) resultAttrValue(attr, slot, selStr, verb, want string) error {
	src, err := attrSpanSource(attr, slot, selStr)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: want, Source: src})
}

func (w *world) resultAttrDoc(attr, slot, selStr, verb string, doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("attribute %q of span matching %q %s: expected a docstring, got none", attr, selStr, verb)
	}
	src, err := attrSpanSource(attr, slot, selStr)
	if err != nil {
		return err
	}
	matcher, err := verbToMatcher(verb)
	if err != nil {
		return err
	}
	return w.check("result", comparator.ResultExpectation{Matcher: matcher, Want: doc.Content, Source: src})
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

// resultMeans routes the inline `the result means "..."` step through the result
// comparator's "semantic" matcher — identical path to contains/equals/regex. The
// matcher's own empty-want guard surfaces a blank meaning as a hard error (FR-013).
func (w *world) resultMeans(s string) error {
	return w.check("result", comparator.ResultExpectation{Matcher: "semantic", Want: s})
}

// resultMeansDoc routes the docstring `the result means:` step through the result
// comparator's "semantic" matcher.
func (w *world) resultMeansDoc(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("the result means: expected a docstring meaning, got none")
	}
	return w.check("result", comparator.ResultExpectation{Matcher: "semantic", Want: doc.Content})
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

func (w *world) responseBodyMatchesSchema(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("the response body matches schema: expected a docstring schema, got none")
	}
	return w.check("result", comparator.ResultExpectation{Matcher: "schema", Want: doc.Content})
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

func (w *world) runsSatisfies(expr string) error {
	return w.checkRuns(expr)
}

func (w *world) runsSatisfiesDoc(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("the runs satisfy: expected a docstring expression, got none")
	}
	return w.checkRuns(doc.Content)
}

func (w *world) checkRuns(expr string) error {
	if len(w.evs) == 0 {
		return fmt.Errorf("the runs satisfy: no runs driven; use a When ... step first")
	}
	c, ok := w.eng.AggregateComparator("aggregate-cel")
	if !ok {
		return fmt.Errorf("no aggregate comparator %q", "aggregate-cel")
	}
	v, err := c.Aggregate(context.Background(), w.evs, comparator.AggregateCELExpectation{Expr: expr})
	if err != nil {
		return fmt.Errorf("aggregate-cel: %w", err)
	}
	w.lastDetail = v.Detail
	if !v.Pass {
		return fmt.Errorf("aggregate-cel failed: %s", strings.Join(v.Reasons, "; "))
	}
	return nil
}

// parseShapeSelector wraps ParseSelector failures with which selector failed
// (role: "subject" or "parent") and the raw value, per the %w error-wrapping
// convention — so a malformed shape step reports actionable, consistent diagnostics.
func parseShapeSelector(role, raw string) (comparator.Selector, error) {
	sel, err := comparator.ParseSelector(raw)
	if err != nil {
		return nil, fmt.Errorf("parse shape %s selector %q: %w", role, raw, err)
	}
	return sel, nil
}

func (w *world) shapeExists(s string) error {
	sel, err := parseShapeSelector("subject", s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "exists", Subject: sel})
}

func (w *world) shapeAbsent(s string) error {
	sel, err := parseShapeSelector("subject", s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "absent", Subject: sel})
}

func (w *world) shapeAtLeast(n int, s string) error {
	sel, err := parseShapeSelector("subject", s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "exists", Subject: sel, Count: &comparator.Count{Op: ">=", N: n}})
}

func (w *world) shapeExactly(n int, s string) error {
	sel, err := parseShapeSelector("subject", s)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "exists", Subject: sel, Count: &comparator.Count{Op: "==", N: n}})
}

func (w *world) shapeChildOf(child, parent string) error {
	cs, err := parseShapeSelector("subject", child)
	if err != nil {
		return err
	}
	ps, err := parseShapeSelector("parent", parent)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "containment", Subject: cs, Parent: ps, Relation: "child"})
}

func (w *world) shapeDescendantOf(child, parent string) error {
	cs, err := parseShapeSelector("subject", child)
	if err != nil {
		return err
	}
	ps, err := parseShapeSelector("parent", parent)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "containment", Subject: cs, Parent: ps, Relation: "descendant"})
}

func (w *world) shapeFanoutAtLeast(parent string, n int, child string) error {
	ps, err := parseShapeSelector("parent", parent)
	if err != nil {
		return err
	}
	cs, err := parseShapeSelector("subject", child)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "fanout", Subject: cs, Parent: ps, Relation: "child", Count: &comparator.Count{Op: ">=", N: n}})
}

func (w *world) shapeFanoutExactly(parent string, n int, child string) error {
	ps, err := parseShapeSelector("parent", parent)
	if err != nil {
		return err
	}
	cs, err := parseShapeSelector("subject", child)
	if err != nil {
		return err
	}
	return w.check("shape", comparator.ShapeExpectation{Kind: "fanout", Subject: cs, Parent: ps, Relation: "child", Count: &comparator.Count{Op: "==", N: n}})
}

func (w *world) matchesShape(name string) error {
	clauses, ok := w.eng.ShapePattern(name)
	if !ok {
		return fmt.Errorf("unknown shape pattern %q (no such pattern under the expectations dir)", name)
	}
	return w.check("shape", comparator.ShapePatternExpectation{Name: name, Clauses: clauses})
}

// precheckShapePatterns fails a scenario at init if it references a shape pattern that was
// not loaded — before the SUT is driven, mirroring precompileScenario for CEL (§7).
func (w *world) precheckShapePatterns(steps []*messages.PickleStep) error {
	for _, st := range steps {
		m := reMatchesShape.FindStringSubmatch(st.Text)
		if m == nil {
			continue
		}
		if _, ok := w.eng.ShapePattern(m[1]); !ok {
			return fmt.Errorf("scenario-init: unknown shape pattern %q (no such pattern under the expectations dir)", m[1])
		}
	}
	return nil
}

// precompileScenario compiles every "the run satisfies" and "the runs satisfy"
// expression in the scenario before any step executes (§7). A syntax/type/unknown-var
// error fails the scenario at init, before the SUT is driven.
func (w *world) precompileScenario(steps []*messages.PickleStep) error {
	for _, st := range steps {
		if expr, ok := satisfiesExpr(st); ok {
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
			continue
		}
		if expr, ok := runsSatisfiesExpr(st); ok {
			c, ok := w.eng.AggregateComparator("aggregate-cel")
			if !ok {
				return fmt.Errorf("scenario-init: 'the runs satisfy' requires the aggregate-cel comparator, which is not registered")
			}
			pc, ok := c.(interface{ Compile(string) error })
			if !ok {
				return fmt.Errorf("scenario-init: aggregate comparator %T does not support pre-compilation", c)
			}
			if err := pc.Compile(expr); err != nil {
				return fmt.Errorf("scenario-init: %w", err)
			}
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

// runsSatisfiesExpr extracts a CEL expression from a "the runs satisfy" step.
func runsSatisfiesExpr(st *messages.PickleStep) (string, bool) {
	if m := reRunsSatisfiesInline.FindStringSubmatch(st.Text); m != nil {
		return m[1], true
	}
	if reRunsSatisfiesDoc.MatchString(st.Text) && st.Argument != nil && st.Argument.DocString != nil {
		return st.Argument.DocString.Content, true
	}
	return "", false
}

// parseRunsTag reads @runs(N) / @runs(N,parallel). Absent -> (1, false, nil). A tag
// that begins "@runs(" but does not match the strict form is a hard error.
func parseRunsTag(tags []*messages.PickleTag) (int, bool, error) {
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Name, "@runs(") {
			continue
		}
		m := reRunsTag.FindStringSubmatch(tag.Name)
		if m == nil {
			return 0, false, fmt.Errorf("scenario-init: malformed @runs tag %q (want @runs(N) or @runs(N,parallel))", tag.Name)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 {
			return 0, false, fmt.Errorf("scenario-init: @runs requires N>=1, got %q", tag.Name)
		}
		return n, m[2] == "parallel", nil
	}
	return 1, false, nil
}
