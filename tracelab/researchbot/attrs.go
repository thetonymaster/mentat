package researchbot

// Pinned OTel GenAI semantic-convention keys. One place so the emitter, tests,
// and downstream Mentat comparators agree exactly.
const (
	AttrOp         = "gen_ai.operation.name"
	AttrAgentName  = "gen_ai.agent.name"
	AttrModel      = "gen_ai.request.model"
	AttrFinish     = "gen_ai.response.finish_reasons"
	AttrToolName   = "gen_ai.tool.name"
	AttrToolArgs   = "gen_ai.tool.call.arguments"
	AttrToolResult = "gen_ai.tool.call.result"
	AttrInTokens   = "gen_ai.usage.input_tokens"
	AttrOutTokens  = "gen_ai.usage.output_tokens"
	AttrCostUSD    = "gen_ai.usage.cost_usd"

	OpInvokeAgent = "invoke_agent"
	OpChat        = "chat"
	OpExecuteTool = "execute_tool"
)
