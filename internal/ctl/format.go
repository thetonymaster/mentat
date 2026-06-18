package ctl

import (
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// FormatForest renders the span forest as an indented tree, highlighting gen_ai attrs.
func FormatForest(tr *trace.Trace, w io.Writer) {
	fmt.Fprintf(w, "Run %s (%d spans, %d root trace(s))\n\n", tr.RunID, len(tr.Spans), len(tr.Roots))
	byParent := map[string][]*trace.Span{}
	for _, s := range tr.Spans {
		byParent[s.ParentID] = append(byParent[s.ParentID], s)
	}
	var emit func(s *trace.Span, depth int)
	emit = func(s *trace.Span, depth int) {
		indent := ""
		for i := 0; i < depth; i++ {
			indent += "  "
		}
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
		fmt.Fprintf(w, "%s+- %s%s\n", indent, s.Name, extra)
		for _, c := range byParent[s.ID] {
			emit(c, depth+1)
		}
	}
	for _, r := range tr.Roots {
		emit(r, 0)
	}
}

// FormatTools lists the execute_tool spans in start order.
func FormatTools(tr *trace.Trace, w io.Writer) {
	tools := tr.ByOp(genai.OpExecuteTool)
	fmt.Fprintf(w, "Run %s: %d tool call(s)\n", tr.RunID, len(tools))
	for i, s := range tools {
		fmt.Fprintf(w, "%2d. %s\n", i+1, s.Attr(genai.ToolName))
	}
}
