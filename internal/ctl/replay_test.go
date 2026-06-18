package ctl

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestReplayFeatureEvaluatesStoredRunWithoutDriving(t *testing.T) {
	// A target whose command would FAIL if driven — proving replay does NOT drive.
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"false"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "r"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(sampleForest(), nil).AnyTimes()
	cor := correlate.New(func() string { return "r" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, _ := engine.Build(cfg, st, cor)

	feature := writeTempFeature(t, `Feature: replay
  Scenario: stored run had the right tools
    Given the agent target "bot"
    When I run scenario "ignored"
    Then the agent calls tools in order:
      | search    |
      | summarize |
`)
	var b bytes.Buffer
	if err := ReplayFeature(context.Background(), eng, "r", feature, "", &b); err != nil {
		t.Fatalf("replay should pass against the stored forest: %v\n%s", err, b.String())
	}
}

// mismatchForest returns a trace whose tool sequence does not include "summarize".
func mismatchForest(runID string) *trace.Trace {
	root := &trace.Span{ID: "1", Name: "invoke_agent researchbot",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
	t1 := &trace.Span{ID: "2", ParentID: "1", Name: "execute_tool search",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"}}
	t2 := &trace.Span{ID: "3", ParentID: "1", Name: "execute_tool analyze",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "analyze"}}
	return &trace.Trace{RunID: runID, Roots: []*trace.Span{root}, Spans: []*trace.Span{root, t1, t2}}
}

func TestReplayFeatureFailsWhenStoredRunDoesNotSatisfyFeature(t *testing.T) {
	tests := []struct {
		name          string
		runID         string
		wantErrSubstr string
	}{
		{
			name:          "mismatched tool order causes non-zero status and error containing run id",
			runID:         "stored-run-42",
			wantErrSubstr: "stored-run-42",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "x",
				Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"false"}, MaxConcurrency: 1}},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: tt.runID}}, nil).AnyTimes()
			st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(mismatchForest(tt.runID), nil).AnyTimes()
			cor := correlate.New(func() string { return tt.runID }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
			eng, _ := engine.Build(cfg, st, cor)

			// Feature asserts "summarize" — the stored forest has "analyze" instead.
			feature := writeTempFeature(t, `Feature: replay mismatch
  Scenario: stored run had wrong tools
    Given the agent target "bot"
    When I run scenario "ignored"
    Then the agent calls tools in order:
      | search    |
      | summarize |
`)
			var b bytes.Buffer
			err := ReplayFeature(context.Background(), eng, tt.runID, feature, "", &b)
			if err == nil {
				t.Fatalf("expected ReplayFeature to return error for mismatched tools, got nil\n%s", b.String())
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Fatalf("error %q does not contain run id %q", err.Error(), tt.wantErrSubstr)
			}
		})
	}
}

func TestReplayFeatureRejectsEmptyRunID(t *testing.T) {
	tests := []struct {
		name          string
		runID         string
		wantErrSubstr string
	}{
		{
			name:          "empty run id returns error before any engine interaction",
			runID:         "",
			wantErrSubstr: "run id is required",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			// No expectations on store — any call would fail the controller.
			cor := mocks.NewMockCorrelator(ctrl)
			// No expectations on correlator — any call would fail the controller.
			cfg := config.Config{
				OTLPEndpoint: "x",
				Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"false"}, MaxConcurrency: 1}},
			}
			eng, err := engine.Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("engine.Build: %v", err)
			}
			feature := writeTempFeature(t, "Feature: noop\n  Scenario: noop\n    Given the agent target \"bot\"\n")
			var b bytes.Buffer
			err = ReplayFeature(context.Background(), eng, tt.runID, feature, "", &b)
			if err == nil {
				t.Fatalf("expected error for empty run id, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSubstr)
			}
		})
	}
}

func writeTempFeature(t *testing.T, body string) string {
	t.Helper()
	p := t.TempDir() + "/f.feature"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
