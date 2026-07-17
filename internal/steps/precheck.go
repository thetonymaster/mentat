package steps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

var (
	stepPatternsOnce sync.Once
	stepPatterns     []*regexp.Regexp
)

// compiledStepPatterns compiles every registered step pattern once. The patterns
// come from the same stepDefs metadata table godog registers, so a step that
// binds no pattern here binds none at runtime either (StepBindingFindings).
func compiledStepPatterns() []*regexp.Regexp {
	stepPatternsOnce.Do(func() {
		for _, d := range StepDocs() {
			stepPatterns = append(stepPatterns, regexp.MustCompile(d.Pattern))
		}
	})
	return stepPatterns
}

// StepBindingFindings reports every pickle step whose text matches no registered
// step pattern — the static equivalent of godog's runtime "undefined step".
func StepBindingFindings(steps []*messages.PickleStep, src Source) []Finding {
	pats := compiledStepPatterns()
	var out []Finding
	for _, st := range steps {
		bound := false
		for _, re := range pats {
			if re.MatchString(st.Text) {
				bound = true
				break
			}
		}
		if !bound {
			out = append(out, stepFinding(src, st, "unbound-step", fmt.Sprintf("no step matches %q", st.Text)))
		}
	}
	return out
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
