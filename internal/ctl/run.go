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
		return core.Evidence{}, err
	}

	if err := SaveLast(ev.RunID); err != nil {
		return ev, err
	}

	switch {
	case opts.Quiet:
		fmt.Fprintln(w, ev.Output.Answer)
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
		fmt.Fprintf(w, "run %s\n", ev.RunID)
		fmt.Fprintf(w, "tools: %v\n", toolNames(ev))
		fmt.Fprintf(w, "spans: %d\n", len(ev.Trace.Spans))
		fmt.Fprintf(w, "answer: %s\n", ev.Output.Answer)
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
