package comparator

import (
	"context"
	"testing"
	"time"

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

// toolTraceMissingName builds a trace with a single execute_tool span that has
// no gen_ai.tool.name attribute, to exercise the malformed-evidence path.
func toolTraceMissingName() *trace.Trace {
	return &trace.Trace{Spans: []*trace.Span{{
		Name:  "execute_tool",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool},
	}}}
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
			name:    "execute_tool span missing tool name returns error",
			ev:      core.Evidence{Trace: toolTraceMissingName()},
			exp:     SequenceExpectation{Order: []string{"search"}},
			wantErr: true,
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

// svcTrace builds a *trace.Trace of SERVER spans, one per name, each carrying a
// service.name attr and a strictly increasing Start so first-seen order is the
// call order. A name may repeat (same service emitting multiple spans).
func svcTrace(names ...string) *trace.Trace {
	tr := &trace.Trace{}
	base := time.Unix(0, 0)
	for i, n := range names {
		tr.Spans = append(tr.Spans, &trace.Span{
			Name:  "POST",
			Start: base.Add(time.Duration(i) * time.Millisecond),
			Attrs: map[string]string{"service.name": n},
		})
	}
	return tr
}

func TestServiceSequenceExported(t *testing.T) {
	tests := []struct {
		name    string
		tr      *trace.Trace
		want    []string
		wantErr bool
	}{
		{
			name: "delegates to serviceSequence correctly",
			tr:   svcTrace("auth", "inventory", "payment"),
			want: []string{"auth", "inventory", "payment"},
		},
		{
			name:    "missing service.name is a hard error",
			tr:      &trace.Trace{Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{}}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ServiceSequence(tt.tr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d]=%q want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestToolSequence_Exported(t *testing.T) {
	tr := toolTrace("search", "fetch")
	got, err := ToolSequence(tr)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"search", "fetch"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestSequenceServiceKind(t *testing.T) {
	tests := []struct {
		name     string
		ev       core.Evidence
		exp      core.Expectation
		wantPass bool
		wantErr  bool
	}{
		{
			name:     "service ordered-subsequence passes (extra services allowed)",
			ev:       core.Evidence{Trace: svcTrace("gateway", "auth", "inventory", "payment", "notify")},
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment"}},
			wantPass: true,
		},
		{
			name:     "service wrong order fails (payment before inventory)",
			ev:       core.Evidence{Trace: svcTrace("gateway", "auth", "payment", "inventory", "notify")},
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: false,
		},
		{
			name:     "service forbidden fails (legacy-pricing called)",
			ev:       core.Evidence{Trace: svcTrace("gateway", "auth", "legacy-pricing", "inventory")},
			exp:      SequenceExpectation{Kind: "service", Forbidden: []string{"legacy-pricing"}},
			wantPass: false,
		},
		{
			name:     "service deduplicates repeated service spans",
			ev:       core.Evidence{Trace: svcTrace("gateway", "gateway", "auth")},
			exp:      SequenceExpectation{Kind: "service", Order: []string{"gateway", "auth"}},
			wantPass: true,
		},
		{
			name:    "service span missing service.name returns error",
			ev:      core.Evidence{Trace: &trace.Trace{Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{}}}}},
			exp:     SequenceExpectation{Kind: "service", Order: []string{"auth"}},
			wantErr: true,
		},
		{
			name:    "unknown Kind returns error",
			ev:      core.Evidence{Trace: svcTrace("gateway", "auth")},
			exp:     SequenceExpectation{Kind: "endpoint", Order: []string{"auth"}},
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
		})
	}
}
