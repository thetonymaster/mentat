package ctl

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
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

func writeTempFeature(t *testing.T, body string) string {
	t.Helper()
	p := t.TempDir() + "/f.feature"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
