package trace

import (
	"strings"
	"testing"
	"time"
)

func TestByOpIsStableSortedAndEnvelopeSpansForest(t *testing.T) {
	t0 := time.Unix(0, 0)
	tests := []struct {
		name         string
		tr           *Trace
		op           string
		wantToolName []string // expected gen_ai.tool.name of ByOp(op) results, in order
		wantEnvelope time.Duration
	}{
		{
			name: "ByOp stable-sorted and envelope spans forest",
			tr: &Trace{
				RunID: "r1",
				Spans: []*Span{
					{Name: "invoke_agent", Start: t0, End: t0.Add(3 * time.Second), Attrs: map[string]string{"gen_ai.operation.name": "invoke_agent"}},
					{Name: "execute_tool search", Start: t0.Add(1 * time.Second), End: t0.Add(2 * time.Second), Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"}},
					{Name: "execute_tool summarize", Start: t0.Add(2 * time.Second), End: t0.Add(2500 * time.Millisecond), Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "summarize"}},
				},
			},
			op:           "execute_tool",
			wantToolName: []string{"search", "summarize"},
			wantEnvelope: 3 * time.Second,
		},
		{
			name:         "empty trace: no matching ops and zero envelope",
			tr:           &Trace{},
			op:           "execute_tool",
			wantToolName: nil,
			wantEnvelope: 0,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tr.ByOp(tt.op)
			if len(got) != len(tt.wantToolName) {
				t.Fatalf("ByOp(%q) len = %d, want %d: %v", tt.op, len(got), len(tt.wantToolName), got)
			}
			for i, want := range tt.wantToolName {
				if name := got[i].Attr("gen_ai.tool.name"); name != want {
					t.Fatalf("ByOp(%q)[%d] gen_ai.tool.name = %q, want %q", tt.op, i, name, want)
				}
			}
			if env := tt.tr.Envelope(); env != tt.wantEnvelope {
				t.Fatalf("Envelope() = %v, want %v", env, tt.wantEnvelope)
			}
		})
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

func TestCanonicalConstants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"StatusUnset", StatusUnset, "Unset"},
		{"StatusOk", StatusOk, "Ok"},
		{"StatusError", StatusError, "Error"},
		{"KindInternal", KindInternal, "SPAN_KIND_INTERNAL"},
		{"KindServer", KindServer, "SPAN_KIND_SERVER"},
		{"KindClient", KindClient, "SPAN_KIND_CLIENT"},
		{"KindProducer", KindProducer, "SPAN_KIND_PRODUCER"},
		{"KindConsumer", KindConsumer, "SPAN_KIND_CONSUMER"},
		{"KindUnspecified", KindUnspecified, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestNormalizeStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"omitted maps to unset", "", StatusUnset, false},
		{"otlp unset spelling", "STATUS_CODE_UNSET", StatusUnset, false},
		{"canonical unset spelling", "Unset", StatusUnset, false},
		{"otlp ok spelling", "STATUS_CODE_OK", StatusOk, false},
		{"canonical ok spelling", "Ok", StatusOk, false},
		{"otlp error spelling", "STATUS_CODE_ERROR", StatusError, false},
		{"canonical error spelling", "Error", StatusError, false},
		{"unknown spelling errors", "ERROR", "", true},
		{"lowercase unknown errors", "ok", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeStatus(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeStatus(%q) err = %v, wantErr = %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeStatus(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.raw) {
				t.Fatalf("NormalizeStatus(%q) err = %q, want it to name the offending value %q", tt.raw, err.Error(), tt.raw)
			}
		})
	}
}

func TestNormalizeKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"omitted maps to unspecified", "", KindUnspecified, false},
		{"internal", "SPAN_KIND_INTERNAL", KindInternal, false},
		{"server", "SPAN_KIND_SERVER", KindServer, false},
		{"client", "SPAN_KIND_CLIENT", KindClient, false},
		{"producer", "SPAN_KIND_PRODUCER", KindProducer, false},
		{"consumer", "SPAN_KIND_CONSUMER", KindConsumer, false},
		{"unknown spelling errors", "SPAN_KIND_BOGUS", "", true},
		{"short form errors", "server", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeKind(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeKind(%q) err = %v, wantErr = %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeKind(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.raw) {
				t.Fatalf("NormalizeKind(%q) err = %q, want it to name the offending value %q", tt.raw, err.Error(), tt.raw)
			}
		})
	}
}
