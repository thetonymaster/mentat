package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/ctl"
	"github.com/thetonymaster/mentat/internal/engine"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "agent" {
		fmt.Fprintln(os.Stderr, "usage: mentatctl agent <run|trace|tools|replay|diff> [flags]")
		os.Exit(2)
	}
	sub, rest := os.Args[2], os.Args[3:]
	if err := dispatch(sub, rest); err != nil {
		fmt.Fprintln(os.Stderr, "mentatctl:", err)
		os.Exit(1)
	}
}

func dispatch(sub string, rest []string) error {
	ctx := context.Background()
	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	cfgPath := fs.String("config", "mentat.yaml", "config file")
	target := fs.String("target", "", "named target from mentat.yaml")
	scenario := fs.String("scenario", "", "harness scenario")
	prompt := fs.String("prompt", "", "prompt")
	last := fs.Bool("last", false, "use the most recent run id")
	asJSON := fs.Bool("json", false, "machine output")
	quiet := fs.Bool("quiet", false, "answer only")
	save := fs.String("save", "", "save the run's trace as a fixture at this path")
	feature := fs.String("feature", "", "feature file (replay)")
	_ = fs.Parse(rest)
	args := fs.Args()

	cfg, st, cor, err := deps(*cfgPath)
	if err != nil {
		return err
	}

	idArg := func() (string, error) {
		if *last {
			return ctl.ReadLast()
		}
		if len(args) == 0 {
			return "", fmt.Errorf("%s: need a run id (or --last)", sub)
		}
		return args[0], nil
	}

	switch sub {
	case "run":
		eng, err := engine.Build(cfg, st, cor)
		if err != nil {
			return err
		}
		ev, err := ctl.Run(ctx, eng, ctl.RunOpts{Target: *target, Scenario: *scenario, Prompt: *prompt, JSON: *asJSON, Quiet: *quiet, Save: *save}, os.Stdout)
		if err != nil {
			return err
		}
		if *save != "" {
			return ctl.WriteFixture(ev.Trace, *save)
		}
		return nil
	case "trace":
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		return ctl.FormatForest(tr, os.Stdout)
	case "tools":
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		return ctl.FormatTools(tr, os.Stdout)
	case "replay":
		id, err := idArg()
		if err != nil {
			return err
		}
		if *feature == "" {
			return fmt.Errorf("replay: --feature is required to re-evaluate a run")
		}
		eng, err := engine.Build(cfg, st, cor)
		if err != nil {
			return err
		}
		return ctl.ReplayFeature(ctx, eng, id, *feature, "", os.Stdout)
	case "diff":
		if len(args) < 2 {
			return fmt.Errorf("diff: need two run ids")
		}
		return ctl.Diff(ctx, cor, st, args[0], args[1], os.Stdout)
	default:
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

func deps(cfgPath string) (config.Config, core.TraceStore, core.Correlator, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("mentatctl: read config %q: %w", cfgPath, err)
	}
	cfg, err := config.Load(data)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("mentatctl: parse config %q: %w", cfgPath, err)
	}
	st, err := engine.BuildStore(cfg)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("mentatctl: build store: %w", err)
	}
	pc := correlate.PollConfig{
		Interval:  parseDur(cfg.Poll.Interval, 200*time.Millisecond),
		Timeout:   parseDur(cfg.Poll.Timeout, 30*time.Second),
		StableFor: orDefault(cfg.Poll.StableFor, 3),
	}
	cor := correlate.New(func() string { return uuid.NewString() }, pc)
	return cfg, st, cor, nil
}

// parseDur converts a duration string into time.Duration. An empty string
// returns def (unset → default). A non-empty but malformed value is fatal.
func parseDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mentatctl: invalid duration %q: %v\n", s, err)
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
