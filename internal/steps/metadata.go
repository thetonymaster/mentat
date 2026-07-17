package steps

import "github.com/cucumber/godog"

// stepDef is one row of the step-metadata table: a registered Gherkin pattern
// paired with the documentation that describes it and the handler binding that
// executes it. The table (stepDefs) is the SINGLE SOURCE OF TRUTH for step
// registration — the InitializerWithCollector closure iterates it via
// registerSteps rather than hand-writing sc.Step calls, and `mentat steps` /
// docs/steps.md render from the same rows. A drift test (metadata_test.go) fails
// loudly if registration and this table ever diverge, or if any group/summary/
// example is blank, so the generated reference can never fall out of sync with
// reality.
type stepDef struct {
	// group is the reference section this step belongs to (e.g. "Drive",
	// "Shape"). It promotes the former comment grouping into data so `mentat steps`
	// and docs/steps.md group deterministically without re-parsing comments. Rows
	// sharing a group must be contiguous (a docs_test invariant).
	group string
	// pattern is the registered regexp (identical to what godog matches against).
	pattern string
	// summary is a one-line description of what the step asserts or does.
	summary string
	// example is one valid Gherkin usage of the step.
	example string
	// handler returns the bound *world method value registered for this pattern.
	// It is a selector (not the method value itself) because handlers are methods
	// on the per-scenario *world, resolved against that world at registration time.
	handler func(w *world) any
}

// StepDoc is the exported, handler-free view of one stepDefs row: the group,
// registered pattern, human summary, and one valid example. It is what `mentat
// steps` and the docs/steps.md generator consume, so both render from the SAME
// single source of truth as registration (see StepDocs).
type StepDoc struct {
	Group   string
	Pattern string
	Summary string
	Example string
}

// StepDocs returns the documentation row for every registered step, in table
// (grammar-grouped) order. It is the sole accessor the CLI and doc generator use;
// they never see the handler binding. A drift test (docs_test.go) proves this
// mirrors stepDefs exactly, so the generated reference cannot diverge from what
// the suite actually registers.
func StepDocs() []StepDoc {
	out := make([]StepDoc, 0, len(stepDefs))
	for _, sd := range stepDefs {
		out = append(out, StepDoc{
			Group:   sd.group,
			Pattern: sd.pattern,
			Summary: sd.summary,
			Example: sd.example,
		})
	}
	return out
}

// stepRegistrar is the minimal seam registerSteps needs from a godog
// ScenarioContext. *godog.ScenarioContext satisfies it, and tests pass a spy to
// observe the live registration without a running suite.
type stepRegistrar interface {
	Step(expr, stepFunc any)
}

var _ stepRegistrar = (*godog.ScenarioContext)(nil)

// registerSteps binds every step in the metadata table to reg, resolving each
// handler selector against w. This is the sole registration path: the composition
// closure delegates to it, and the drift test drives it with a spy.
func registerSteps(reg stepRegistrar, w *world) {
	for _, sd := range stepDefs {
		reg.Step(sd.pattern, sd.handler(w))
	}
}

// stepDefs is the metadata table: every registered step, its documentation, and
// its handler binding. Order mirrors the grammar grouping (drive → sequence →
// budgets → result → aggregate → shape), and the group field names each section.
var stepDefs = []stepDef{
	// --- drive ---
	{
		group:   "Drive",
		pattern: `^the (?:agent|service) target "([^"]+)"$`,
		summary: "Selects the agent or service target that subsequent When steps drive.",
		example: `Given the agent target "researchbot"`,
		handler: func(w *world) any { return w.target_ },
	},
	{
		group:   "Drive",
		pattern: `^I run scenario "([^"]+)"$`,
		summary: "Drives the target with a named scenario argument.",
		example: `When I run scenario "quarterly-report"`,
		handler: func(w *world) any { return w.runScenario },
	},
	{
		group:   "Drive",
		pattern: `^I run the agent with prompt "([^"]*)"$`,
		summary: "Drives the target with a free-text prompt argument.",
		example: `When I run the agent with prompt "Summarize Q3 revenue"`,
		handler: func(w *world) any { return w.runPrompt },
	},
	{
		group:   "Drive",
		pattern: `^I send the request with body:$`,
		summary: "Drives an HTTP target, sending the docstring as the request body.",
		example: "When I send the request with body:\n  \"\"\"\n  {\"amount\": 42}\n  \"\"\"",
		handler: func(w *world) any { return w.sendRequestBodyDoc },
	},
	{
		group:   "Drive",
		pattern: `^I send the request with body fixture "([^"]+)"$`,
		summary: "Drives an HTTP target, sending a fixture file (relative to the feature dir, or absolute) as the request body.",
		example: `When I send the request with body fixture "bodies/order.json"`,
		handler: func(w *world) any { return w.sendRequestBodyFixture },
	},

	// --- sequence ---
	{
		group:   "Sequence",
		pattern: `^the agent calls tools in order:$`,
		summary: "Asserts the agent invoked the listed tools in the given relative order.",
		example: "Then the agent calls tools in order:\n  | search    |\n  | summarize |",
		handler: func(w *world) any { return w.toolsInOrder },
	},
	{
		group:   "Sequence",
		pattern: `^the tool "([^"]+)" is never called$`,
		summary: "Asserts the named tool is never invoked during the run.",
		example: `Then the tool "delete_record" is never called`,
		handler: func(w *world) any { return w.toolNeverCalled },
	},
	{
		group:   "Sequence",
		pattern: `^the tool "([^"]+)" is called at most (\d+) times$`,
		summary: "Bounds how many times the named tool may be invoked.",
		example: `Then the tool "search" is called at most 3 times`,
		handler: func(w *world) any { return w.toolCalledAtMost },
	},
	{
		group:   "Sequence",
		pattern: `^the services are called in order:$`,
		summary: "Asserts the listed services were called in the given relative order.",
		example: "Then the services are called in order:\n  | gateway |\n  | billing |",
		handler: func(w *world) any { return w.servicesInOrder },
	},
	{
		group:   "Sequence",
		pattern: `^the service "([^"]+)" is never called$`,
		summary: "Asserts the named service is never called during the run.",
		example: `Then the service "audit" is never called`,
		handler: func(w *world) any { return w.serviceNeverCalled },
	},

	// --- budgets ---
	{
		group:   "Budgets",
		pattern: `^total tokens are under (\d+)$`,
		summary: "Asserts the run's total token usage is below the given ceiling.",
		example: `Then total tokens are under 5000`,
		handler: func(w *world) any { return w.tokensUnder },
	},
	{
		group:   "Budgets",
		pattern: `^total cost is under ([0-9.]+) USD$`,
		summary: "Asserts the run's total cost is below the given USD ceiling.",
		example: `Then total cost is under 0.05 USD`,
		handler: func(w *world) any { return w.costUnder },
	},
	{
		group:   "Budgets",
		pattern: `^total latency is under (\d+) ms$`,
		summary: "Asserts the run's wall-clock latency is below the given millisecond ceiling.",
		example: `Then total latency is under 2000 ms`,
		handler: func(w *world) any { return w.latencyUnder },
	},
	{
		group:   "Budgets",
		pattern: `^no span has status "ERROR"$`,
		summary: "Asserts no span in the run's trace forest carries an ERROR status.",
		example: `Then no span has status "ERROR"`,
		handler: func(w *world) any { return w.noErrorSpans },
	},

	// --- result ---
	{
		group:   "Result",
		pattern: `^the result contains "([^"]*)"$`,
		summary: "Asserts the driver output contains the given substring.",
		example: `Then the result contains "revenue"`,
		handler: func(w *world) any { return w.resultContains },
	},
	{
		group:   "Result",
		pattern: `^the result equals "([^"]*)"$`,
		summary: "Asserts the driver output equals the given value exactly.",
		example: `Then the result equals "42"`,
		handler: func(w *world) any { return w.resultEquals },
	},
	{
		group:   "Result",
		pattern: `^the response status is (\d+)$`,
		summary: "Asserts the response status code equals the given value.",
		example: `Then the response status is 200`,
		handler: func(w *world) any { return w.responseStatus },
	},
	{
		group:   "Result",
		pattern: `^the result matches regex "([^"]*)"$`,
		summary: "Asserts the driver output matches the given regular expression.",
		example: `Then the result matches regex "^\d+ items$"`,
		handler: func(w *world) any { return w.resultMatchesRegex },
	},
	{
		group:   "Result",
		pattern: `^the result means "([^"]*)"$`,
		summary: "Semantic (judge) assertion that the driver output means the given claim.",
		example: `Then the result means "the request was approved"`,
		handler: func(w *world) any { return w.resultMeans },
	},
	{
		group:   "Result",
		pattern: `^the result means:$`,
		summary: "Semantic (judge) assertion using a docstring meaning.",
		example: "Then the result means:\n  \"\"\"\n  the request was approved\n  \"\"\"",
		handler: func(w *world) any { return w.resultMeansDoc },
	},
	{
		group:   "Result",
		pattern: `^the response body json-contains:$`,
		summary: "Asserts the response body JSON contains the given subset.",
		example: "Then the response body json-contains:\n  \"\"\"\n  {\"status\": \"ok\"}\n  \"\"\"",
		handler: func(w *world) any { return w.responseBodyJSONContains },
	},
	{
		group:   "Result",
		pattern: `^the response body matches schema:$`,
		summary: "Asserts the response body validates against the given JSON schema.",
		example: "Then the response body matches schema:\n  \"\"\"\n  {\"type\": \"object\", \"required\": [\"id\"]}\n  \"\"\"",
		handler: func(w *world) any { return w.responseBodyMatchesSchema },
	},
	{
		group:   "Result",
		pattern: `^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (contains|equals|matches regex) "([^"]*)"$`,
		summary: "Asserts a matcher over a tool call's result attribute, with an optional call selector.",
		example: `Then the result of the last call to tool "search" contains "revenue"`,
		handler: func(w *world) any { return w.resultToolValue },
	},
	{
		group:   "Result",
		pattern: `^the result of (?:(the (?:first|last|\d+(?:st|nd|rd|th)) call|every call|any call) to )?tool "([^"]+)" (json-contains|matches schema):$`,
		summary: "Asserts a docstring matcher over a tool call's result attribute, with an optional call selector.",
		example: "Then the result of tool \"search\" json-contains:\n  \"\"\"\n  {\"count\": 3}\n  \"\"\"",
		handler: func(w *world) any { return w.resultToolDoc },
	},
	{
		group:   "Result",
		pattern: `^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?(?:the )?span matching "([^"]+)" (contains|equals|matches regex) "([^"]*)"$`,
		summary: "Asserts a matcher over a named attribute of a selected span, with an optional span selector.",
		example: `Then attribute "http.status_code" of the first span matching "service.name=billing" equals "200"`,
		handler: func(w *world) any { return w.resultAttrValue },
	},
	{
		group:   "Result",
		pattern: `^attribute "([^"]+)" of (?:(the (?:first|last|\d+(?:st|nd|rd|th))|every|any) )?(?:the )?span matching "([^"]+)" (json-contains|matches schema):$`,
		summary: "Asserts a docstring matcher over a named attribute of a selected span, with an optional span selector.",
		example: "Then attribute \"http.response.body\" of span matching \"service.name=billing\" json-contains:\n  \"\"\"\n  {\"ok\": true}\n  \"\"\"",
		handler: func(w *world) any { return w.resultAttrDoc },
	},

	// --- aggregate / cel ---
	{
		group:   "Aggregate / CEL",
		pattern: `^the run satisfies "([^"]*)"$`,
		summary: "Asserts the run satisfies the given inline CEL expression.",
		example: `Then the run satisfies "tokens < 5000"`,
		handler: func(w *world) any { return w.runSatisfies },
	},
	{
		group:   "Aggregate / CEL",
		pattern: `^the run satisfies:$`,
		summary: "Asserts the run satisfies the given docstring CEL expression.",
		example: "Then the run satisfies:\n  \"\"\"\n  tokens < 5000\n  \"\"\"",
		handler: func(w *world) any { return w.runSatisfiesDoc },
	},
	{
		group:   "Aggregate / CEL",
		pattern: `^the runs satisfy "([^"]*)"$`,
		summary: "Asserts all runs of a @runs(N) scenario satisfy the given inline CEL expression.",
		example: `Then the runs satisfy "runs.all(r, !r.failed)"`,
		handler: func(w *world) any { return w.runsSatisfies },
	},
	{
		group:   "Aggregate / CEL",
		pattern: `^the runs satisfy:$`,
		summary: "Asserts all runs of a @runs(N) scenario satisfy the given docstring CEL expression.",
		example: "Then the runs satisfy:\n  \"\"\"\n  runs.all(r, !r.failed)\n  \"\"\"",
		handler: func(w *world) any { return w.runsSatisfiesDoc },
	},

	// --- shape ---
	{
		group:   "Shape",
		pattern: `^a span matching "([^"]*)" exists$`,
		summary: "Asserts at least one span in the trace forest matches the selector.",
		example: `Then a span matching "gen_ai.operation.name=execute_tool" exists`,
		handler: func(w *world) any { return w.shapeExists },
	},
	{
		group:   "Shape",
		pattern: `^no span matching "([^"]*)" exists$`,
		summary: "Asserts no span in the trace forest matches the selector.",
		example: `Then no span matching "gen_ai.tool.name=delete_record" exists`,
		handler: func(w *world) any { return w.shapeAbsent },
	},
	{
		group:   "Shape",
		pattern: `^at least (\d+) spans? match(?:es)? "([^"]*)"$`,
		summary: "Asserts at least N spans match the selector.",
		example: `Then at least 2 spans match "gen_ai.operation.name=execute_tool"`,
		handler: func(w *world) any { return w.shapeAtLeast },
	},
	{
		group:   "Shape",
		pattern: `^exactly (\d+) spans? match(?:es)? "([^"]*)"$`,
		summary: "Asserts exactly N spans match the selector.",
		example: `Then exactly 1 span matches "gen_ai.operation.name=invoke_agent"`,
		handler: func(w *world) any { return w.shapeExactly },
	},
	{
		group:   "Shape",
		pattern: `^a span matching "([^"]*)" is a child of a span matching "([^"]*)"$`,
		summary: "Asserts a span matching the subject selector is a direct child of one matching the parent selector.",
		example: `Then a span matching "gen_ai.operation.name=execute_tool" is a child of a span matching "gen_ai.operation.name=invoke_agent"`,
		handler: func(w *world) any { return w.shapeChildOf },
	},
	{
		group:   "Shape",
		pattern: `^a span matching "([^"]*)" is a descendant of a span matching "([^"]*)"$`,
		summary: "Asserts a span matching the subject selector is a descendant of one matching the parent selector.",
		example: `Then a span matching "gen_ai.tool.name=search" is a descendant of a span matching "gen_ai.operation.name=invoke_agent"`,
		handler: func(w *world) any { return w.shapeDescendantOf },
	},
	{
		group:   "Shape",
		pattern: `^a span matching "([^"]*)" has at least (\d+) children matching "([^"]*)"$`,
		summary: "Asserts a parent span has at least N children matching the child selector.",
		example: `Then a span matching "gen_ai.operation.name=invoke_agent" has at least 2 children matching "gen_ai.operation.name=execute_tool"`,
		handler: func(w *world) any { return w.shapeFanoutAtLeast },
	},
	{
		group:   "Shape",
		pattern: `^a span matching "([^"]*)" has exactly (\d+) children matching "([^"]*)"$`,
		summary: "Asserts a parent span has exactly N children matching the child selector.",
		example: `Then a span matching "gen_ai.operation.name=invoke_agent" has exactly 3 children matching "gen_ai.operation.name=execute_tool"`,
		handler: func(w *world) any { return w.shapeFanoutExactly },
	},
	{
		group:   "Shape",
		pattern: `^the run matches shape "([^"]*)"$`,
		summary: "Asserts the run matches a named shape pattern loaded from the expectations dir.",
		example: `Then the run matches shape "research-flow"`,
		handler: func(w *world) any { return w.matchesShape },
	},
}
