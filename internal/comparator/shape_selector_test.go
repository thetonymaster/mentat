package comparator

import (
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
	}{
		{"single", "gen_ai.tool.name=search", Selector{{"gen_ai.tool.name", "search"}}, false},
		{"conjunction", "gen_ai.operation.name=execute_tool, gen_ai.tool.name=search",
			Selector{{"gen_ai.operation.name", "execute_tool"}, {"gen_ai.tool.name", "search"}}, false},
		{"trims spaces", "  service.name = payment  ", Selector{{"service.name", "payment"}}, false},
		{"value may contain =", "k=a=b", Selector{{"k", "a=b"}}, false},
		{"reserved status", "span.status=ERROR", Selector{{"span.status", "ERROR"}}, false},
		{"empty selector", "", nil, true},
		{"blank selector", "   ", nil, true},
		{"missing equals", "service.name", nil, true},
		{"empty key", "=payment", nil, true},
		{"empty value", "service.name=", nil, true},
		{"empty predicate", "a=b,,c=d", nil, true},
		{"unknown reserved key", "span.staus=ERROR", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSelector(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSelector(%q) = %v, want error", tt.in, got)
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
		ID: "s1", Name: "execute_tool search", Status: "ERROR", Kind: "INTERNAL",
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
		{"reserved span.status", Selector{{"span.status", "ERROR"}}, true},
		{"reserved span.kind", Selector{{"span.kind", "INTERNAL"}}, true},
		{"reserved status mismatch", Selector{{"span.status", "OK"}}, false},
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
