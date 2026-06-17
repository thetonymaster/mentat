package researchbot

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// requireAttrString fetches a string attribute from a span or fatals if absent.
func requireAttrString(t *testing.T, s tracetest.SpanStub, key string) string {
	t.Helper()
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	t.Fatalf("span %q: attribute %q not found", s.Name, key)
	return ""
}

// requireAttrInt fetches an int64 attribute from a span or fatals if absent.
func requireAttrInt(t *testing.T, s tracetest.SpanStub, key string) int64 {
	t.Helper()
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsInt64()
		}
	}
	t.Fatalf("span %q: attribute %q not found", s.Name, key)
	return 0
}

// requireAttrFloat fetches a float64 attribute from a span or fatals if absent.
func requireAttrFloat(t *testing.T, s tracetest.SpanStub, key string) float64 {
	t.Helper()
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsFloat64()
		}
	}
	t.Fatalf("span %q: attribute %q not found", s.Name, key)
	return 0
}

// requireAttrStringSlice fetches a []string attribute from a span or fatals if absent.
func requireAttrStringSlice(t *testing.T, s tracetest.SpanStub, key string) []string {
	t.Helper()
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value.AsStringSlice()
		}
	}
	t.Fatalf("span %q: attribute %q not found", s.Name, key)
	return nil
}

// byNameUniq indexes spans by name; fatals on duplicate names.
func byNameUniq(t *testing.T, spans tracetest.SpanStubs) map[string]tracetest.SpanStub {
	t.Helper()
	m := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		if _, dup := m[s.Name]; dup {
			t.Fatalf("duplicate span name %q", s.Name)
		}
		m[s.Name] = s
	}
	return m
}

func TestEmitNilPlanReturnsError(t *testing.T) {
	ctx := context.Background()
	exp := tracetest.NewInMemoryExporter()
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer tp.Shutdown(ctx) //nolint:errcheck
	err = Emit(ctx, tp.Tracer("test"), nil)
	if err == nil {
		t.Fatal("expected error for nil plan, got nil")
	}
}

func TestEmit(t *testing.T) {
	tests := []struct {
		name      string
		plan      *Plan
		wantSpans int
		// childNames are the span names expected as children of root.
		childNames []string
	}{
		{
			name: "root-only (no steps)",
			plan: &Plan{
				Scenario: "empty",
				Tokens:   Tokens{Input: 100, Output: 50},
				CostUSD:  0.001,
				Steps:    nil,
			},
			wantSpans:  1,
			childNames: nil,
		},
		{
			name: "chat-only plan",
			plan: &Plan{
				Scenario: "chat-only",
				Tokens:   Tokens{Input: 500, Output: 200},
				CostUSD:  0.005,
				Steps: []Step{
					{Chat: &ChatStep{Model: "claude-x", Finish: "end_turn"}},
				},
			},
			wantSpans:  2,
			childNames: []string{"chat claude-x"},
		},
		{
			name: "tool-only plan",
			plan: &Plan{
				Scenario: "tool-only",
				Tokens:   Tokens{Input: 800, Output: 300},
				CostUSD:  0.009,
				Steps: []Step{
					{Tool: &ToolStep{Name: "search", Args: "q1", Result: "r1"}},
				},
			},
			wantSpans:  2,
			childNames: []string{"execute_tool search"},
		},
		{
			name: "mixed plan",
			plan: &Plan{
				Scenario: "happy",
				Tokens:   Tokens{Input: 1200, Output: 600},
				CostUSD:  0.018,
				Steps: []Step{
					{Chat: &ChatStep{Model: "claude-x", Finish: "tool_calls"}},
					{Tool: &ToolStep{Name: "search", Args: "q3", Result: "doc-1"}},
				},
			},
			wantSpans:  3,
			childNames: []string{"chat claude-x", "execute_tool search"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			exp := tracetest.NewInMemoryExporter()
			tp, err := NewTracerProvider(ctx, exp)
			if err != nil {
				t.Fatalf("provider: %v", err)
			}
			if err := Emit(ctx, tp.Tracer("test"), tt.plan); err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if err := tp.ForceFlush(ctx); err != nil {
				t.Fatalf("flush: %v", err)
			}

			spans := exp.GetSpans()
			if len(spans) != tt.wantSpans {
				t.Fatalf("want %d spans, got %d", tt.wantSpans, len(spans))
			}

			idx := byNameUniq(t, spans)

			root, ok := idx["invoke_agent researchbot"]
			if !ok {
				t.Fatal("missing root span")
			}

			// Assert ALL root-span pinned attributes.
			if got := requireAttrString(t, root, AttrOp); got != OpInvokeAgent {
				t.Fatalf("root %s: want %q, got %q", AttrOp, OpInvokeAgent, got)
			}
			if got := requireAttrString(t, root, AttrAgentName); got != "researchbot" {
				t.Fatalf("root %s: want %q, got %q", AttrAgentName, "researchbot", got)
			}
			if got := requireAttrInt(t, root, AttrInTokens); got != int64(tt.plan.Tokens.Input) {
				t.Fatalf("root %s: want %d, got %d", AttrInTokens, tt.plan.Tokens.Input, got)
			}
			if got := requireAttrInt(t, root, AttrOutTokens); got != int64(tt.plan.Tokens.Output) {
				t.Fatalf("root %s: want %d, got %d", AttrOutTokens, tt.plan.Tokens.Output, got)
			}
			if got := requireAttrFloat(t, root, AttrCostUSD); got != tt.plan.CostUSD {
				t.Fatalf("root %s: want %v, got %v", AttrCostUSD, tt.plan.CostUSD, got)
			}

			// Assert every expected child is parented to root.
			for _, childName := range tt.childNames {
				child, ok := idx[childName]
				if !ok {
					t.Fatalf("missing child span %q", childName)
				}
				if child.Parent.SpanID() != root.SpanContext.SpanID() {
					t.Fatalf("span %q: parent=%v, want root %v",
						childName, child.Parent.SpanID(), root.SpanContext.SpanID())
				}
			}

			// Assert ALL pinned attributes on every chat and tool span produced by this plan.
			for _, step := range tt.plan.Steps {
				if c := step.Chat; c != nil {
					spanName := "chat " + c.Model
					chatSpan, ok := idx[spanName]
					if !ok {
						t.Fatalf("missing chat span %q", spanName)
					}
					if got := requireAttrString(t, chatSpan, AttrOp); got != OpChat {
						t.Fatalf("chat span %s: want %q, got %q", AttrOp, OpChat, got)
					}
					if got := requireAttrString(t, chatSpan, AttrModel); got != c.Model {
						t.Fatalf("chat span %s: want %q, got %q", AttrModel, c.Model, got)
					}
					finishReasons := requireAttrStringSlice(t, chatSpan, AttrFinish)
					if len(finishReasons) != 1 || finishReasons[0] != c.Finish {
						t.Fatalf("chat span %s: want [%q], got %v", AttrFinish, c.Finish, finishReasons)
					}
				}
				if tool := step.Tool; tool != nil {
					spanName := "execute_tool " + tool.Name
					toolSpan, ok := idx[spanName]
					if !ok {
						t.Fatalf("missing tool span %q", spanName)
					}
					if got := requireAttrString(t, toolSpan, AttrOp); got != OpExecuteTool {
						t.Fatalf("tool span %s: want %q, got %q", AttrOp, OpExecuteTool, got)
					}
					if got := requireAttrString(t, toolSpan, AttrToolName); got != tool.Name {
						t.Fatalf("tool span %s: want %q, got %q", AttrToolName, tool.Name, got)
					}
					if got := requireAttrString(t, toolSpan, AttrToolArgs); got != tool.Args {
						t.Fatalf("tool span %s: want %q, got %q", AttrToolArgs, tool.Args, got)
					}
					if got := requireAttrString(t, toolSpan, AttrToolResult); got != tool.Result {
						t.Fatalf("tool span %s: want %q, got %q", AttrToolResult, tool.Result, got)
					}
				}
			}
		})
	}
}
