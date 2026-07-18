package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thetonymaster/mentat"
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

// runMain is the CLI as "consumer zero" (spec 007, R7): a thin caller of the public
// mentat.Run entry point. Every composition/reporting/budget/pricing concern lives in
// mentat.Run, so there is exactly ONE composition path. The CLI keeps only the process
// concerns the library deliberately leaves out — flag parsing, signal handling, and
// mapping Results onto an os.Exit code. Its observable behaviour (byte-stable happy-path
// stdout — SC-005; reports written with the interrupted marker; exit 130/1/0 precedence)
// is preserved by mentat.Run + Results.ExitCode.
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

	cfg, err := mentat.LoadConfig(*cfgPath)
	if err != nil {
		// LoadConfig already prefixes its errors with "mentat: "; print as-is so the
		// operator sees a single prefix, not a doubled "mentat: mentat:".
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Signal handling (feature 003, FR-006) is a process concern kept in main: the first
	// SIGINT/SIGTERM cancels sigCtx (which mentat.Run honours via godog's DefaultContext),
	// so in-flight work stops and every configured report is still written with the
	// interrupted marker; a second signal restores the default handler and force-quits.
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		stop() // a second signal now terminates the process by default
	}()

	// Assemble the run over the public option surface. Verbosity always maps to a stderr
	// handler; the no-flag default is a discard handler so the happy path stays
	// byte-identical (SC-005). Reports are collected into a name->path map (json/html/junit)
	// and passed only when at least one target is set — no WithReports means no files.
	opts := []mentat.Option{
		mentat.WithFeatures(paths...),
		mentat.WithOutput(os.Stdout),
		mentat.WithConcurrency(*concurrency),
		mentat.WithTags(*tags),
		mentat.WithFailFast(*failFast),
		mentat.WithVerbosity(os.Stderr, *verbose, *debug),
	}
	reports := map[string]string{}
	if *reportJSON != "" {
		reports["json"] = *reportJSON
	}
	if *reportHTML != "" {
		reports["html"] = *reportHTML
	}
	if *junit != "" {
		reports["junit"] = *junit
	}
	if len(reports) > 0 {
		opts = append(opts, mentat.WithReports(reports))
	}

	// Map Results onto the process exit code with the same precedence the CLI has always
	// used: interrupted wins (130), else any failed scenario (1), else 0. A harness/emit
	// error from Run is printed and forces a non-zero exit when the suite itself was green.
	res, runErr := mentat.Run(sigCtx, cfg, opts...)
	code := res.ExitCode()
	if runErr != nil {
		// Run already prefixes its errors with "mentat: "; print as-is so the operator
		// sees a single prefix, not a doubled "mentat: mentat:".
		fmt.Fprintln(os.Stderr, runErr)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
