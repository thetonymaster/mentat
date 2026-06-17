package comparator

import (
	"context"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// toolTrace builds a *trace.Trace whose spans represent execute_tool calls in
// the given order. Spans intentionally have a zero Start so that ByOp's stable
// sort preserves insertion order.
func toolTrace(names ...string) *trace.Trace {
	tr := &trace.Trace{}
	for _, n := range names {
		tr.Spans = append(tr.Spans, &trace.Span{
			Name:  "execute_tool " + n,
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: n},
		})
	}
	return tr
}

func TestSequenceName(t *testing.T) {
	if got := NewSequence().Name(); got != "sequence" {
		t.Fatalf("Name() = %q, want %q", got, "sequence")
	}
}

func TestSequenceCompare(t *testing.T) {
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      core.Expectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "ordered-subsequence passes",
			ev:       core.Evidence{Trace: toolTrace("search", "fetch_doc", "summarize")},
			exp:      SequenceExpectation{Order: []string{"search", "summarize"}},
			wantPass: true,
			wantErr:  false,
		},
		{
			name:     "wrong-order fails",
			ev:       core.Evidence{Trace: toolTrace("summarize", "search")},
			exp:      SequenceExpectation{Order: []string{"search", "summarize"}},
			wantPass: false,
			wantErr:  false,
		},
		{
			name:     "forbidden tool fails",
			ev:       core.Evidence{Trace: toolTrace("search", "delete_record", "summarize")},
			exp:      SequenceExpectation{Forbidden: []string{"delete_record"}},
			wantPass: false,
			wantErr:  false,
		},
		{
			name:     "empty Order with no Forbidden passes",
			ev:       core.Evidence{Trace: toolTrace("search")},
			exp:      SequenceExpectation{},
			wantPass: true,
			wantErr:  false,
		},
		{
			name:    "nil Trace returns error",
			ev:      core.Evidence{Trace: nil},
			exp:     SequenceExpectation{Order: []string{"search"}},
			wantErr: true,
		},
		{
			name:    "wrong expectation type string returns error",
			ev:      core.Evidence{Trace: toolTrace("search")},
			exp:     "not a SequenceExpectation",
			wantErr: true,
		},
		{
			name:    "wrong expectation type int returns error",
			ev:      core.Evidence{Trace: toolTrace("search")},
			exp:     42,
			wantErr: true,
		},
		{
			name:    "wrong expectation type nil returns error",
			ev:      core.Evidence{Trace: toolTrace("search")},
			exp:     nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewSequence().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v; reasons = %v", got.Pass, tt.wantPass, got.Reasons)
			}
			if tt.wantErr && got.Pass {
				t.Fatalf("wantErr=true but got Pass=true; err=%v", err)
			}
		})
	}
}
