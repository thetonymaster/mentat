package steps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
)

// Finding is one located authoring defect: which file and line, its class (a
// stable machine key such as "bad-cel" or "unknown-target"), and a human message.
// It is the shared currency of the scenario-init prechecks and `mentat validate`
// — the prechecks RETURN findings (collect-all) rather than stopping at the first,
// so the same logic serves both the fail-fast Before hook (which surfaces the
// first finding as the scenario-init error) and the static validator (which
// aggregates every finding across the corpus).
type Finding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Class   string `json:"class"`
	Message string `json:"message"`
}

// Source locates findings within a feature file. `mentat validate` populates it
// from the parsed GherkinDocument (mapping each pickle step/tag's AST node id to
// its source line); the scenario-init hook leaves it zero (File "", Line nil),
// because a runtime scenario failure is already located by scenario name and the
// line is irrelevant there.
type Source struct {
	File string
	// Line maps an AST node id to its 1-based source line, or nil when lines are
	// unavailable (scenario-init).
	Line func(nodeID string) int
}

func (s Source) lineOf(nodeIDs []string) int {
	if s.Line == nil || len(nodeIDs) == 0 {
		return 0
	}
	return s.Line(nodeIDs[0])
}

func (s Source) tagLine(nodeID string) int {
	if s.Line == nil {
		return 0
	}
	return s.Line(nodeID)
}

func stepFinding(src Source, st *messages.PickleStep, class, msg string) Finding {
	return Finding{File: src.File, Line: src.lineOf(st.AstNodeIds), Class: class, Message: msg}
}

// PrecheckEngine is the engine subset the authoring prechecks need: the two CEL
// comparators (for precompilation) and the loaded shape patterns. Defined by the
// consumer so both *engine.Engine (scenario-init) and validate's own lightweight
// checker satisfy it — comparators never see a store or driver (invariant #1).
type PrecheckEngine interface {
	Comparator(name string) (core.Comparator, bool)
	AggregateComparator(name string) (core.AggregateComparator, bool)
	ShapePattern(name string) ([]comparator.ShapeExpectation, bool)
}

var _ PrecheckEngine = (*engine.Engine)(nil)

// reTarget mirrors the drive target step pattern; validate extracts the referenced
// target name to check it against the configured targets.
var reTarget = regexp.MustCompile(`^the (?:agent|service) target "([^"]+)"$`)

// StepPatterns is the compiled step-pattern set ONE engine binds against: the
// built-in stepDefs rows plus whatever comparator-contributed phrases that engine
// resolved.
//
// It is a value derived from an engine, deliberately NOT package state. This file
// previously held a sync.Once cache (stepPatternsOnce/stepPatterns) that compiled
// the pattern set once per process. That fixes the first-compiled set for every
// later caller, so a second engine would be checked against the first engine's
// patterns and report a valid feature file as broken — the same defect class 007
// closed for registries, and the property US2 exists to prove.
//
// The cache never bit because its only consumer was `mentat validate`, a
// single-shot CLI process. The library validate entry point, which a consumer can
// call repeatedly in one process against different engines, is what makes it live.
type StepPatterns []*regexp.Regexp

// CompileStepPatterns compiles each pattern in order, returning an error that names
// the offending pattern. It returns an error rather than panicking because
// comparator-contributed patterns are author input: a bad regex must surface as a
// descriptive engine-build failure, never a crash (Constitution IV).
func CompileStepPatterns(patterns []string) (StepPatterns, error) {
	out := make(StepPatterns, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("compiling step pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// BuiltinStepPatterns compiles the built-in stepDefs patterns, in table order.
//
// MustCompile is correct here and nowhere else in this file: these are literals in
// the stepDefs table, guarded by the drift tests, so a failure is an unreachable
// invariant violation rather than user input. This preserves exactly the contract
// the deleted cache had — it only stops memoizing it.
func BuiltinStepPatterns() StepPatterns {
	docs := StepDocs()
	out := make(StepPatterns, 0, len(docs))
	for _, d := range docs {
		out = append(out, regexp.MustCompile(d.Pattern))
	}
	return out
}

// StepBindingFindings classifies every pickle step by HOW MANY patterns in pats
// match its text. The count has exactly three answers and each gets one:
//
//	0  -> "unbound-step", the static equivalent of godog's runtime "undefined step"
//	1  -> no finding; that definition is what binds
//	>1 -> "ambiguous-step", naming every matching pattern
//
// One function answers all three because it is one question. They were two checks —
// binding here, ambiguity nowhere — and the gap between them was a validator that
// reported CLEAN on a suite the runner refuses.
//
// # The >1 case is godog's own predicate, not an approximation of it
//
// Measured against the pinned godog v0.15.1:
//
//   - matchStepTextAndType (godog suite.go:511-556) loops every registered definition,
//     tests h.Expr.FindStringSubmatch(text), collects each matching h.Expr.String()
//     into matchingExpressions, and under Strict returns ErrAmbiguous when
//     len(matchingExpressions) > 1. mentat.Run sets Strict (run.go).
//   - the keywordMatches filter inside that loop is INERT for every Mentat step:
//     ScenarioContext.Step registers with formatters.None (godog test_context.go:255-257),
//     which keywordMatches short-circuits to true, and registerSteps (metadata.go:107,110)
//     uses reg.Step for both built-ins and contributed phrases.
//
// So godog's predicate reduces to "how many registered patterns match this sentence",
// and SuiteCheck.Patterns is that same set in the same registration order
// (stepPatternsFor, phrase.go: built-ins then contributed). Same predicate, same
// inputs, same order — which is why the patterns are listed in pattern-set order here
// too, lining up with godog's own matchingExpressions output.
//
// This decides nothing about regex OVERLAP. It is per-sentence multi-match, scoped to
// the sentences the corpus actually contains: two patterns that could collide but that
// no step in the corpus reaches are not reported HERE.
//
// They are no longer reported by nobody, which is what an earlier version of this
// comment said ("neither runner nor validator has an opinion about them"). Feature 013
// added a decision procedure — Intersects, disjoint.go — and two consumers of it: a CI
// gate over the built-in set, and `pattern-overlap` findings for pairs involving a
// contributed pattern, produced in EngineStepChecks and returned by mentat.Validate.
// Those answer the pattern-pair question with no corpus at all. This function still
// answers only the per-sentence one, and the division of labour is deliberate: this
// names a LINE an author wrote, `pattern-overlap` names a PAIR of definitions.
//
// pats is a parameter, not package state, so the answer is always about the engine
// the caller means. See StepPatterns.
func StepBindingFindings(pats StepPatterns, steps []*messages.PickleStep, src Source) []Finding {
	var out []Finding
	for _, st := range steps {
		var matched []string
		for _, re := range pats {
			if re.MatchString(st.Text) {
				matched = append(matched, re.String())
			}
		}
		switch {
		case len(matched) == 0:
			out = append(out, stepFinding(src, st, "unbound-step", fmt.Sprintf("no step matches %q", st.Text)))
		case len(matched) > 1:
			out = append(out, stepFinding(src, st, "ambiguous-step", ambiguousStepMessage(st.Text, matched)))
		}
	}
	return out
}

// ambiguousStepMessage renders one line: findings print one per line
// (cmd/mentat/validate.go renderText), so a multi-line message would break the
// format. Patterns are quoted with strconv.Quote (what %q does for a string), matching
// how argumentProblem quotes step definitions.
func ambiguousStepMessage(text string, matched []string) string {
	quoted := make([]string, 0, len(matched))
	for _, p := range matched {
		quoted = append(quoted, strconv.Quote(p))
	}
	return fmt.Sprintf("step %q matches %d step definitions and the runner refuses it as ambiguous: %s",
		text, len(matched), strings.Join(quoted, ", "))
}

// TargetFindings reports every `the (agent|service) target "X"` step whose X is
// not among the configured targets.
func TargetFindings(known map[string]bool, steps []*messages.PickleStep, src Source) []Finding {
	var out []Finding
	for _, st := range steps {
		m := reTarget.FindStringSubmatch(st.Text)
		if m == nil {
			continue
		}
		if !known[m[1]] {
			out = append(out, stepFinding(src, st, "unknown-target", fmt.Sprintf("unknown target %q (not a configured target)", m[1])))
		}
	}
	return out
}

// CELFindings compiles every "the run satisfies" / "the runs satisfy" expression
// in steps against eng's comparators, returning one finding per uncompilable
// expression. This is the collect-all core precompileScenario delegates to.
func CELFindings(eng PrecheckEngine, steps []*messages.PickleStep, src Source) []Finding {
	var out []Finding
	for _, st := range steps {
		if expr, ok := satisfiesExpr(st); ok {
			if msg := compileRunCEL(eng, expr); msg != "" {
				out = append(out, stepFinding(src, st, "bad-cel", msg))
			}
			continue
		}
		if expr, ok := runsSatisfiesExpr(st); ok {
			if msg := compileRunsCEL(eng, expr); msg != "" {
				out = append(out, stepFinding(src, st, "bad-cel", msg))
			}
		}
	}
	return out
}

// compileRunCEL returns "" when expr compiles against the cel comparator, else a
// clean (scenario-init-prefix-free) diagnostic message. A missing/incapable
// comparator is a hard message, never a silent pass (Constitution IV).
func compileRunCEL(eng PrecheckEngine, expr string) string {
	c, ok := eng.Comparator("cel")
	if !ok {
		return "'the run satisfies' requires the cel comparator, which is not registered"
	}
	pc, ok := c.(interface{ Compile(string) error })
	if !ok {
		return fmt.Sprintf("cel comparator %T does not support pre-compilation", c)
	}
	if err := pc.Compile(expr); err != nil {
		return err.Error()
	}
	return ""
}

// compileRunsCEL mirrors compileRunCEL for the aggregate-cel comparator.
func compileRunsCEL(eng PrecheckEngine, expr string) string {
	c, ok := eng.AggregateComparator("aggregate-cel")
	if !ok {
		return "'the runs satisfy' requires the aggregate-cel comparator, which is not registered"
	}
	pc, ok := c.(interface{ Compile(string) error })
	if !ok {
		return fmt.Sprintf("aggregate comparator %T does not support pre-compilation", c)
	}
	if err := pc.Compile(expr); err != nil {
		return err.Error()
	}
	return ""
}

// ShapePatternFindings reports every `the run matches shape "X"` step whose X was
// not loaded from the expectations dir.
func ShapePatternFindings(eng PrecheckEngine, steps []*messages.PickleStep, src Source) []Finding {
	var out []Finding
	for _, st := range steps {
		m := reMatchesShape.FindStringSubmatch(st.Text)
		if m == nil {
			continue
		}
		if _, ok := eng.ShapePattern(m[1]); !ok {
			out = append(out, stepFinding(src, st, "unknown-shape", fmt.Sprintf("unknown shape pattern %q (no such pattern under the expectations dir)", m[1])))
		}
	}
	return out
}

// RunsTagFindings reports a malformed @runs(...) scenario tag as a finding.
func RunsTagFindings(tags []*messages.PickleTag, src Source) []Finding {
	_, _, tag, msg := parseRunsTagRaw(tags)
	if msg == "" {
		return nil
	}
	line := 0
	if tag != nil {
		line = src.tagLine(tag.AstNodeId)
	}
	return []Finding{{File: src.File, Line: line, Class: "bad-runs-tag", Message: msg}}
}

// parseRunsTagRaw is the shared @runs parser: it returns the resolved (n,parallel)
// and, on a malformed tag, the offending tag plus a clean message (no
// scenario-init prefix). parseRunsTag (steps.go) wraps this for the fail-fast
// Before hook; RunsTagFindings wraps it for collect-all validation.
func parseRunsTagRaw(tags []*messages.PickleTag) (n int, parallel bool, tag *messages.PickleTag, msg string) {
	// Scan ALL tags: a pickle inherits feature/rule/scenario-level tags, so more than
	// one @runs may reach here. A malformed tag is reported as before; two or more VALID
	// @runs tags are ambiguous (Constitution IV: ambiguous input is a hard, descriptive
	// error, never an order-dependent guess). We remember the first valid match and, on
	// seeing a second, return an ambiguity naming both — locating it at the second tag.
	var (
		firstTag *messages.PickleTag
		firstN   int
		firstPar bool
	)
	for _, t := range tags {
		if !strings.HasPrefix(t.Name, "@runs(") {
			continue
		}
		m := reRunsTag.FindStringSubmatch(t.Name)
		if m == nil {
			return 0, false, t, fmt.Sprintf("malformed @runs tag %q (want @runs(N) or @runs(N,parallel))", t.Name)
		}
		nn, err := strconv.Atoi(m[1])
		if err != nil || nn < 1 {
			return 0, false, t, fmt.Sprintf("@runs requires N>=1, got %q", t.Name)
		}
		if firstTag != nil {
			return 0, false, t, fmt.Sprintf("ambiguous @runs tags: %q and %q both set the run count (only one @runs is allowed)", firstTag.Name, t.Name)
		}
		firstTag, firstN, firstPar = t, nn, m[2] == "parallel"
	}
	if firstTag != nil {
		return firstN, firstPar, nil, ""
	}
	return 1, false, nil, ""
}
