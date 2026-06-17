// Package genai holds the OTel GenAI attribute keys Mentat reads. Single source
// of truth; mirrors the values researchbot emits.
package genai

const (
	Op        = "gen_ai.operation.name"
	ToolName  = "gen_ai.tool.name"
	InTokens  = "gen_ai.usage.input_tokens"
	OutTokens = "gen_ai.usage.output_tokens"
	CostUSD   = "gen_ai.usage.cost_usd"

	OpInvokeAgent = "invoke_agent"
	OpChat        = "chat"
	OpExecuteTool = "execute_tool"
)
