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

func TestFormatForest(t *testing.T) {
	tests := []struct {
		name     string
		tr       *trace.Trace
		wantSubs []string
	}{
		{
			name:     "nil trace prints marker",
			tr:       nil,
			wantSubs: []string{"(no trace)"},
		},
		{
			name:     "empty trace prints header with zero counts",
			tr:       &trace.Trace{RunID: "r0"},
			wantSubs: []string{"r0", "0 spans"},
		},
		{
			name: "happy path shows root, children, and token counts",
			tr:   sampleForest(),
			wantSubs: []string{
				"invoke_agent researchbot",
				"execute_tool search",
				"execute_tool summarize",
				"in=1200",
				"out=600",
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FormatForest panicked: %v", r)
				}
			}()
			var b bytes.Buffer
			FormatForest(tt.tr, &b)
			out := b.String()
			for _, want := range tt.wantSubs {
				if !strings.Contains(out, want) {
					t.Fatalf("FormatForest output missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

func TestFormatTools(t *testing.T) {
	tests := []struct {
		name     string
		tr       *trace.Trace
		wantSubs []string
	}{
		{
			name:     "nil trace prints marker",
			tr:       nil,
			wantSubs: []string{"(no trace)"},
		},
		{
			name:     "empty trace prints zero tool calls",
			tr:       &trace.Trace{RunID: "r0"},
			wantSubs: []string{"r0", "0 tool call"},
		},
		{
			name: "happy path lists tools in sequence",
			tr:   sampleForest(),
			wantSubs: []string{
				"1. search",
				"2. summarize",
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FormatTools panicked: %v", r)
				}
			}()
			var b bytes.Buffer
			FormatTools(tt.tr, &b)
			out := b.String()
			for _, want := range tt.wantSubs {
				if !strings.Contains(out, want) {
					t.Fatalf("FormatTools output missing %q in:\n%s", want, out)
				}
			}
		})
	}
}
