package ctl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/engine"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestRunDrivesPrintsSummaryAndSavesLast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1}},
	}
	tr := &trace.Trace{RunID: "run-1", Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "run-1"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, _ := engine.Build(cfg, st, cor)

	var b bytes.Buffer
	ev, err := Run(context.Background(), eng, RunOpts{Target: "bot", Scenario: "happy"}, &b)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ev.Output.Answer != "hi" {
		t.Fatalf("answer = %q", ev.Output.Answer)
	}
	if !strings.Contains(b.String(), "run-1") || !strings.Contains(b.String(), "hi") {
		t.Fatalf("summary missing run id/answer:\n%s", b.String())
	}
	if got, _ := ReadLast(); got != "run-1" {
		t.Fatalf("last not saved: %q", got)
	}
}
