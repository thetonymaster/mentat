package ctl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func sampleForest() *trace.Trace {
	root := &trace.Span{ID: "1", Name: "invoke_agent researchbot",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "1200", genai.OutTokens: "600"}}
	t1 := &trace.Span{ID: "2", ParentID: "1", Name: "execute_tool search",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"}}
	t2 := &trace.Span{ID: "3", ParentID: "1", Name: "execute_tool summarize",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "summarize"}}
	return &trace.Trace{RunID: "r1", Roots: []*trace.Span{root}, Spans: []*trace.Span{root, t1, t2}}
}

func TestFormatForestShowsRootAndChildren(t *testing.T) {
	var b bytes.Buffer
	FormatForest(sampleForest(), &b)
	out := b.String()
	for _, want := range []string{"invoke_agent researchbot", "execute_tool search", "execute_tool summarize"} {
		if !strings.Contains(out, want) {
			t.Fatalf("forest missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatToolsListsSequence(t *testing.T) {
	var b bytes.Buffer
	FormatTools(sampleForest(), &b)
	out := b.String()
	if !strings.Contains(out, "1. search") || !strings.Contains(out, "2. summarize") {
		t.Fatalf("tools sequence wrong:\n%s", out)
	}
}
