package researchbot

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Emit replays a plan into a span tree. Children are started from the root's
// context so they are parented to it; sequential start order makes tool/chat
// ordering deterministic.
func Emit(ctx context.Context, tr trace.Tracer, p *Plan) error {
	if p == nil {
		return fmt.Errorf("emit: plan is nil")
	}
	ctx, root := tr.Start(ctx, "invoke_agent researchbot", trace.WithAttributes(
		attribute.String(AttrOp, OpInvokeAgent),
		attribute.String(AttrAgentName, "researchbot"),
		attribute.Int(AttrInTokens, p.Tokens.Input),
		attribute.Int(AttrOutTokens, p.Tokens.Output),
		attribute.Float64(AttrCostUSD, p.CostUSD),
	))
	defer root.End()

	for i, s := range p.Steps {
		switch {
		case s.Chat != nil && s.Tool != nil:
			return fmt.Errorf("emit: step %d has both chat and tool", i)
		case s.Chat != nil:
			_, sp := tr.Start(ctx, "chat "+s.Chat.Model, trace.WithAttributes(
				attribute.String(AttrOp, OpChat),
				attribute.String(AttrModel, s.Chat.Model),
				attribute.StringSlice(AttrFinish, []string{s.Chat.Finish}),
			))
			sp.End()
		case s.Tool != nil:
			_, sp := tr.Start(ctx, "execute_tool "+s.Tool.Name, trace.WithAttributes(
				attribute.String(AttrOp, OpExecuteTool),
				attribute.String(AttrToolName, s.Tool.Name),
				attribute.String(AttrToolArgs, s.Tool.Args),
				attribute.String(AttrToolResult, s.Tool.Result),
			))
			sp.End()
		default:
			return fmt.Errorf("emit: step %d has neither chat nor tool", i)
		}
	}
	return nil
}
