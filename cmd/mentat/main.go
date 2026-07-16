package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cucumber/godog"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/report"
	"github.com/thetonymaster/mentat/internal/steps"
)

// main dispatches to a subcommand. `run` drives behaviour scenarios (its flow is
// byte-stable — SC-005); `steps` prints/generates the step reference. Unknown or
// missing subcommands print usage and exit 2, matching the pre-subcommand CLI.
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		runMain(os.Args[2:])
	case "steps":
		if err := stepsCmd(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "mentat:", err)
			os.Exit(1)
		}
	case "validate":
		code, err := validateCmd(os.Args[2:], os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mentat:", err)
		}
		os.Exit(code)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mentat <command> [flags]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  run      [paths...] [flags]        run behaviour scenarios")
	fmt.Fprintln(os.Stderr, "  steps    [--format md|text]        print the step reference")
	fmt.Fprintln(os.Stderr, "  validate [paths...] [--format ...]  statically check the feature corpus")
}

// runMain is the unchanged `mentat run` flow (feature 003/005): its stdout is the
// byte-stable happy path (SC-005), so this body must stay identical to the
// pre-subcommand version — only its arg source moved from os.Args[2:] to args.
func runMain(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "mentat.yaml", "config file")
	concurrency := fs.Int("concurrency", 1, "scenario scheduler width")
	tags := fs.String("tags", "", "godog tag expression")
	junit := fs.String("junit", "", "write JUnit XML to this file")
	reportJSON := fs.String("report-json", "", "write a JSON run report to this file")
	reportHTML := fs.String("report-html", "", "write an HTML run report to this file")
	failFast := fs.Bool("fail-fast", false, "stop on first failure")
	verbose := fs.Bool("v", false, "narrate the run at Info level to stderr")
	debug := fs.Bool("vv", false, "narrate the run at Debug level to stderr (implies -v)")
	_ = fs.Parse(args)
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"features"}
	}

	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}
	cfg, err := config.Load(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}

	// Verbosity flags map to a stderr slog handler; the no-flag default is a
	// discard handler so the happy path stays byte-identical (SC-005). The logger
	// (and the store endpoint) are injected into the seams here at the composition
	// root — no package-global logger, no slog.SetDefault.
	logger := engine.NewLogger(os.Stderr, *verbose, *debug)

	cor, err := engine.BuildCorrelator(cfg, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}
	st, err := engine.BuildStore(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}
	eng, err := engine.Build(cfg, st, cor, engine.WithLogger(logger))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}

	// Signal handling (feature 003, FR-006): the first SIGINT/SIGTERM cancels the
	// suite context so in-flight work stops and every configured report is still
	// written; a second signal restores the default handler and force-quits.
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		stop() // a second signal now terminates the process by default
	}()

	opts := godog.Options{
		Format:         "pretty",
		Paths:          paths,
		Tags:           *tags,
		Concurrency:    *concurrency,
		Output:         os.Stdout,
		StopOnFailure:  *failFast,
		DefaultContext: sigCtx,
	}

	col := report.NewCollector()
	suite := godog.TestSuite{ScenarioInitializer: steps.InitializerWithCollector(eng, col), Options: &opts}
	started := time.Now()
	code := suite.Run()
	interrupted := sigCtx.Err() != nil

	// Always emit the configured reports — the scenarios that completed plus the
	// interrupted marker — written atomically (temp+rename) so a signal arriving
	// mid-write never leaves a truncated file. JUnit is emitted here from the
	// collector too (not godog's format), so it carries the interrupted property.
	targets := map[string]string{}
	if *reportJSON != "" {
		targets["json"] = *reportJSON
	}
	if *reportHTML != "" {
		targets["html"] = *reportHTML
	}
	if *junit != "" {
		targets["junit"] = *junit
	}
	if len(targets) > 0 {
		if err := report.EmitReports(col.Report(started, time.Since(started), interrupted), targets); err != nil {
			fmt.Fprintln(os.Stderr, "mentat:", err)
			if code == 0 {
				code = 1
			}
		}
	}

	if interrupted {
		// 128 + SIGINT(2) = 130, the conventional "interrupted" exit code — distinct
		// from a plain assertion failure (1) so CI can tell cancellation from a red suite.
		os.Exit(130)
	}
	os.Exit(code)
}
