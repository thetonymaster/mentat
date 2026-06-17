package trace

import (
	"testing"
	"time"
)

func TestByOpIsStableSortedAndEnvelopeSpansForest(t *testing.T) {
	t0 := time.Unix(0, 0)
	tr := &Trace{
		RunID: "r1",
		Spans: []*Span{
			{Name: "invoke_agent", Start: t0, End: t0.Add(3 * time.Second), Attrs: map[string]string{"gen_ai.operation.name": "invoke_agent"}},
			{Name: "execute_tool search", Start: t0.Add(1 * time.Second), End: t0.Add(2 * time.Second), Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"}},
			{Name: "execute_tool summarize", Start: t0.Add(2 * time.Second), End: t0.Add(2500 * time.Millisecond), Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "summarize"}},
		},
	}
	tools := tr.ByOp("execute_tool")
	if len(tools) != 2 || tools[0].Attr("gen_ai.tool.name") != "search" || tools[1].Attr("gen_ai.tool.name") != "summarize" {
		t.Fatalf("ByOp order wrong: %v", tools)
	}
	if tr.Envelope() != 3*time.Second {
		t.Fatalf("envelope = %v, want 3s", tr.Envelope())
	}
}

func TestAttrInt(t *testing.T) {
	s := &Span{Attrs: map[string]string{"gen_ai.usage.input_tokens": "1200", "gen_ai.usage.cost_usd": "0.018", "bad_int": "abc"}}
	tests := []struct {
		name    string
		key     string
		wantVal int
		wantOK  bool
	}{
		{"happy path", "gen_ai.usage.input_tokens", 1200, true},
		{"missing key", "missing_key", 0, false},
		{"unparseable value", "bad_int", 0, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, ok := s.AttrInt(tt.key)
			if ok != tt.wantOK || got != tt.wantVal {
				t.Fatalf("AttrInt(%q) = (%d, %v), want (%d, %v)", tt.key, got, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestAttrFloat(t *testing.T) {
	s := &Span{Attrs: map[string]string{"gen_ai.usage.input_tokens": "1200", "gen_ai.usage.cost_usd": "0.018", "bad_float": "abc"}}
	tests := []struct {
		name    string
		key     string
		wantVal float64
		wantOK  bool
	}{
		{"happy path", "gen_ai.usage.cost_usd", 0.018, true},
		{"missing key", "missing_key", 0, false},
		{"unparseable value", "bad_float", 0, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, ok := s.AttrFloat(tt.key)
			if ok != tt.wantOK || got != tt.wantVal {
				t.Fatalf("AttrFloat(%q) = (%f, %v), want (%f, %v)", tt.key, got, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestEnvelopeEmpty(t *testing.T) {
	if got := (&Trace{}).Envelope(); got != 0 {
		t.Fatalf("Envelope() on empty Trace = %v, want 0", got)
	}
}
