package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v26"
	messages "github.com/cucumber/messages/go/v21"
	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/expectations"
	"github.com/thetonymaster/mentat/internal/steps"
)

// validateCmd is the pure seam behind `mentat validate`: it parses flags, runs
// every authoring precheck STATICALLY over the feature corpus, and renders the
// findings. It never drives a SUT or contacts a store/judge — it constructs no
// store, driver, or correlator at all, so a network call is impossible by
// construction. It returns (exitCode, err): findings → exit 1, clean corpus →
// exit 0, a bad flag/format → (2, err). main() maps this to os.Exit.
func validateCmd(args []string, stdout io.Writer) (int, error) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "mentat.yaml", "config file")
	format := fs.String("format", "text", "output format: text (human-readable) or json")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *format != "text" && *format != "json" {
		return 2, fmt.Errorf("validate: unknown --format %q (want text or json)", *format)
	}
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"features"}
	}

	findings := runValidate(*cfgPath, paths)

	switch *format {
	case "json":
		if err := renderJSON(stdout, findings); err != nil {
			return 2, fmt.Errorf("validate: render json: %w", err)
		}
	default:
		if err := renderText(stdout, findings); err != nil {
			return 2, fmt.Errorf("validate: render text: %w", err)
		}
	}

	if len(findings) > 0 {
		return 1, nil
	}
	return 0, nil
}

// runValidate collects EVERY finding across the corpus — it never stops at the
// first. Config and expectations load tolerantly: a failure in either is itself a
// reported finding (No Silent Fallbacks) and the remaining checks continue with
// what could be resolved.
func runValidate(cfgPath string, paths []string) []steps.Finding {
	var findings []steps.Finding

	known := map[string]bool{}
	expDir := ""
	if data, err := os.ReadFile(cfgPath); err != nil {
		findings = append(findings, steps.Finding{File: cfgPath, Class: "config", Message: err.Error()})
	} else if cfg, cerr := config.Load(data); cerr != nil {
		findings = append(findings, steps.Finding{File: cfgPath, Class: "config", Message: cerr.Error()})
	} else {
		for name := range cfg.Targets {
			known[name] = true
		}
		expDir = cfg.Expectations
	}

	pats, perr := expectations.Load(expDir)
	if perr != nil {
		findings = append(findings, steps.Finding{File: expDir, Class: "expectations", Message: perr.Error()})
		pats = expectations.Patterns{}
	}

	// The checker satisfies steps.PrecheckEngine with real cel/aggregate-cel
	// comparators (constructed directly — no registry, no store, no driver) and the
	// tolerantly-loaded shape patterns. This is the exact interface the scenario-init
	// prechecks consume, so validate reuses their logic verbatim.
	chk := checker{cel: comparator.NewCEL(nil), agg: comparator.NewAggregateCEL(nil), pats: pats}

	files, pathFindings := featureFiles(paths)
	findings = append(findings, pathFindings...)
	if len(files) == 0 {
		// An empty suite is a mistake, not a pass (No Silent Fallbacks).
		findings = append(findings, steps.Finding{
			File:    strings.Join(paths, ", "),
			Class:   "no-features",
			Message: fmt.Sprintf("no .feature files found under %s", strings.Join(paths, ", ")),
		})
		return dedupeSort(findings)
	}

	for _, f := range files {
		findings = append(findings, checkFeature(f, known, chk)...)
	}
	return dedupeSort(findings)
}

// checker is validate's steps.PrecheckEngine: real comparators for CEL
// precompilation and the loaded patterns for shape resolution. No network seam.
type checker struct {
	cel  core.Comparator
	agg  core.AggregateComparator
	pats expectations.Patterns
}

func (c checker) Comparator(name string) (core.Comparator, bool) {
	if name == "cel" && c.cel != nil {
		return c.cel, true
	}
	return nil, false
}

func (c checker) AggregateComparator(name string) (core.AggregateComparator, bool) {
	if name == "aggregate-cel" && c.agg != nil {
		return c.agg, true
	}
	return nil, false
}

func (c checker) ShapePattern(name string) ([]comparator.ShapeExpectation, bool) {
	return c.pats.Get(name)
}

// checkFeature parses one feature file, generates its pickles, and runs every
// precheck against each pickle — resolving each finding's source line via the AST.
func checkFeature(path string, known map[string]bool, chk checker) []steps.Finding {
	data, err := os.ReadFile(path)
	if err != nil {
		return []steps.Finding{{File: path, Class: "read", Message: err.Error()}}
	}
	gen := newIDGen()
	doc, err := gherkin.ParseGherkinDocument(bytes.NewReader(data), gen)
	if err != nil {
		// A malformed feature is a hard finding, never a silent skip.
		return []steps.Finding{{File: path, Class: "parse", Message: err.Error()}}
	}
	lm := lineMap(doc)
	src := steps.Source{File: path, Line: func(id string) int { return lm[id] }}

	var out []steps.Finding
	for _, pk := range gherkin.Pickles(*doc, path, gen) {
		out = append(out, steps.RunsTagFindings(pk.Tags, src)...)
		out = append(out, steps.StepBindingFindings(pk.Steps, src)...)
		out = append(out, steps.TargetFindings(known, pk.Steps, src)...)
		out = append(out, steps.CELFindings(chk, pk.Steps, src)...)
		out = append(out, steps.ShapePatternFindings(chk, pk.Steps, src)...)
	}
	return out
}

// featureFiles resolves the given paths (dirs walked recursively, files taken
// as-is) into a sorted, de-duplicated list of *.feature files. A path that cannot
// be stat'd is itself a finding — never silently ignored.
func featureFiles(paths []string) ([]string, []steps.Finding) {
	var files []string
	var findings []steps.Finding
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
			findings = append(findings, steps.Finding{File: p, Class: "path", Message: err.Error()})
			continue
		}
		if !info.IsDir() {
			add(p)
			continue
		}
		_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if !d.IsDir() {
				add(path)
			}
			return nil
		})
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

// dedupeSort removes identical findings (a scenario outline expands to one pickle
// per row, which would otherwise duplicate line-identical findings) and orders them
// deterministically by file, line, class, then message.
func dedupeSort(fs []steps.Finding) []steps.Finding {
	seen := map[steps.Finding]bool{}
	out := make([]steps.Finding, 0, len(fs))
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

func renderText(w io.Writer, fs []steps.Finding) error {
	if len(fs) == 0 {
		_, err := fmt.Fprintln(w, "validate: no issues found")
		return err
	}
	for _, f := range fs {
		if _, err := fmt.Fprintf(w, "%s:%d: [%s] %s\n", f.File, f.Line, f.Class, f.Message); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "validate: %d issue(s) found\n", len(fs))
	return err
}

func renderJSON(w io.Writer, fs []steps.Finding) error {
	if fs == nil {
		fs = []steps.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Findings []steps.Finding `json:"findings"`
	}{Findings: fs})
}
