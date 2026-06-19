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
	domain, sub, rest, err := splitDomainVerb(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: mentatctl <agent|service> <run|trace|tools|services|replay|diff> [flags]")
		os.Exit(2)
	}
	if err := dispatch(domain, sub, rest); err != nil {
		fmt.Fprintln(os.Stderr, "mentatctl:", err)
		os.Exit(1)
	}
}

// splitDomainVerb parses `mentatctl <domain> <verb> [rest...]`. domain must be
// "agent" or "service"; an unknown domain or a missing verb is an error.
func splitDomainVerb(args []string) (domain, sub string, rest []string, err error) {
	if len(args) < 2 {
		return "", "", nil, fmt.Errorf("need <domain> <verb>")
	}
	domain = args[0]
	if domain != "agent" && domain != "service" {
		return "", "", nil, fmt.Errorf("unknown domain %q (want agent or service)", domain)
	}
	return domain, args[1], args[2:], nil
}

// checkDomainVerb rejects a domain-specific verb used under the wrong domain:
// `tools` is agent-only, `services` is service-only. Shared verbs (run, trace,
// replay, diff) are valid under both domains and pass through. Checked before any
// config/store is built so an invalid combination fails fast.
func checkDomainVerb(domain, sub string) error {
	switch sub {
	case "tools":
		if domain != "agent" {
			return fmt.Errorf("verb %q is only valid for the agent domain", sub)
		}
	case "services":
		if domain != "service" {
			return fmt.Errorf("verb %q is only valid for the service domain", sub)
		}
	}
	return nil
}

func dispatch(domain, sub string, rest []string) error {
	if err := checkDomainVerb(domain, sub); err != nil {
		return err
	}
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
	case "services":
		id, err := idArg()
		if err != nil {
			return err
		}
		tr, err := ctl.Resolve(ctx, cor, st, id)
		if err != nil {
			return err
		}
		return ctl.FormatServices(tr, os.Stdout)
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
		if domain == "service" {
			return ctl.DiffServices(ctx, cor, st, args[0], args[1], os.Stdout)
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
