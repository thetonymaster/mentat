package ctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
)

// Run drives the target named by opts.Target, prints a compact summary to w
// (or --json/--quiet variants), saves the run id via SaveLast, and returns the
// resulting Evidence.
func Run(ctx context.Context, eng *engine.Engine, opts RunOpts, w io.Writer) (core.Evidence, error) {
	var args []string
	switch {
	case opts.Scenario != "":
		args = []string{"--scenario", opts.Scenario}
	case opts.Prompt != "":
		args = []string{"--prompt", opts.Prompt}
	}

	ev, err := eng.Drive(ctx, opts.Target, args)
	if err != nil {
		return core.Evidence{}, fmt.Errorf("ctl: drive target %q with args %v: %w", opts.Target, args, err)
	}

	if err := SaveLast(ev.RunID); err != nil {
		return ev, fmt.Errorf("ctl: save last: %w", err)
	}

	switch {
	case opts.Quiet:
		if _, err := fmt.Fprintln(w, ev.Output.Answer); err != nil {
			return ev, fmt.Errorf("ctl: write answer for run %s: %w", ev.RunID, err)
		}
	case opts.JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{
			"runID":  ev.RunID,
			"answer": ev.Output.Answer,
			"tools":  toolNames(ev),
			"spans":  len(ev.Trace.Spans),
		}); err != nil {
			return ev, fmt.Errorf("ctl: encode json: %w", err)
		}
	default:
		if _, err := fmt.Fprintf(w, "run %s\n", ev.RunID); err != nil {
			return ev, fmt.Errorf("ctl: write run id for run %s: %w", ev.RunID, err)
		}
		if _, err := fmt.Fprintf(w, "tools: %v\n", toolNames(ev)); err != nil {
			return ev, fmt.Errorf("ctl: write tools for run %s: %w", ev.RunID, err)
		}
		if _, err := fmt.Fprintf(w, "spans: %d\n", len(ev.Trace.Spans)); err != nil {
			return ev, fmt.Errorf("ctl: write spans for run %s: %w", ev.RunID, err)
		}
		if _, err := fmt.Fprintf(w, "answer: %s\n", ev.Output.Answer); err != nil {
			return ev, fmt.Errorf("ctl: write answer for run %s: %w", ev.RunID, err)
		}
	}

	return ev, nil
}

func toolNames(ev core.Evidence) []string {
	var names []string
	for _, s := range ev.Trace.ByOp(genai.OpExecuteTool) {
		names = append(names, s.Attr(genai.ToolName))
	}
	return names
}
