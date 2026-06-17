package researchbot

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Plan struct {
	Scenario string  `yaml:"scenario"`
	Output   string  `yaml:"output"`
	Tokens   Tokens  `yaml:"tokens"`
	CostUSD  float64 `yaml:"cost_usd"`
	Steps    []Step  `yaml:"steps"`
}

type Tokens struct {
	Input  int `yaml:"input"`
	Output int `yaml:"output"`
}

type Step struct {
	Chat *ChatStep `yaml:"chat,omitempty"`
	Tool *ToolStep `yaml:"tool,omitempty"`
}

type ChatStep struct {
	Model  string `yaml:"model"`
	Finish string `yaml:"finish"`
}

type ToolStep struct {
	Name   string `yaml:"name"`
	Args   string `yaml:"args"`
	Result string `yaml:"result"`
}

func LoadPlan(data []byte) (*Plan, error) {
	var p Plan
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Plan) Validate() error {
	if p.Scenario == "" {
		return fmt.Errorf("plan: scenario is required")
	}
	for i, s := range p.Steps {
		switch {
		case s.Chat != nil && s.Tool != nil:
			return fmt.Errorf("plan: step %d has both chat and tool", i)
		case s.Chat == nil && s.Tool == nil:
			return fmt.Errorf("plan: step %d has neither chat nor tool", i)
		case s.Tool != nil && s.Tool.Name == "":
			return fmt.Errorf("plan: step %d tool missing name", i)
		case s.Chat != nil && s.Chat.Model == "":
			return fmt.Errorf("plan: step %d chat missing model", i)
		}
	}
	return nil
}
