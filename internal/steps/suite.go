package steps

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v26"
	messages "github.com/cucumber/messages/go/v21"
)

// SuiteCheck runs every authoring precheck over a corpus of .feature files.
//
// It lives here, beside the checks themselves, so there is exactly ONE implementation
// of "walk the suite and collect findings". Two consumers share it:
//
//   - `mentat validate`, the CLI, which builds a lightweight checker with the two CEL
//     comparators and the BUILT-IN step patterns, and nothing else.
//   - the library validate entry point, which builds the same engine a run would use
//     and therefore also sees comparator-contributed phrases.
//
// The difference between them is the DATA each supplies, not the logic. That is what
// keeps a contributed phrase from being reported as an unbound step by one path and
// accepted by the other for reasons nobody can reconstruct.
type SuiteCheck struct {
	// Engine supplies comparators and shape patterns for the CEL and shape checks.
	Engine PrecheckEngine
	// Patterns is the step-pattern set feature text is bound against — built-ins
	// alone for a compiled binary, built-ins plus contributed phrases for an
	// engine-aware caller.
	Patterns StepPatterns
	// Targets are the configured target names. CheckTargets is separate from
	// len(Targets) > 0 because "config failed to load" and "config declares no
	// targets" must not produce the same findings: the first would report every
	// reference as unknown, which is a lie about the feature file.
	Targets      map[string]bool
	CheckTargets bool
	// CheckShapes is likewise false when the expectations dir could not be read, so
	// an unavailable source is never mistaken for "every reference is unknown".
	CheckShapes bool
	// Arguments carries the step-argument expectations this check enforces: every
	// built-in row's, derived from its handler signature, plus any contributed phrases
	// the engine resolved.
	//
	// A compiled binary cannot see a consumer's phrases, so it supplies
	// BuiltinStepArguments() — the built-in half is exactly what it CAN check, and that
	// half needs no comparator to reach. Its zero value checks nothing, which is only
	// correct for a caller that has deliberately opted out.
	//
	// It exists so a statically-validated suite and a run agree. Without it Validate
	// reported CLEAN on a feature file that Run rejects at scenario init — the two
	// answering differently about the same suite is the drift this whole surface is
	// supposed to prevent.
	//
	// The agreement is one-directional by design: this walks EVERY scenario in the
	// corpus, including ones a run's tag expression would skip, so an argument defect
	// cannot hide behind a tag filter until the day someone runs that tag.
	//
	// That breadth claim is about TAGS and nothing more. A step matching both a built-in
	// and a contributed phrase is still skipped by THIS check, and correctly (see
	// StepArguments.stepProblem: under Strict neither definition binds, so naming an
	// argument the step never reads would send the author somewhere the defect is not).
	// It is no longer skipped by the SuiteCheck: StepBindingFindings classifies it as
	// `ambiguous-step` over Patterns, which is the finding Run's own ambiguity failure
	// corresponds to. The two answer the same way about the same suite.
	//
	// What neither of these answers is regex OVERLAP in the abstract. Both classify per
	// sentence, so a collision on a sentence no scenario in the corpus contains is
	// invisible to both.
	//
	// Something else does answer it now. Feature 013 added Intersects (disjoint.go),
	// which decides emptiness-of-intersection for a pattern pair without any corpus;
	// the built-in set is gated by it in CI, and pairs involving a contributed pattern
	// are reported as `pattern-overlap` by mentat.Validate. An earlier version of this
	// comment said mentat "does not compute it", which was true when written and is the
	// claim 013 was raised to retire.
	Arguments StepArguments
}

// Paths resolves paths (directories walked recursively, files taken as-is) into
// .feature files and checks each, returning deduplicated, deterministically ordered
// findings.
//
// An empty corpus is itself a finding. A validator that reports success over zero
// files tells you nothing and looks exactly like success.
func (s SuiteCheck) Paths(paths []string) []Finding {
	files, findings := featureFiles(paths)
	if len(files) == 0 {
		findings = append(findings, Finding{
			File:    strings.Join(paths, ", "),
			Class:   "no-features",
			Message: "no .feature files found under " + strings.Join(paths, ", "),
		})
		return DedupeSortFindings(findings)
	}
	for _, f := range files {
		findings = append(findings, s.Feature(f)...)
	}
	return DedupeSortFindings(findings)
}

// Feature parses one feature file, generates its pickles, and runs every precheck
// against each — resolving each finding's source line through the AST.
func (s SuiteCheck) Feature(path string) []Finding {
	data, err := os.ReadFile(path)
	if err != nil {
		return []Finding{{File: path, Class: "read", Message: err.Error()}}
	}
	gen := newIDGen()
	doc, err := gherkin.ParseGherkinDocument(bytes.NewReader(data), gen)
	if err != nil {
		// A malformed feature is a hard finding, never a silent skip.
		return []Finding{{File: path, Class: "parse", Message: err.Error()}}
	}
	lm := lineMap(doc)
	src := Source{File: path, Line: func(id string) int { return lm[id] }}

	var out []Finding
	for _, pk := range gherkin.Pickles(*doc, path, gen) {
		out = append(out, RunsTagFindings(pk.Tags, src)...)
		out = append(out, StepBindingFindings(s.Patterns, pk.Steps, src)...)
		if s.CheckTargets {
			out = append(out, TargetFindings(s.Targets, pk.Steps, src)...)
		}
		out = append(out, s.Arguments.Findings(pk.Steps, src)...)
		out = append(out, CELFindings(s.Engine, pk.Steps, src)...)
		if s.CheckShapes {
			out = append(out, ShapePatternFindings(s.Engine, pk.Steps, src)...)
		}
	}
	return out
}

// featureFiles resolves paths into a sorted, de-duplicated list of *.feature files.
// A path that cannot be stat'd is itself a finding — never silently ignored.
func featureFiles(paths []string) ([]string, []Finding) {
	var files []string
	var findings []Finding
	seen := map[string]bool{}
	add := func(p string) {
		if strings.HasSuffix(p, ".feature") && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			findings = append(findings, Finding{File: p, Class: "path", Message: err.Error()})
			continue
		}
		if !info.IsDir() {
			add(p)
			continue
		}
		walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, werr error) error {
			// An unreadable subtree is a reported finding, never a silent omission:
			// record it and keep walking siblings (return nil, not the error).
			if werr != nil {
				findings = append(findings, Finding{File: path, Class: "path", Message: werr.Error()})
				return nil
			}
			if !d.IsDir() {
				add(path)
			}
			return nil
		})
		if walkErr != nil {
			findings = append(findings, Finding{File: p, Class: "path", Message: walkErr.Error()})
		}
	}
	sort.Strings(files)
	return files, findings
}

// lineMap maps each AST node id (Step/Tag) to its 1-based source line, so a pickle
// step or tag can be located back to the feature file line it came from.
func lineMap(doc *messages.GherkinDocument) map[string]int {
	m := map[string]int{}
	if doc.Feature == nil {
		return m
	}
	addSteps := func(ss []*messages.Step) {
		for _, s := range ss {
			if s.Location != nil {
				m[s.Id] = int(s.Location.Line)
			}
		}
	}
	addTags := func(ts []*messages.Tag) {
		for _, tg := range ts {
			if tg.Location != nil {
				m[tg.Id] = int(tg.Location.Line)
			}
		}
	}
	addTags(doc.Feature.Tags)
	for _, ch := range doc.Feature.Children {
		if ch.Background != nil {
			addSteps(ch.Background.Steps)
		}
		if ch.Scenario != nil {
			addTags(ch.Scenario.Tags)
			addSteps(ch.Scenario.Steps)
		}
		if ch.Rule != nil {
			addTags(ch.Rule.Tags)
			for _, rc := range ch.Rule.Children {
				if rc.Background != nil {
					addSteps(rc.Background.Steps)
				}
				if rc.Scenario != nil {
					addTags(rc.Scenario.Tags)
					addSteps(rc.Scenario.Steps)
				}
			}
		}
	}
	return m
}

// newIDGen returns a fresh monotonic id source; sharing one instance across
// ParseGherkinDocument and Pickles keeps AST node ids and pickle ids collision-free
// so pickle AstNodeIds resolve back into lineMap.
func newIDGen() func() string {
	var n int
	return func() string {
		n++
		return strconv.Itoa(n)
	}
}

// DedupeSortFindings removes identical findings (a scenario outline expands to one
// pickle per row, which would otherwise duplicate line-identical findings) and orders
// them deterministically by file, line, class, then message.
func DedupeSortFindings(fs []Finding) []Finding {
	seen := map[Finding]bool{}
	out := make([]Finding, 0, len(fs))
	for _, f := range fs {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Class != b.Class:
			return a.Class < b.Class
		default:
			return a.Message < b.Message
		}
	})
	return out
}
