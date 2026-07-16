package ctl

import (
	"fmt"
	"io"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// RenderSummary renders the human `mentatctl agent run` summary for ev. The first
// four lines (run/tools/spans/answer) are byte-identical to the pre-US7 format;
// US7 appends tokens (in/out), cost, latency envelope (ms) and the root trace ids.
// Every metric reuses the single-source Evidence derivation — comparator.TokensInOut,
// comparator.CostOrZero (pricing table, same unknown/ambiguous-model rules) and
// Trace.Envelope — so the summary never disagrees with comparators or reports.
// A malformed token/cost value is a hard, wrapped error, never a fabricated number.
func RenderSummary(ev core.Evidence, pricing core.Pricing) (string, error) {
	in, out, err := comparator.TokensInOut(ev.Trace)
	if err != nil {
		return "", fmt.Errorf("ctl: derive tokens for run %s: %w", ev.RunID, err)
	}
	cost, err := comparator.CostOrZero(ev.Trace, pricing)
	if err != nil {
		return "", fmt.Errorf("ctl: derive cost for run %s: %w", ev.RunID, err)
	}
	var b strings.Builder
	// Pre-US7 lines — must stay byte-identical (byte-stability contract, T023).
	fmt.Fprintf(&b, "run %s\n", ev.RunID)
	fmt.Fprintf(&b, "tools: %v\n", toolNames(ev))
	fmt.Fprintf(&b, "spans: %d\n", spanCount(ev.Trace))
	fmt.Fprintf(&b, "answer: %s\n", ev.Output.Answer)
	// US7 additive lines.
	fmt.Fprintf(&b, "tokens: in %d out %d\n", in, out)
	fmt.Fprintf(&b, "cost: $%.4f\n", cost)
	fmt.Fprintf(&b, "latency: %d ms\n", latencyMS(ev.Trace))
	fmt.Fprintf(&b, "traces: %s\n", strings.Join(traceIDs(ev.Trace), " "))
	return b.String(), nil
}

func spanCount(tr *trace.Trace) int {
	if tr == nil {
		return 0
	}
	return len(tr.Spans)
}

func latencyMS(tr *trace.Trace) int64 {
	if tr == nil {
		return 0
	}
	return tr.Envelope().Milliseconds()
}

func traceIDs(tr *trace.Trace) []string {
	if tr == nil {
		return nil
	}
	return tr.TraceIDs
}

// FormatForest renders the span forest as an indented tree, highlighting gen_ai attrs.
func FormatForest(tr *trace.Trace, w io.Writer) error {
	if tr == nil {
		if _, err := fmt.Fprintln(w, "(no trace)"); err != nil {
			return fmt.Errorf("ctl: format forest no-trace line: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(w, "Run %s (%d spans, %d root trace(s))\n\n", tr.RunID, len(tr.Spans), len(tr.Roots)); err != nil {
		return fmt.Errorf("ctl: format forest header run %s: %w", tr.RunID, err)
	}
	byParent := map[string][]*trace.Span{}
	for _, s := range tr.Spans {
		byParent[s.ParentID] = append(byParent[s.ParentID], s)
	}
	var emit func(s *trace.Span, depth int) error
	emit = func(s *trace.Span, depth int) error {
		indent := strings.Repeat("  ", depth)
		extra := ""
		if n, ok := s.AttrInt(genai.InTokens); ok {
			extra += fmt.Sprintf(" in=%d", n)
		}
		if n, ok := s.AttrInt(genai.OutTokens); ok {
			extra += fmt.Sprintf(" out=%d", n)
		}
		if tn := s.Attr(genai.ToolName); tn != "" {
			extra += " tool=" + tn
		}
		if _, err := fmt.Fprintf(w, "%s+- %s%s\n", indent, s.Name, extra); err != nil {
			return fmt.Errorf("ctl: format forest span %s: %w", s.Name, err)
		}
		for _, c := range byParent[s.ID] {
			if err := emit(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range tr.Roots {
		if err := emit(r, 0); err != nil {
			return err
		}
	}
	return nil
}

// FormatServices lists the distinct services in first-seen call order, mirroring
// FormatTools for the service domain. It reuses the sequence comparator's service
// selection (single source of truth with the `services` CEL variable).
func FormatServices(tr *trace.Trace, w io.Writer) error {
	if tr == nil {
		if _, err := fmt.Fprintln(w, "(no trace)"); err != nil {
			return fmt.Errorf("ctl: format services no-trace line: %w", err)
		}
		return nil
	}
	svcs, err := comparator.ServiceSequence(tr)
	if err != nil {
		return fmt.Errorf("ctl: format services run %s: %w", tr.RunID, err)
	}
	if _, err := fmt.Fprintf(w, "Run %s: %d service call(s)\n", tr.RunID, len(svcs)); err != nil {
		return fmt.Errorf("ctl: format services header run %s: %w", tr.RunID, err)
	}
	for i, s := range svcs {
		if _, err := fmt.Fprintf(w, "%2d. %s\n", i+1, s); err != nil {
			return fmt.Errorf("ctl: format service line %d run %s: %w", i+1, tr.RunID, err)
		}
	}
	return nil
}

// FormatTools lists the execute_tool spans in start order.
func FormatTools(tr *trace.Trace, w io.Writer) error {
	if tr == nil {
		if _, err := fmt.Fprintln(w, "(no trace)"); err != nil {
			return fmt.Errorf("ctl: format tools no-trace line: %w", err)
		}
		return nil
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if _, err := fmt.Fprintf(w, "Run %s: %d tool call(s)\n", tr.RunID, len(tools)); err != nil {
		return fmt.Errorf("ctl: format tools header run %s: %w", tr.RunID, err)
	}
	for i, s := range tools {
		if _, err := fmt.Fprintf(w, "%2d. %s\n", i+1, s.Attr(genai.ToolName)); err != nil {
			return fmt.Errorf("ctl: format tool line %d run %s: %w", i+1, tr.RunID, err)
		}
	}
	return nil
}
