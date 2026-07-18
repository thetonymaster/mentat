package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	eng *engine.Engine
	col *report.Collector
	// ctx is the scenario's context, captured in sc.Before. Every scenario-scoped
	// operation (drive, resolve, compare, aggregate, judge) runs under it so one
	// budget/cancellation bounds them all (feature 003, FR-004). A fresh background
	// context is banned in this file (a ctx_guard test enforces it) to prevent the
	// audit-B2 regression of discarding the scenario context.
	ctx context.Context
	// uri is the feature file's path, captured in sc.Before from scenario.Uri. The
	// body-fixture step resolves a relative fixture path against filepath.Dir(uri)
	// so a body lives next to the .feature that references it; absolute paths are
	// used as-is.
	uri        string
	target     string
	ev         core.Evidence
	evs        []core.Evidence
	n          int
	parallel   bool
	lastDetail *core.AggregateDetail
	// lastJudge accumulates the judge-token usage of every semantic check in the
	// scenario (US6). The After hook is verdict-authoritative (it rebuilds the
	// Verdict from stepErr, not the comparator's return), so — mirroring lastDetail —
	// check() records each Verdict.Judge here so the usage still reaches report.Derive.
	// Nil until a semantic check actually issues a judge call (no fabricated zeros).
	lastJudge *core.JudgeUsage
	// budget is the optional post-scenario judge-spend ceiling (US6); nil disables the
	// check (today's behaviour). abort cancels the suite context when the budget trips,
	// so no new scenario starts a judge call. Both are wired at the composition root.
	budget *report.Budget
	abort  context.CancelFunc
}

// Initializer binds the v1 grammar; results go to a discarded collector.
// Existing callers are unaffected; results are not surfaced.
func Initializer(eng *engine.Engine) func(*godog.ScenarioContext) {
	return InitializerWithCollector(eng, report.NewCollector())
}

// InitializerWithCollector binds the v1 grammar and records one ScenarioResult per
// scenario into col. Use this at the composition root to capture run reports. It runs
// with no judge budget (unlimited) — today's behaviour.
func InitializerWithCollector(eng *engine.Engine, col *report.Collector) func(*godog.ScenarioContext) {
	return InitializerWithBudget(eng, col, nil, nil)
}

// InitializerWithBudget is InitializerWithCollector plus a post-scenario judge-spend
// budget (US6). After each scenario's ledger is collected, the budget accounts its
// completed judge cost; when the running total crosses the ceiling (or a usage cannot
// be priced) abort cancels the suite context so no NEW scenario starts a judge call.
// A nil budget disables the check (unlimited); a nil abort makes the trip advisory.
func InitializerWithBudget(eng *engine.Engine, col *report.Collector, budget *report.Budget, abort context.CancelFunc) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		w := &world{eng: eng, col: col, budget: budget, abort: abort}

		// Registration is table-driven: registerSteps binds every pattern in the
		// stepDefs metadata table (metadata.go), which is the single source of truth
		// shared by `mentat steps` / docs/steps.md. A drift test fails if any step is
		// registered outside this table (see metadata_test.go).
		registerSteps(sc, w)

		// §7: compile every CEL expression in the scenario before any step runs,
		// so a malformed expectation fails before an expensive SUT is driven.
		sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
			// Capture the scenario context so every step's drive/compare/judge runs
			// under it (FR-004). godog cancels it on suite interruption, so a signal
			// or scenario timeout reaches all scenario-scoped work.
			w.ctx = ctx
			// Capture the feature file path so the body-fixture step can resolve a
			// relative fixture against the feature's own directory.
			w.uri = scenario.Uri
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
			v := core.Verdict{Pass: stepErr == nil, Detail: w.lastDetail, Judge: w.lastJudge}
			if stepErr != nil {
				v.Reasons = []string{stepErr.Error()}
			}
			// report.Derive is an observer and never fails a scenario (audit A8):
			// a derivation problem yields a DerivationNote on the entry, not an
			// error. The verdict comes only from stepErr, so the hook returns nil.
			sr := report.Derive(scenario.Name, scenario.Uri, tagNames(scenario.Tags), v, w.evs, w.eng.Pricing())
			w.col.Append(sr)
			// Post-scenario judge budget (US6): account this scenario's completed judge
			// cost. On a trip (or an unpriceable usage) abort the suite context so the
			// next scenario starts no new judge call — in-flight votes already finished.
			// The scenario verdict itself is untouched (the hook still returns nil): the
			// budget gates SUBSEQUENT scenarios, it does not retroactively fail this one.
			if w.budget != nil {
				if err := w.budget.Add(sr); err != nil && w.abort != nil {
					w.abort()
				}
			}
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

func (w *world) runScenario(name string) error { return w.drive([]string{"--scenario", name}, "") }
func (w *world) runPrompt(p string) error      { return w.drive([]string{"--prompt", p}, "") }

// sendRequestBodyDoc drives the target with the doc-string as the request body
// (RunSpec.Input), the HTTP analogue of runPrompt. A missing doc-string is a hard
// error rather than a silently-empty body (No Silent Fallbacks).
func (w *world) sendRequestBodyDoc(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("send-request-body step: expected a docstring body, got none")
	}
	if err := w.requireBodyAdapter(); err != nil {
		return err
	}
	return w.drive(nil, doc.Content)
}

// sendRequestBodyFixture reads a fixture file and drives the target with its
// contents as the request body (RunSpec.Input). A relative path resolves against
// the feature directory (filepath.Dir(w.uri)); an absolute path is used as-is. An
// unreadable fixture is a hard error naming the RESOLVED path, raised before any
// drive so a bad fixture never sends an empty body (No Silent Fallbacks).
func (w *world) sendRequestBodyFixture(path string) error {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(w.uri), resolved)
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("read request body fixture %q: %w", resolved, err)
	}
	if err := w.requireBodyAdapter(); err != nil {
		return err
	}
	return w.drive(nil, string(body))
}

// requireBodyAdapter rejects a request-body step whose target uses an adapter that
// does not consume a request body — only the http adapter does. Without this the
// shell adapter would silently discard the body (Constitution IV: no silent
// fallbacks). When no target is set the accessor reports ok=false and this passes
// through, leaving drive's "no target set" error to fire (path preserved).
func (w *world) requireBodyAdapter() error {
	if a, ok := w.eng.Adapter(w.target); ok && a != "http" {
		return fmt.Errorf("request-body step: target %q uses the %q adapter, which does not consume a request body (only http does)", w.target, a)
	}
	return nil
}

func (w *world) drive(args []string, input string) error {
	if w.target == "" {
		return fmt.Errorf("no target set; use a Given ... target step first")
	}
	n := w.n
	if n < 1 {
		n = 1
	}
	evs, err := w.eng.DriveNInput(w.ctx, w.target, args, input, n, w.parallel)
	if err != nil {
		return err
	}
	w.evs = evs
	w.ev = evs[0] // single-run comparators evaluate the first run
	// R4 rule 2 (A2): a single-run scenario fails the moment its one run failed —
	// even with no Then step, and even though a resolve failure retains the driver
	// Output. Multi-run (@runs(N>1)) keeps the typed-failed-sample model so the
	// aggregate policy decides.
	if n == 1 && evs[0].Failed {
		return fmt.Errorf("run %q failed: %s", evs[0].RunID, evs[0].FailureMsg)
	}
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
	v, err := c.Compare(w.ctx, w.ev, exp)
	// Record any judge usage BEFORE the pass/fail branch: a failing semantic verdict
	// still consumed judge tokens, and the ledger must account for it (US6). A
	// non-semantic comparator leaves v.Judge nil, so this is a no-op for them.
	if v.Judge != nil {
		w.lastJudge = addJudge(w.lastJudge, v.Judge)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !v.Pass {
		return fmt.Errorf("%s failed: %s", name, strings.Join(v.Reasons, "; "))
	}
	return nil
}

// addJudge folds add into acc field-wise, allocating acc on first use. It sums the
// per-check judge usage across a scenario's semantic steps (US6). The judge model id
// is carried (last non-empty wins — every vote goes to the same configured judge).
func addJudge(acc, add *core.JudgeUsage) *core.JudgeUsage {
	if add == nil {
		return acc
	}
	if acc == nil {
		acc = &core.JudgeUsage{}
	}
	acc.Calls += add.Calls
	acc.InputTokens += add.InputTokens
	acc.OutputTokens += add.OutputTokens
	if add.Model != "" {
		acc.Model = add.Model
	}
	return acc
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
			n, err := strconv.Atoi(mm[1])
			if err != nil {
				return 0, 0, fmt.Errorf("steps: span ordinal %q: %w", f[1], err)
			}
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
	v, err := c.Aggregate(w.ctx, w.evs, comparator.AggregateCELExpectation{Expr: expr})
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
// not loaded — before the SUT is driven, mirroring precompileScenario for CEL (§7). It
// delegates to the collect-all ShapePatternFindings (shared with `mentat validate`) and
// surfaces the FIRST finding as the scenario-init error, so behaviour is unchanged.
func (w *world) precheckShapePatterns(steps []*messages.PickleStep) error {
	if fs := ShapePatternFindings(w.eng, steps, Source{}); len(fs) > 0 {
		return fmt.Errorf("scenario-init: %s", fs[0].Message)
	}
	return nil
}

// precompileScenario compiles every "the run satisfies" and "the runs satisfy"
// expression in the scenario before any step executes (§7). A syntax/type/unknown-var
// error fails the scenario at init, before the SUT is driven. It delegates to the
// collect-all CELFindings (shared with `mentat validate`) and surfaces the FIRST
// finding as the scenario-init error, so behaviour is unchanged.
func (w *world) precompileScenario(steps []*messages.PickleStep) error {
	if fs := CELFindings(w.eng, steps, Source{}); len(fs) > 0 {
		return fmt.Errorf("scenario-init: %s", fs[0].Message)
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
// that begins "@runs(" but does not match the strict form is a hard error. It wraps
// the shared parseRunsTagRaw (precheck.go) — the same parser RunsTagFindings uses for
// collect-all validation — prefixing "scenario-init:" so the fail-fast error text is
// unchanged.
func parseRunsTag(tags []*messages.PickleTag) (int, bool, error) {
	n, parallel, _, msg := parseRunsTagRaw(tags)
	if msg != "" {
		return 0, false, fmt.Errorf("scenario-init: %s", msg)
	}
	return n, parallel, nil
}
