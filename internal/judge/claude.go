// Package judge holds the Judge backends behind the core.Judge seam. It is the
// only package permitted to import the Anthropic SDK, keeping the comparator layer
// transport-free (Constitution I). The default backend, claudeJudge, asks Claude
// whether a candidate string means an expected meaning and returns a structured
// verdict — never a guessed one (Constitution IV).
package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// apiKeyEnv is the environment variable the Anthropic SDK reads for credentials.
// The judge checks it before issuing a request so a missing key surfaces as a hard
// error before any model call (US2-AC3), not as an opaque 401.
const apiKeyEnv = "ANTHROPIC_API_KEY"

// verdictMaxTokens caps the response. The verdict is a tiny JSON object, so a small
// budget is enough and keeps latency/cost down (research Decision 4).
const verdictMaxTokens = 1024

// claudeJudge is the Claude-backed core.Judge. It holds an immutable SDK client,
// the configured model, and the temperature to apply on models that accept it.
type claudeJudge struct {
	client      anthropic.Client
	model       string
	temperature float64
}

// NewClaude builds the Claude judge from config. It MUST NOT require or validate the
// API key here: engine.Build wires the judge unconditionally, so construction must
// succeed in environments without a key when no semantic scenario runs (judge-seam.md).
// The key is checked at the first Judge call.
func NewClaude(cfg config.Config) (core.Judge, error) {
	return newClaudeWithClient(anthropic.NewClient(), cfg.Judge.Model, cfg.Judge.Temperature), nil
}

// newClaudeWithClient is the test seam: it injects a pre-built SDK client (e.g. one
// pointed at an httptest.Server via option.WithBaseURL) so hermetic tests exercise
// the request/response/error mapping without a live backend.
func newClaudeWithClient(client anthropic.Client, model string, temperature float64) *claudeJudge {
	return &claudeJudge{client: client, model: model, temperature: temperature}
}

// verdictSchema is the JSON schema for the structured output: {match,reason}, both
// required, no extras (research Decision 3).
var verdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"match":  map[string]any{"type": "boolean"},
		"reason": map[string]any{"type": "string"},
	},
	"required":             []string{"match", "reason"},
	"additionalProperties": false,
}

// Judge renders one semantic verdict over req.Candidate vs req.Expected.
func (j *claudeJudge) Judge(ctx context.Context, req core.JudgeRequest) (core.JudgeVerdict, error) {
	if strings.TrimSpace(os.Getenv(apiKeyEnv)) == "" {
		return core.JudgeVerdict{}, fmt.Errorf("judge: %s is unset; the Claude judge backend requires an API key", apiKeyEnv)
	}
	// A configured temperature on a model that rejects the knob (Opus-tier/Fable)
	// must not be silently dropped (Constitution IV: no silent fallbacks). Fail loud
	// here — symmetric with the API-key pre-flight — so it only fires when the judge
	// is actually invoked, never at engine.Build construction time.
	if j.temperature > 0 && !acceptsTemperature(j.model) {
		return core.JudgeVerdict{}, fmt.Errorf("judge.temperature=%v is set but model %q does not accept a temperature parameter (Opus-tier/Fable reject it); remove judge.temperature or use a Sonnet/Haiku model", j.temperature, j.model)
	}

	params := anthropic.MessageNewParams{
		Model:     j.model,
		MaxTokens: verdictMaxTokens,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildPrompt(req))),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: verdictSchema},
		},
	}
	// Thinking disabled: with structured output the response is the verdict JSON, so
	// reasoning tokens add only latency (research Decision 4). Fable/Mythos are
	// always-on-thinking and reject an explicit thinking:disabled with HTTP 400, so
	// send the param only on models that accept it; otherwise leave it unset.
	if acceptsDisabledThinking(j.model) {
		disabled := anthropic.NewThinkingConfigDisabledParam()
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	}
	// temperature is rejected (HTTP 400) on Opus-tier / Fable; send it only on models
	// that accept it (research Decision 4).
	if acceptsTemperature(j.model) {
		params.Temperature = anthropic.Float(j.temperature)
	}

	msg, err := j.client.Messages.New(ctx, params)
	if err != nil {
		return core.JudgeVerdict{}, classifyErr(err)
	}

	if msg.StopReason == anthropic.StopReasonRefusal {
		return core.JudgeVerdict{}, fmt.Errorf("judge: model refused to render a verdict (stop_reason=%q)", msg.StopReason)
	}

	for _, block := range msg.Content {
		if block.Type != "text" {
			continue
		}
		var out struct {
			Match  bool   `json:"match"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(block.Text), &out); err != nil {
			return core.JudgeVerdict{}, fmt.Errorf("judge: parsing structured verdict %q: %w", block.Text, err)
		}
		// Record the call's token usage against the CONFIGURED model (j.model), which
		// is what the ledger prices — not the response's echoed model id (US6). Calls=1:
		// one backend call, one ledger row.
		return core.JudgeVerdict{
			Match:  out.Match,
			Reason: out.Reason,
			Usage: core.JudgeUsage{
				Calls:        1,
				InputTokens:  msg.Usage.InputTokens,
				OutputTokens: msg.Usage.OutputTokens,
				Model:        j.model,
			},
		}, nil
	}
	return core.JudgeVerdict{}, fmt.Errorf("judge: response had no text content block to parse a verdict from")
}

// buildPrompt instructs the judge to decide whether Candidate means Expected and to
// return the {match,reason} verdict with a non-empty reason when match is false.
func buildPrompt(req core.JudgeRequest) string {
	return fmt.Sprintf(
		"You are a strict semantic judge. Decide whether the Candidate answer means the "+
			"same thing as the Expected meaning — judge meaning, not wording.\n\n"+
			"Expected meaning:\n%s\n\nCandidate answer:\n%s\n\n"+
			"Return a JSON object with fields \"match\" (true if the Candidate means the "+
			"Expected, false otherwise) and \"reason\" (a brief rationale; it MUST be "+
			"non-empty whenever match is false).",
		req.Expected, req.Candidate,
	)
}

// temperatureAcceptingFamilies are the Claude model families known to accept the
// temperature knob. Matched as substrings (not pinned version IDs) so new point
// releases within a family keep working without a code change (research Decision 4).
var temperatureAcceptingFamilies = []string{"sonnet", "haiku"}

// acceptsTemperature reports whether the model accepts the temperature knob. It is a
// deliberate allowlist: sending temperature to a rejecting model is a hard HTTP 400,
// while omitting it on an accepting model merely uses that model's default. So an
// unknown/unlisted model OMITS temperature by design — the safe direction. Opus-tier
// and Fable are absent and therefore reject it (research Decision 4).
func acceptsTemperature(model string) bool {
	for _, family := range temperatureAcceptingFamilies {
		if strings.Contains(model, family) {
			return true
		}
	}
	return false
}

// disabledThinkingFamilies are the Claude model families that accept an explicit
// thinking:{type:"disabled"}. Fable/Mythos are always-on-thinking and return HTTP 400
// on it, so they are absent and thus OMIT the param — the safe direction (omitting
// merely uses the model's default adaptive thinking, never a hard error). Matched as
// substrings, mirroring temperatureAcceptingFamilies. Kept SEPARATE from that list
// because Opus accepts disabled-thinking but rejects temperature.
var disabledThinkingFamilies = []string{"opus", "sonnet", "haiku"}

// acceptsDisabledThinking reports whether the model accepts thinking:{type:"disabled"}.
// It is a deliberate allowlist: sending it to a rejecting model (Fable/Mythos) is a
// hard HTTP 400, while omitting it merely uses that model's default adaptive thinking.
// So an unknown/unlisted model OMITS the param by design — the safe direction.
func acceptsDisabledThinking(model string) bool {
	for _, family := range disabledThinkingFamilies {
		if strings.Contains(model, family) {
			return true
		}
	}
	return false
}

// classifyErr maps an SDK call error to a descriptive, %w-wrapped error naming the
// cause (Constitution IV / FR-007). It never yields a verdict.
func classifyErr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("judge: request context error: %w", err)
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401:
			return fmt.Errorf("judge: authentication failed (HTTP 401), check %s: %w", apiKeyEnv, err)
		case apiErr.StatusCode == 429:
			return fmt.Errorf("judge: rate limit exceeded after SDK retries (HTTP 429): %w", err)
		case apiErr.StatusCode >= 500:
			return fmt.Errorf("judge: backend error (HTTP %d): %w", apiErr.StatusCode, err)
		default:
			return fmt.Errorf("judge: API error (HTTP %d): %w", apiErr.StatusCode, err)
		}
	}
	return fmt.Errorf("judge: calling Anthropic API: %w", err)
}
