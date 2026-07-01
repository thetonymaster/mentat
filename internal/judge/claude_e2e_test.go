//go:build e2e

package judge

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// e2eModel is the model the live judge tests use. Override with JUDGE_E2E_MODEL
// (e.g. claude-haiku-4-5 for a cheaper run); defaults to the documented default.
func e2eModel() string {
	if m := os.Getenv("JUDGE_E2E_MODEL"); m != "" {
		return m
	}
	return "claude-opus-4-8"
}

// TestClaudeJudge_Live exercises the real Anthropic backend (research Decision 8):
// a paraphrase-but-correct candidate matches; a contradictory candidate does not and
// carries a reason. Requires ANTHROPIC_API_KEY; skipped otherwise.
func TestClaudeJudge_Live(t *testing.T) {
	t.Parallel()
	if os.Getenv(apiKeyEnv) == "" {
		t.Skipf("%s unset; skipping live judge test", apiKeyEnv)
	}

	j, err := NewClaude(config.Config{Judge: config.JudgeConfig{Model: e2eModel()}})
	if err != nil {
		t.Fatalf("NewClaude: %v", err)
	}

	tests := []struct {
		name      string
		candidate string
		expected  string
		wantMatch bool
	}{
		{
			name:      "paraphrase but correct",
			candidate: "The capital of France is Paris.",
			expected:  "Paris is France's capital city.",
			wantMatch: true,
		},
		{
			name:      "contradictory answer",
			candidate: "The capital of France is Berlin.",
			expected:  "Paris is France's capital city.",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			v, err := j.Judge(ctx, core.JudgeRequest{Candidate: tt.candidate, Expected: tt.expected})
			if err != nil {
				t.Fatalf("Judge: %v", err)
			}
			if v.Match != tt.wantMatch {
				t.Fatalf("Match = %v, want %v (reason: %q)", v.Match, tt.wantMatch, v.Reason)
			}
			if !v.Match && v.Reason == "" {
				t.Fatal("no-match verdict must carry a non-empty reason")
			}
		})
	}
}
