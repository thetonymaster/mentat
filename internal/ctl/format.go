package ctl

import (
	"fmt"
	"io"
	"strings"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

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
