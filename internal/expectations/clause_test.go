package expectations

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/comparator"
)

func TestParseCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    *comparator.Count
		wantErr bool
	}{
		{"empty is nil", "", nil, false},
		{"ge", ">=3", &comparator.Count{Op: ">=", N: 3}, false},
		{"eq", "==2", &comparator.Count{Op: "==", N: 2}, false},
		{"trims spaces", "  >= 4 ", &comparator.Count{Op: ">=", N: 4}, false},
		{"bad op gt", ">5", nil, true},
		{"bad op le", "<=5", nil, true},
		{"non-integer", "==x", nil, true},
		{"negative", "==-1", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCount(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCount(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCount(%q): %v", tt.in, err)
			}
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("parseCount(%q) = %v, want %v", tt.in, got, tt.want)
			}
			if got != nil && (got.Op != tt.want.Op || got.N != tt.want.N) {
				t.Errorf("parseCount(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestClauseToExpectation(t *testing.T) {
	t.Parallel()
	fan := func(p, c, n string) *fanoutYAML { return &fanoutYAML{Parent: p, Child: c, Count: n} }
	tests := []struct {
		name     string
		in       clauseYAML
		wantKind string
		wantErr  bool
	}{
		{"exists no count", clauseYAML{Exists: "gen_ai.tool.name=search"}, "exists", false},
		{"exists with count", clauseYAML{Exists: "gen_ai.tool.name=search", Count: ">=2"}, "exists", false},
		{"absent", clauseYAML{Absent: "span.status=ERROR"}, "absent", false},
		{"child of", clauseYAML{Child: "gen_ai.tool.name=search", Of: "gen_ai.operation.name=chat"}, "containment", false},
		{"descendant of", clauseYAML{Descendant: "gen_ai.tool.name=search", Of: "gen_ai.operation.name=invoke_agent"}, "containment", false},
		{"fanout", clauseYAML{Fanout: fan("gen_ai.operation.name=chat", "gen_ai.tool.name=search", ">=3")}, "fanout", false},
		{"no discriminator", clauseYAML{Of: "a=b"}, "", true},
		{"two discriminators", clauseYAML{Exists: "a=b", Absent: "c=d"}, "", true},
		{"exists with of", clauseYAML{Exists: "a=b", Of: "c=d"}, "", true},
		{"absent with count", clauseYAML{Absent: "a=b", Count: ">=1"}, "", true},
		{"child without of", clauseYAML{Child: "a=b"}, "", true},
		{"child with count", clauseYAML{Child: "a=b", Of: "c=d", Count: ">=1"}, "", true},
		{"fanout without count", clauseYAML{Fanout: fan("a=b", "c=d", "")}, "", true},
		{"fanout missing parent", clauseYAML{Fanout: fan("", "c=d", ">=1")}, "", true},
		{"bad selector", clauseYAML{Exists: "not-a-selector"}, "", true},
		{"bad count", clauseYAML{Exists: "a=b", Count: ">5"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := clauseToExpectation(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("clauseToExpectation(%+v) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("clauseToExpectation(%+v): %v", tt.in, err)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tt.wantKind)
			}
		})
	}
}

// Verify the fields are wired through, not just the Kind.
func TestClauseToExpectationFields(t *testing.T) {
	t.Parallel()
	got, err := clauseToExpectation(clauseYAML{
		Fanout: &fanoutYAML{Parent: "gen_ai.operation.name=chat", Child: "gen_ai.tool.name=search", Count: ">=3"},
	})
	if err != nil {
		t.Fatalf("clauseToExpectation: %v", err)
	}
	if got.Kind != "fanout" || got.Relation != "child" || got.Count == nil || got.Count.Op != ">=" || got.Count.N != 3 {
		t.Fatalf("unexpected fanout expectation: %+v (count %+v)", got, got.Count)
	}
	if len(got.Subject) != 1 || got.Subject[0] != (comparator.Pred{Key: "gen_ai.tool.name", Value: "search"}) {
		t.Errorf("Subject = %v, want child selector", got.Subject)
	}
	if len(got.Parent) != 1 || got.Parent[0] != (comparator.Pred{Key: "gen_ai.operation.name", Value: "chat"}) {
		t.Errorf("Parent = %v, want parent selector", got.Parent)
	}
}
