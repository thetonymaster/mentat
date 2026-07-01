package comparator

import (
	"context"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestRetriesName(t *testing.T) {
	t.Parallel()
	if got := NewRetries().Name(); got != "retries" {
		t.Fatalf("Name() = %q, want %q", got, "retries")
	}
}

func TestRetriesCompare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ev         core.Evidence
		exp        core.Expectation
		wantPass   bool
		wantErr    bool
		wantReason string // substring asserted on a failing Verdict
	}{
		{
			name:     "under ceiling passes",
			ev:       core.Evidence{Trace: toolTrace("search", "fetch_doc", "search")},
			exp:      RetriesExpectation{Tool: "search", Max: 2},
			wantPass: true,
		},
		{
			name:     "at ceiling passes",
			ev:       core.Evidence{Trace: toolTrace("search", "search")},
			exp:      RetriesExpectation{Tool: "search", Max: 2},
			wantPass: true,
		},
		{
			name:       "over ceiling fails naming tool count and ceiling",
			ev:         core.Evidence{Trace: toolTrace("search", "search", "search")},
			exp:        RetriesExpectation{Tool: "search", Max: 2},
			wantPass:   false,
			wantReason: `tool "search" was called 3 times, exceeding the maximum of 2`,
		},
		{
			name:     "only the named tool counts toward the ceiling",
			ev:       core.Evidence{Trace: toolTrace("search", "fetch_doc", "fetch_doc", "fetch_doc")},
			exp:      RetriesExpectation{Tool: "search", Max: 1},
			wantPass: true,
		},
		{
			name:     "Max=0 with tool absent passes",
			ev:       core.Evidence{Trace: toolTrace("fetch_doc", "summarize")},
			exp:      RetriesExpectation{Tool: "search", Max: 0},
			wantPass: true,
		},
		{
			name:       "Max=0 with single invocation fails",
			ev:         core.Evidence{Trace: toolTrace("search")},
			exp:        RetriesExpectation{Tool: "search", Max: 0},
			wantPass:   false,
			wantReason: `tool "search" was called 1 times, exceeding the maximum of 0`,
		},
		{
			name:    "execute_tool span missing tool name returns error",
			ev:      core.Evidence{Trace: toolTraceMissingName()},
			exp:     RetriesExpectation{Tool: "search", Max: 1},
			wantErr: true,
		},
		{
			name:    "nil Trace returns error",
			ev:      core.Evidence{Trace: nil},
			exp:     RetriesExpectation{Tool: "search", Max: 1},
			wantErr: true,
		},
		{
			name:    "wrong expectation type string returns error",
			ev:      core.Evidence{Trace: toolTrace("search")},
			exp:     "not a RetriesExpectation",
			wantErr: true,
		},
		{
			name:    "wrong expectation type int returns error",
			ev:      core.Evidence{Trace: toolTrace("search")},
			exp:     42,
			wantErr: true,
		},
		{
			name:    "wrong expectation type nil returns error",
			ev:      core.Evidence{Trace: toolTrace("search")},
			exp:     nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewRetries().Compare(context.Background(), tt.ev, tt.exp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if got.Pass {
					t.Fatalf("wantErr=true but got Pass=true; err=%v", err)
				}
				return
			}
			if got.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v; reasons = %v", got.Pass, tt.wantPass, got.Reasons)
			}
			if tt.wantReason != "" {
				joined := strings.Join(got.Reasons, "; ")
				if !strings.Contains(joined, tt.wantReason) {
					t.Fatalf("reasons %q do not contain %q", joined, tt.wantReason)
				}
			}
		})
	}
}
