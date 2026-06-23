package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cucumber/godog"
	"github.com/google/uuid"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/registry"
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

	opts := godog.Options{
		Format:        "pretty",
		Paths:         paths,
		Tags:          *tags,
		Concurrency:   *concurrency,
		Output:        os.Stdout,
		StopOnFailure: *failFast,
	}
	var junitFile *os.File
	if *junit != "" {
		opts.Format = "junit"
		f, err := os.Create(*junit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mentat: create junit file %q: %v\n", *junit, err)
			os.Exit(1)
		}
		junitFile = f
		opts.Output = f
	}

	col := report.NewCollector()
	suite := godog.TestSuite{ScenarioInitializer: steps.InitializerWithCollector(eng, col), Options: &opts}
	started := time.Now()
	code := suite.Run()
	if junitFile != nil {
		if err := junitFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "mentat: close junit file %q: %v\n", *junit, err)
			if code == 0 {
				code = 1
			}
		}
	}
	targets := map[string]string{}
	if *reportJSON != "" {
		targets["json"] = *reportJSON
	}
	if *reportHTML != "" {
		targets["html"] = *reportHTML
	}
	if len(targets) > 0 {
		if err := emitReports(col.Report(started, time.Since(started)), targets); err != nil {
			fmt.Fprintln(os.Stderr, "mentat:", err)
			if code == 0 {
				code = 1
			}
		}
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

// emitReports writes each selected report. A failure (unknown reporter, create/encode
// error) is returned — never swallowed (invariant #4). The caller turns it into a
// non-zero exit.
func emitReports(rep core.RunReport, targets map[string]string) error {
	for name, path := range targets {
		r, ok := registry.Reporter(name)
		if !ok {
			return fmt.Errorf("unknown reporter %q", name)
		}
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create %s report %q: %w", name, path, err)
		}
		if err := r.Report(rep, f); err != nil {
			_ = f.Close() // best-effort; the Report error takes precedence
			return fmt.Errorf("writing %s report %q: %w", name, path, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %s report %q: %w", name, path, err)
		}
	}
	return nil
}
