package researchbot

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCaptureFixtureSerializesAttributeValues pins the exact string form that
// each OTel attribute value type serializes to through the real capture path.
//
// This is a regression guard for the deprecation swap of attribute.Value.Emit()
// -> attribute.Value.String(): for otel v1.44.0 they are byte-identical for the
// types the researchbot emits. A FUTURE otel upgrade that changes String()'s
// formatting (most dangerously, dropping the JSON quotes on STRINGSLICE so
// ["tool_calls"] becomes [tool_calls]) would silently shift the goldens — this
// test goes red instead.
func TestCaptureFixtureSerializesAttributeValues(t *testing.T) {
	// A single chat step gives us every value type the emitter produces:
	//   STRING       -> gen_ai.operation.name / agent.name / request.model
	//   INT64        -> gen_ai.usage.input_tokens (1200) / output_tokens
	//   FLOAT64      -> gen_ai.usage.cost_usd (0.018)
	//   STRINGSLICE  -> gen_ai.response.finish_reasons (["tool_calls"])
	p := &Plan{
		Scenario: "serialize_pin",
		Output:   "answer",
		Tokens:   Tokens{Input: 1200, Output: 340},
		CostUSD:  0.018,
		Steps: []Step{
			{Chat: &ChatStep{Model: "claude-3-5-sonnet", Finish: "tool_calls"}},
		},
	}

	data, err := CaptureFixture(context.Background(), p)
	if err != nil {
		t.Fatalf("CaptureFixture: %v", err)
	}

	var fx struct {
		Spans []struct {
			Op    string            `json:"op"`
			Attrs map[string]string `json:"attrs"`
		} `json:"spans"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	// Locate the root span (carries the int/float usage attrs) and the chat span
	// (carries the string-slice finish_reasons attr).
	var root, chat map[string]string
	for _, s := range fx.Spans {
		switch s.Op {
		case OpInvokeAgent:
			root = s.Attrs
		case OpChat:
			chat = s.Attrs
		}
	}
	if root == nil {
		t.Fatal("no root (invoke_agent) span in fixture")
	}
	if chat == nil {
		t.Fatal("no chat span in fixture")
	}

	tests := []struct {
		name  string
		attrs map[string]string
		key   string
		want  string
	}{
		{
			// THE load-bearing assertion: STRINGSLICE must serialize JSON-quoted.
			name:  "STRINGSLICE finish_reasons is JSON-quoted",
			attrs: chat,
			key:   AttrFinish,
			want:  `["tool_calls"]`,
		},
		{
			name:  "STRING model is bare",
			attrs: chat,
			key:   AttrModel,
			want:  "claude-3-5-sonnet",
		},
		{
			name:  "STRING operation name is bare",
			attrs: root,
			key:   AttrOp,
			want:  OpInvokeAgent,
		},
		{
			name:  "INT64 input_tokens is unquoted integer",
			attrs: root,
			key:   AttrInTokens,
			want:  "1200",
		},
		{
			name:  "INT64 output_tokens is unquoted integer",
			attrs: root,
			key:   AttrOutTokens,
			want:  "340",
		},
		{
			name:  "FLOAT64 cost_usd is unquoted float",
			attrs: root,
			key:   AttrCostUSD,
			want:  "0.018",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.attrs[tt.key]
			if !ok {
				t.Fatalf("attr %q absent from captured span", tt.key)
			}
			if got != tt.want {
				t.Fatalf("attr %q = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
