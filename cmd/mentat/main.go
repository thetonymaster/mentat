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
	"github.com/google/uuid"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/report"
	"github.com/thetonymaster/mentat/internal/steps"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: mentat run [paths...] [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "mentat.yaml", "config file")
	concurrency := fs.Int("concurrency", 1, "scenario scheduler width")
	tags := fs.String("tags", "", "godog tag expression")
	junit := fs.String("junit", "", "write JUnit XML to this file")
	reportJSON := fs.String("report-json", "", "write a JSON run report to this file")
	reportHTML := fs.String("report-html", "", "write an HTML run report to this file")
	failFast := fs.Bool("fail-fast", false, "stop on first failure")
	_ = fs.Parse(os.Args[2:])
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

	pc := correlate.PollConfig{
		Interval:  parseDur(cfg.Poll.Interval, 200*time.Millisecond),
		Timeout:   parseDur(cfg.Poll.Timeout, 30*time.Second),
		StableFor: orDefault(cfg.Poll.StableFor, 3),
	}
	cor := correlate.New(func() string { return uuid.NewString() }, pc)
	st, err := engine.BuildStore(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mentat:", err)
		os.Exit(1)
	}
	eng, err := engine.Build(cfg, st, cor)
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

// parseDur converts a duration string into time.Duration. An empty string
// returns def (unset → default). A non-empty but malformed value is fatal.
func parseDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mentat: invalid duration %q: %v\n", s, err)
		os.Exit(1)
	}
	return d
}

func orDefault(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
