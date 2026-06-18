package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/thetonymaster/mentat/internal/trace"
)

func chmodReadOnly(dir string) error {
	return os.Chmod(dir, 0o555)
}

// errWriter is an io.Writer that always returns an error on Write.
type errWriter struct{}

func (errWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write: disk full")
}

// buildTestEngine builds a minimal Engine backed by a mock TraceStore that
// returns tr for any query.  The correlator uses a fixed run id of runID.
func buildTestEngine(t *testing.T, runID string, tr *trace.Trace) *engine.Engine {
	t.Helper()
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: runID}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
	cor := correlate.New(func() string { return runID },
		correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}
	return eng
}

func TestRun(t *testing.T) {
	const runID = "run-1"
	tr := &trace.Trace{
		RunID: runID,
		Spans: []*trace.Span{{Name: "root"}},
		Roots: []*trace.Span{{Name: "root"}},
	}

	t.Run("default summary contains run id and answer", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		var b bytes.Buffer
		ev, err := Run(context.Background(), eng, RunOpts{Target: "bot"}, &b)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if ev.Output.Answer != "hi" {
			t.Fatalf("answer = %q, want %q", ev.Output.Answer, "hi")
		}
		out := b.String()
		if !strings.Contains(out, runID) {
			t.Fatalf("summary missing run id:\n%s", out)
		}
		if !strings.Contains(out, "hi") {
			t.Fatalf("summary missing answer:\n%s", out)
		}
		got, rerr := ReadLast()
		if rerr != nil {
			t.Fatalf("ReadLast: %v", rerr)
		}
		if got != runID {
			t.Fatalf("last not saved: got %q want %q", got, runID)
		}
	})

	t.Run("quiet mode outputs only answer", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		var b bytes.Buffer
		ev, err := Run(context.Background(), eng, RunOpts{Target: "bot", Quiet: true}, &b)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if ev.Output.Answer != "hi" {
			t.Fatalf("answer = %q", ev.Output.Answer)
		}
		out := strings.TrimSpace(b.String())
		if out != "hi" {
			t.Fatalf("quiet output = %q, want %q", out, "hi")
		}
		// quiet output must NOT contain the run id prefix
		if strings.Contains(out, "run") {
			t.Fatalf("quiet output unexpectedly contains run prefix: %q", out)
		}
	})

	t.Run("json mode outputs valid JSON with required fields", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		var b bytes.Buffer
		ev, err := Run(context.Background(), eng, RunOpts{Target: "bot", JSON: true}, &b)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if ev.Output.Answer != "hi" {
			t.Fatalf("answer = %q", ev.Output.Answer)
		}
		var got map[string]any
		if err := json.Unmarshal(b.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\noutput: %s", err, b.String())
		}
		if got["runID"] != runID {
			t.Fatalf("json runID = %v, want %q", got["runID"], runID)
		}
		if got["answer"] != "hi" {
			t.Fatalf("json answer = %v, want %q", got["answer"], "hi")
		}
		if _, ok := got["tools"]; !ok {
			t.Fatal("json missing 'tools' field")
		}
		if _, ok := got["spans"]; !ok {
			t.Fatal("json missing 'spans' field")
		}
	})

	t.Run("json mode encode error is returned wrapped", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		_, err := Run(context.Background(), eng, RunOpts{Target: "bot", JSON: true}, errWriter{})
		if err == nil {
			t.Fatal("expected error from errWriter, got nil")
		}
		if !strings.Contains(err.Error(), "ctl: encode json") {
			t.Fatalf("error missing expected prefix, got: %v", err)
		}
	})

	t.Run("drive error propagates", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		// Use an unknown target to make Drive return an error immediately.
		ctrl := gomock.NewController(t)
		st := mocks.NewMockTraceStore(ctrl)
		cor := correlate.New(func() string { return runID },
			correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
		cfg := config.Config{
			OTLPEndpoint: "x",
			Targets: map[string]config.Target{
				"bot": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
			},
		}
		eng, err := engine.Build(cfg, st, cor)
		if err != nil {
			t.Fatalf("engine.Build: %v", err)
		}

		var b bytes.Buffer
		_, err = Run(context.Background(), eng, RunOpts{Target: "unknown-target"}, &b)
		if err == nil {
			t.Fatal("expected error for unknown target, got nil")
		}
		if !strings.Contains(err.Error(), "unknown target") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("save last error is returned wrapped", func(t *testing.T) {
		// Make HOME a read-only directory so MkdirAll inside SaveLast fails.
		roDir := t.TempDir()
		// chmod read-only after creation
		if err := chmodReadOnly(roDir); err != nil {
			t.Skipf("cannot make dir read-only: %v", err)
		}
		t.Setenv("HOME", roDir)

		eng := buildTestEngine(t, runID, tr)

		var b bytes.Buffer
		_, err := Run(context.Background(), eng, RunOpts{Target: "bot"}, &b)
		if err == nil {
			t.Fatal("expected error from read-only SaveLast, got nil")
		}
		if !strings.Contains(err.Error(), "ctl: save last") {
			t.Fatalf("error missing 'ctl: save last' prefix, got: %v", err)
		}
	})
}
