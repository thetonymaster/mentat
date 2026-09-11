package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

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
	// The limit is stated in --help rather than left for a user to discover as a
	// wall of false unbound-step findings on a feature file that is actually valid.
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, "usage: mentat validate [paths...] [--config FILE] [--format text|json]\n\n"+
			"Statically checks feature files: step binding, step arguments, target and shape\n"+
			"references, CEL expressions and @runs tags. Drives no SUT and contacts no store.\n\n"+
			"A step carrying an argument its definition cannot receive is reported: the runner\n"+
			"discards such an argument in silence, so the step would assert something you did\n"+
			"not write. This can fail a suite that passed before the check existed.\n\n"+
			"Checks BUILT-IN steps only. Comparator-contributed Gherkin phrases are scoped\n"+
			"to the engine that registered them, and a compiled binary cannot reach a\n"+
			"consumer's Go registrations, so a suite written in contributed phrases will\n"+
			"report them as unbound here. Validate such a suite from your own test binary\n"+
			"via the library entry point, which builds the same engine your run will use.\n\n")
		fs.PrintDefaults()
	}
	format := fs.String("format", "text", "output format: text (human-readable) or json")
	// flag.FlagSet stops parsing at the first positional, so we resume after each
	// path to accept flags interspersed with the positional paths (the documented
	// `validate [paths...] [--config ...] [--format ...]` contract).
	var paths []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return 2, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		paths = append(paths, rest[0])
		rest = rest[1:]
	}
	if *format != "text" && *format != "json" {
		return 2, fmt.Errorf("validate: unknown --format %q (want text or json)", *format)
	}
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
	// configOK/expOK gate the derived checks: an unavailable source is reported once
	// as its own finding, but its check is SKIPPED rather than run against empty data
	// — otherwise one load error balloons into a false unknown-target (or
	// unknown-shape) for every reference in the corpus.
	configOK := true
	if data, err := os.ReadFile(cfgPath); err != nil {
		findings = append(findings, steps.Finding{File: cfgPath, Class: "config", Message: err.Error()})
		configOK = false
	} else if cfg, cerr := config.Load(data); cerr != nil {
		findings = append(findings, steps.Finding{File: cfgPath, Class: "config", Message: cerr.Error()})
		configOK = false
	} else {
		for name := range cfg.Targets {
			known[name] = true
		}
		expDir = cfg.Expectations
	}

	expOK := true
	pats, perr := expectations.Load(expDir)
	if perr != nil {
		findings = append(findings, steps.Finding{File: expDir, Class: "expectations", Message: perr.Error()})
		pats = expectations.Patterns{}
		expOK = false
	}

	// The checker satisfies steps.PrecheckEngine with real cel/aggregate-cel
	// comparators (constructed directly — no registry, no store, no driver) and the
	// tolerantly-loaded shape patterns. This is the exact interface the scenario-init
	// prechecks consume, so validate reuses their logic verbatim.
	// stepPats is compiled once per invocation and threaded through, replacing the
	// package-level sync.Once cache precheck.go used to hold. A compiled binary can
	// only ever see the BUILT-IN patterns: a consumer's WithComparator calls live in
	// their module and cannot reach this process, so contributed phrases are
	// structurally out of reach here. That limit is documented rather than papered
	// over — see the library validate entry point.
	chk := checker{
		cel:      comparator.NewCEL(nil),
		agg:      comparator.NewAggregateCEL(nil),
		pats:     pats,
		stepPats: steps.BuiltinStepPatterns(),
	}

	// One implementation of "walk the suite and collect findings", shared with the
	// library validate entry point. What differs between the two is the DATA: this
	// path supplies BUILT-IN step patterns only, because a compiled binary cannot see
	// a consumer's Go registrations. See the note on stepPats above.
	pre := findings
	out := steps.SuiteCheck{
		Engine:       chk,
		Patterns:     chk.stepPats,
		Targets:      known,
		CheckTargets: configOK,
		CheckShapes:  expOK,
		// The built-in half of the step-argument check. A binary cannot see a
		// consumer's contributed phrases, but a surplus docstring on a BUILT-IN step
		// needs no comparator to write and is discarded silently by the runner, so
		// this is a defect the binary is fully equipped to catch.
		//
		// Guarded by the step-argument row in seedDefectCorpus: deleting this line
		// reddens TestValidateCollectsAllFindings. Verified by mutation, because
		// `make ci` exempts cmd/* from the coverage gate and would not have noticed.
		Arguments: steps.BuiltinStepArguments(),
	}.Paths(paths)
	// Config/expectations findings gathered before the walk are folded in and
	// re-sorted, so the output stays one deterministically ordered list.
	return steps.DedupeSortFindings(append(pre, out...))
}

// checker is validate's steps.PrecheckEngine: real comparators for CEL
// precompilation and the loaded patterns for shape resolution. No network seam.
type checker struct {
	cel  core.Comparator
	agg  core.AggregateComparator
	pats expectations.Patterns
	// stepPats is the compiled step-pattern set this invocation binds against —
	// built-ins only, for the reason given where it is constructed. It is carried
	// here so it is compiled once per run rather than once per feature file, and so
	// the set is an explicit input to the checks rather than package state.
	stepPats steps.StepPatterns
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
