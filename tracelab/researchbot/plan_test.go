package researchbot

import "testing"

func TestLoadPlanParsesStepsInOrder(t *testing.T) {
	data := []byte(`
scenario: happy
output: "Q3 revenue grew 12%"
tokens: { input: 1200, output: 600 }
cost_usd: 0.018
steps:
  - chat: { model: claude-x, finish: tool_calls }
  - tool: { name: search, args: "q3", result: "doc-1" }
`)
	p, err := LoadPlan(data)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if p.Scenario != "happy" || p.Tokens.Input != 1200 || p.CostUSD != 0.018 {
		t.Fatalf("scalars wrong: %+v", p)
	}
	if len(p.Steps) != 2 || p.Steps[0].Chat == nil || p.Steps[1].Tool == nil {
		t.Fatalf("steps wrong: %+v", p.Steps)
	}
	if p.Steps[1].Tool.Name != "search" {
		t.Fatalf("tool name = %q", p.Steps[1].Tool.Name)
	}
}

func TestValidateRejectsStepWithBothChatAndTool(t *testing.T) {
	p := &Plan{Scenario: "x", Steps: []Step{{Chat: &ChatStep{}, Tool: &ToolStep{}}}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for step with both chat and tool")
	}
}

func TestLoadPlanErrors(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "invalid yaml",
			data:    []byte(":\tbad:\tyaml: ["),
			wantErr: true,
		},
		{
			name:    "missing scenario",
			data:    []byte("output: x\n"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadPlan(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		plan    *Plan
		wantErr bool
	}{
		{
			name:    "valid no steps",
			plan:    &Plan{Scenario: "x"},
			wantErr: false,
		},
		{
			name:    "missing scenario",
			plan:    &Plan{},
			wantErr: true,
		},
		{
			name: "both chat and tool",
			plan: &Plan{Scenario: "x", Steps: []Step{
				{Chat: &ChatStep{}, Tool: &ToolStep{Name: "t"}},
			}},
			wantErr: true,
		},
		{
			name:    "neither chat nor tool",
			plan:    &Plan{Scenario: "x", Steps: []Step{{}}},
			wantErr: true,
		},
		{
			name: "tool missing name",
			plan: &Plan{Scenario: "x", Steps: []Step{
				{Tool: &ToolStep{}},
			}},
			wantErr: true,
		},
		{
			name: "valid chat step",
			plan: &Plan{Scenario: "x", Steps: []Step{
				{Chat: &ChatStep{Model: "m", Finish: "end_turn"}},
			}},
			wantErr: false,
		},
		{
			name: "valid tool step",
			plan: &Plan{Scenario: "x", Steps: []Step{
				{Tool: &ToolStep{Name: "search", Args: "q", Result: "r"}},
			}},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
