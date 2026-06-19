package comparator

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestCELName(t *testing.T) {
	if got := NewCEL().Name(); got != "cel" {
		t.Fatalf("Name() = %q, want %q", got, "cel")
	}
}

func TestCELWrongExpectationType(t *testing.T) {
	_, err := NewCEL().Compare(context.Background(), core.Evidence{}, "not a CELExpectation")
	if err == nil {
		t.Fatal("want error for wrong expectation type, got nil")
	}
}

func TestCELOutputVars(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		out       core.Output
		wantPass  bool
		reasonSub string // substring required in the failure reason (when !wantPass)
	}{
		{name: "status pass", expr: `status == 201`, out: core.Output{Status: 201}, wantPass: true},
		{name: "status fail shows value", expr: `status == 200`, out: core.Output{Status: 201}, wantPass: false, reasonSub: "status=201"},
		{name: "exitCode pass", expr: `exitCode == 0`, out: core.Output{ExitCode: 0}, wantPass: true},
		{name: "answer contains macro", expr: `answer.contains("revenue")`, out: core.Output{Answer: "Q3 revenue up"}, wantPass: true},
		{name: "bodyText startsWith", expr: `bodyText.startsWith("{")`, out: core.Output{Body: []byte(`{"a":1}`)}, wantPass: true},
		{name: "compound fail shows offending value", expr: `status == 201 && exitCode == 0`, out: core.Output{Status: 500, ExitCode: 0}, wantPass: false, reasonSub: "status=500"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: tt.out}
			v, err := NewCEL().Compare(context.Background(), ev, CELExpectation{Expr: tt.expr})
			if err != nil {
				t.Fatalf("Compare(%q): %v", tt.expr, err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if tt.reasonSub != "" {
				if len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], tt.reasonSub) {
					t.Fatalf("want reason containing %q, got %v", tt.reasonSub, v.Reasons)
				}
			}
		})
	}
}
