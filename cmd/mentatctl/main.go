package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
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
// replay, diff) are valid under both domains and pass through. An unknown verb is
// rejected here too. Checked before any config/store is built so an invalid verb
// or combination fails fast with the right CLI error instead of a config-load
// error from deps().
func checkDomainVerb(domain, sub string) error {
	switch sub {
	case "run", "trace", "replay", "diff":
		return nil
	case "tools":
		if domain != "agent" {
			return fmt.Errorf("verb %q is only valid for the agent domain", sub)
		}
	case "services":
		if domain != "service" {
			return fmt.Errorf("verb %q is only valid for the service domain", sub)
		}
	default:
		return fmt.Errorf("unknown subcommand %q", sub)
	}
	return nil
}

// runFlags are the parsed flags shared by the mentatctl verbs. Extracted from
// dispatch so the flag surface (including the US7 --prompt-file/-o/--timeout
// additions) is registered in one place and testable via bindRunFlags.
type runFlags struct {
	cfgPath    *string
	target     *string
	scenario   *string
	prompt     *string
	promptFile *string
	output     *string
	last       *bool
	asJSON     *bool
	quiet      *bool
	save       *string
	feature    *string
	timeout    *time.Duration
	verbose    *bool
	debug      *bool
}

// bindRunFlags registers every flag on fs and returns the bound pointers.
func bindRunFlags(fs *flag.FlagSet) *runFlags {
	return &runFlags{
		cfgPath:    fs.String("config", "mentat.yaml", "config file"),
		target:     fs.String("target", "", "named target from mentat.yaml"),
		scenario:   fs.String("scenario", "", "harness scenario"),
		prompt:     fs.String("prompt", "", "prompt"),
		promptFile: fs.String("prompt-file", "", "read the prompt from a file (- = stdin); mutually exclusive with --prompt"),
		output:     fs.String("o", "", "write the answer (only) to this file"),
		last:       fs.Bool("last", false, "use the most recent run id"),
		asJSON:     fs.Bool("json", false, "machine output"),
		quiet:      fs.Bool("quiet", false, "answer only"),
		save:       fs.String("save", "", "save the run's trace as a fixture at this path"),
		feature:    fs.String("feature", "", "feature file (replay)"),
		timeout:    fs.Duration("timeout", 0, "bound this invocation (e.g. 30s); 0 = no timeout"),
		verbose:    fs.Bool("v", false, "narrate at Info level to stderr"),
		debug:      fs.Bool("vv", false, "narrate at Debug level to stderr (implies -v)"),
	}
}

// resolvePrompt resolves the effective prompt from the --prompt and --prompt-file
// flags. --prompt-file wins only by being set: setting both is an error (no silent
// precedence). `-` reads stdin. A missing/unreadable file is a hard error naming
// the path. The read prompt is trimmed of surrounding whitespace.
func resolvePrompt(prompt, promptFile string, stdin io.Reader) (string, error) {
	if promptFile == "" {
		return prompt, nil
	}
	if prompt != "" {
		return "", fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if promptFile == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	b, err := os.ReadFile(promptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", promptFile, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// timeoutErr wraps a --timeout run failure with a descriptive, duration-naming
// error when (and only when) the bounding context hit its deadline. A nil run
// error stays nil; any other failure passes through unchanged.
func timeoutErr(timeout time.Duration, ctxErr, runErr error) error {
	if runErr == nil {
		return nil
	}
	if timeout > 0 && errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("run timed out after %s: %w", timeout, runErr)
	}
	return runErr
}

// universalFlags are accepted by every verb: the config path and the two
// verbosity switches. They are allowed on top of each verb's own flags.
var universalFlags = map[string]bool{"config": true, "v": true, "vv": true}

// verbFlags lists, per verb, the flags that verb's dispatch case actually reads
// (beyond the universal set). A flag set on the command line but absent here is
// rejected by checkFlags so an unsupported flag can never be silently ignored
// (architecture invariant: no silent fallbacks). run reads its inputs directly
// and never resolves an id, so `last` is deliberately absent; trace/tools/
// services/replay resolve an id via idArg() and so accept `last`.
var verbFlags = map[string]map[string]bool{
	"run":      {"target": true, "scenario": true, "prompt": true, "prompt-file": true, "o": true, "json": true, "quiet": true, "save": true, "timeout": true},
	"trace":    {"last": true},
	"tools":    {"last": true},
	"services": {"last": true},
	"replay":   {"feature": true, "last": true},
	"diff":     {},
}

// checkFlags is a pure post-parse guard: it rejects any flag that was explicitly
// set on fs but is not supported by the sub verb, and rejects a negative
// --timeout. It inspects only the flags Parse actually saw (fs.Visit), so unset
// defaults never trip it. Returned errors name the offending flag and verb (or
// the bad duration) so the failure is self-explanatory.
func checkFlags(sub string, fs *flag.FlagSet, timeout time.Duration) error {
	allowed := verbFlags[sub]
	var bad error
	fs.Visit(func(fl *flag.Flag) {
		if bad != nil {
			return
		}
		if universalFlags[fl.Name] || allowed[fl.Name] {
			return
		}
		bad = fmt.Errorf("flag %q is not supported by the %q command", "--"+fl.Name, sub)
	})
	if bad != nil {
		return bad
	}
	if timeout < 0 {
		return fmt.Errorf("--timeout must be non-negative, got %s", timeout)
	}
	return nil
}

func dispatch(domain, sub string, rest []string) error {
	if err := checkDomainVerb(domain, sub); err != nil {
		return err
	}
	ctx := context.Background()
	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	f := bindRunFlags(fs)
	_ = fs.Parse(rest)
	if err := checkFlags(sub, fs, *f.timeout); err != nil {
		return err
	}
	args := fs.Args()

	// Verbosity flags map to a stderr slog handler (discard by default, SC-005);
	// the logger and the store endpoint are injected into the seams here.
	logger := engine.NewLogger(os.Stderr, *f.verbose, *f.debug)

	cfg, st, cor, err := deps(*f.cfgPath, logger)
	if err != nil {
		return err
	}

	idArg := func() (string, error) {
		if *f.last {
			return ctl.ReadLast()
		}
		if len(args) == 0 {
			return "", fmt.Errorf("%s: need a run id (or --last)", sub)
		}
		return args[0], nil
	}

	switch sub {
	case "run":
		prompt, err := resolvePrompt(*f.prompt, *f.promptFile, os.Stdin)
		if err != nil {
			return err
		}
		eng, err := engine.Build(cfg, st, cor, engine.WithLogger(logger))
		if err != nil {
			return err
		}
		runCtx := ctx
		if *f.timeout > 0 {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(ctx, *f.timeout)
			defer cancel()
		}
		opts := ctl.RunOpts{Target: *f.target, Scenario: *f.scenario, Prompt: prompt, JSON: *f.asJSON, Quiet: *f.quiet, Save: *f.save, Output: *f.output}
		ev, err := ctl.Run(runCtx, eng, opts, os.Stdout)
		if err != nil {
			return timeoutErr(*f.timeout, runCtx.Err(), err)
		}
		if *f.save != "" {
			return ctl.WriteFixture(ev.Trace, *f.save)
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
		if *f.feature == "" {
			return fmt.Errorf("replay: --feature is required to re-evaluate a run")
		}
		eng, err := engine.Build(cfg, st, cor, engine.WithLogger(logger))
		if err != nil {
			return err
		}
		return ctl.ReplayFeature(ctx, eng, id, *f.feature, "", os.Stdout)
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

func deps(cfgPath string, logger *slog.Logger) (config.Config, core.TraceStore, core.Correlator, error) {
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
	cor, err := engine.BuildCorrelator(cfg, logger)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("mentatctl: build correlator: %w", err)
	}
	return cfg, st, cor, nil
}
