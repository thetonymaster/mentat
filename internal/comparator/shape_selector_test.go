package comparator

import (
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/trace"
)

func TestParseSelector(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    Selector
		wantErr bool
		errSub  string // when wantErr, the error message must contain this substring
	}{
		{name: "single", in: "gen_ai.tool.name=search", want: Selector{{"gen_ai.tool.name", "search"}}},
		{name: "conjunction", in: "gen_ai.operation.name=execute_tool, gen_ai.tool.name=search",
			want: Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "search"}}},
		{name: "trims spaces", in: "  service.name = payment  ", want: Selector{{"service.name", "payment"}}},
		{name: "value may contain =", in: "k=a=b", want: Selector{{"k", "a=b"}}},
		{name: "reserved status canonical Error parses", in: "span.status=Error", want: Selector{{"span.status", "Error"}}},
		{name: "reserved kind canonical SERVER parses", in: "span.kind=SPAN_KIND_SERVER", want: Selector{{"span.kind", "SPAN_KIND_SERVER"}}},
		{name: "reserved name accepts any value", in: "span.name=execute_tool search", want: Selector{{"span.name", "execute_tool search"}}},
		{name: "empty selector", in: "", wantErr: true},
		{name: "blank selector", in: "   ", wantErr: true},
		{name: "missing equals", in: "service.name", wantErr: true},
		{name: "empty key", in: "=payment", wantErr: true},
		{name: "empty value", in: "service.name=", wantErr: true},
		{name: "empty predicate", in: "a=b,,c=d", wantErr: true},
		{name: "unknown reserved key", in: "span.staus=ERROR", wantErr: true},
		// A selector value under a reserved key must be a canonical constant; an
		// unknown value is a permanently-green selector (never matches) and so is a
		// hard authoring error at parse time naming the offending value.
		{name: "status wrong case is authoring error", in: "span.status=ERROR", wantErr: true, errSub: "ERROR"},
		{name: "status bogus value is authoring error", in: "span.status=bogus", wantErr: true, errSub: "bogus"},
		{name: "kind lowercase is authoring error", in: "span.kind=server", wantErr: true, errSub: "server"},
		{name: "kind bogus value is authoring error", in: "span.kind=SPAN_KIND_ROBOT", wantErr: true, errSub: "SPAN_KIND_ROBOT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSelector(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSelector(%q) = %v, want error", tt.in, got)
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("ParseSelector(%q) error %q missing offending value %q", tt.in, err.Error(), tt.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSelector(%q) unexpected error: %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSelector(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("pred[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSelectorMatchSpan(t *testing.T) {
	t.Parallel()
	sp := &trace.Span{
		ID: "s1", Name: "execute_tool search", Status: trace.StatusError, Kind: trace.KindInternal,
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search"},
	}
	tests := []struct {
		name string
		sel  Selector
		want bool
	}{
		{"attr match", Selector{{"gen_ai.tool.name", "search"}}, true},
		{"attr mismatch", Selector{{"gen_ai.tool.name", "delete"}}, false},
		{"missing attr is non-match", Selector{{"service.name", "payment"}}, false},
		{"conjunction all hold", Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "search"}}, true},
		{"conjunction one fails", Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "delete"}}, false},
		{"reserved span.name", Selector{{"span.name", "execute_tool search"}}, true},
		{"reserved span.status", Selector{{"span.status", "Error"}}, true},
		{"reserved span.kind", Selector{{"span.kind", "SPAN_KIND_INTERNAL"}}, true},
		{"reserved status mismatch", Selector{{"span.status", "Ok"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.sel.matchSpan(sp); got != tt.want {
				t.Errorf("matchSpan = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectorString(t *testing.T) {
	t.Parallel()
	got := Selector{{"a", "1"}, {"b", "2"}}.String()
	if got != "{a=1, b=2}" {
		t.Errorf("String() = %q, want %q", got, "{a=1, b=2}")
	}
}
