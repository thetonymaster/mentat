package cel

import (
	"reflect"
	"strings"
	"testing"
)

func TestEngineCompile(t *testing.T) {
	eng, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tests := []struct {
		name    string
		expr    string
		wantErr bool
		errSub  string
	}{
		{name: "valid bool eq", expr: `status == 201`},
		{name: "valid list macro", expr: `"search" in tools`},
		{name: "valid conditional", expr: `status == 201 ? tokens < 5000 : tokens < 2000`},
		{name: "syntax error", expr: `status ==`, wantErr: true},
		{name: "unknown variable", expr: `nope == 1`, wantErr: true, errSub: "nope"},
		{name: "type error int vs string", expr: `status == "x"`, wantErr: true},
		{name: "non-bool result", expr: `tokens + 1`, wantErr: true, errSub: "bool"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := eng.Compile(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Compile(%q) err=%v wantErr=%v", tt.expr, err, tt.wantErr)
			}
			if tt.errSub != "" && (err == nil || !strings.Contains(err.Error(), tt.errSub)) {
				t.Fatalf("Compile(%q): want error containing %q, got %v", tt.expr, tt.errSub, err)
			}
		})
	}
}

func TestEngineReferences(t *testing.T) {
	eng, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	prg, err := eng.Compile(`tokens < 5000 && status == 201`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got := prg.References()
	want := []string{"status", "tokens"} // sorted, deduped
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("References() = %v, want %v", got, want)
	}
}

func TestEngineEval(t *testing.T) {
	eng, err := NewEngine()
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tests := []struct {
		name string
		expr string
		vars map[string]any
		want bool
	}{
		{"int eq true", `status == 201`, map[string]any{"status": int64(201)}, true},
		{"int eq false", `status == 201`, map[string]any{"status": int64(200)}, false},
		{"list in true", `"search" in tools`, map[string]any{"tools": []string{"search", "summarize"}}, true},
		{"list in false", `"x" in tools`, map[string]any{"tools": []string{"search"}}, false},
		{"body dyn field", `body.ok == true`, map[string]any{"body": map[string]any{"ok": true}}, true},
		{"body null", `body == null`, map[string]any{"body": nil}, true},
		{"conditional true branch", `status == 201 ? tokens < 5000 : tokens < 2000`, map[string]any{"status": int64(201), "tokens": int64(3000)}, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			prg, err := eng.Compile(tt.expr)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.expr, err)
			}
			got, err := prg.Eval(tt.vars)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tt.expr, err)
			}
			if got != tt.want {
				t.Fatalf("Eval(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}
