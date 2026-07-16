package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	stubForestByID(st, func(string) (*trace.Trace, error) { return tr, nil })
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
		if !strings.Contains(err.Error(), "ctl: drive target") {
			t.Fatalf("error missing 'ctl: drive target' prefix, got: %v", err)
		}
		if !strings.Contains(err.Error(), "unknown target") {
			t.Fatalf("error missing underlying 'unknown target' message, got: %v", err)
		}
	})

	t.Run("save last error is returned wrapped", func(t *testing.T) {
		// Point HOME at a regular file so os.MkdirAll(HOME/.mentat, …) fails
		// with ENOTDIR — this is privilege-independent and works under root.
		homeFile := filepath.Join(t.TempDir(), "home-file")
		if err := os.WriteFile(homeFile, []byte("x"), 0o644); err != nil {
			t.Fatalf("setup HOME file: %v", err)
		}
		t.Setenv("HOME", homeFile)

		eng := buildTestEngine(t, runID, tr)

		var b bytes.Buffer
		_, err := Run(context.Background(), eng, RunOpts{Target: "bot"}, &b)
		if err == nil {
			t.Fatal("expected error from SaveLast with file-as-HOME, got nil")
		}
		if !strings.Contains(err.Error(), "ctl: save last") {
			t.Fatalf("error missing 'ctl: save last' prefix, got: %v", err)
		}
	})

	t.Run("quiet write error is returned wrapped", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		_, err := Run(context.Background(), eng, RunOpts{Target: "bot", Quiet: true}, errWriter{})
		if err == nil {
			t.Fatal("expected error from failing writer in quiet mode, got nil")
		}
		if !strings.Contains(err.Error(), "ctl: write answer") {
			t.Fatalf("error missing 'ctl: write answer' prefix, got: %v", err)
		}
	})

	t.Run("default write error is returned wrapped", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		_, err := Run(context.Background(), eng, RunOpts{Target: "bot"}, errWriter{})
		if err == nil {
			t.Fatal("expected error from failing writer in default mode, got nil")
		}
		if !strings.Contains(err.Error(), "ctl: write") {
			t.Fatalf("error missing 'ctl: write' prefix, got: %v", err)
		}
	})

	t.Run("output flag writes answer-only to file alongside the stdout summary", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		outPath := filepath.Join(t.TempDir(), "answer.txt")
		var b bytes.Buffer
		if _, err := Run(context.Background(), eng, RunOpts{Target: "bot", Output: outPath}, &b); err != nil {
			t.Fatalf("Run: %v", err)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read output file: %v", err)
		}
		if string(data) != "hi\n" {
			t.Fatalf("output file = %q, want %q (answer + newline only)", data, "hi\n")
		}
		// -o is additive: the full stdout summary is still produced.
		if !strings.Contains(b.String(), runID) {
			t.Fatalf("stdout summary missing run id with -o set:\n%s", b.String())
		}
	})

	t.Run("output flag unwritable target is a hard error", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		eng := buildTestEngine(t, runID, tr)

		badPath := filepath.Join(t.TempDir(), "no-such-dir", "answer.txt")
		var b bytes.Buffer
		_, err := Run(context.Background(), eng, RunOpts{Target: "bot", Output: badPath}, &b)
		if err == nil {
			t.Fatal("expected error writing answer to a nonexistent directory, got nil")
		}
		if !strings.Contains(err.Error(), "ctl: write answer file") {
			t.Fatalf("error missing 'ctl: write answer file' prefix, got: %v", err)
		}
	})
}

// canonicalEvidence is the fixed Evidence the run-golden.txt contract renders
// from: known run id, tools, span count, answer, tokens, cost, latency and a
// two-trace forest (multi-root, invariant §2). Its rendered summary is committed
// as the golden and must stay byte-stable.
func canonicalEvidence() core.Evidence {
	base := time.Unix(1700000000, 0)
	root1 := &trace.Span{
		ID:    "r1",
		Name:  "invoke_agent",
		Start: base,
		End:   base.Add(1500 * time.Millisecond),
		Attrs: map[string]string{
			genai.Op:        genai.OpInvokeAgent,
			genai.InTokens:  "1200",
			genai.OutTokens: "340",
			genai.CostUSD:   "0.0105",
		},
	}
	tool1 := &trace.Span{
		ID: "t1", ParentID: "r1", Name: "execute_tool",
		Start: base.Add(100 * time.Millisecond), End: base.Add(200 * time.Millisecond),
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"},
	}
	tool2 := &trace.Span{
		ID: "t2", ParentID: "r1", Name: "execute_tool",
		Start: base.Add(300 * time.Millisecond), End: base.Add(400 * time.Millisecond),
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "fetch"},
	}
	root2 := &trace.Span{
		ID: "r2", Name: "invoke_agent",
		Start: base.Add(500 * time.Millisecond), End: base.Add(600 * time.Millisecond),
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent},
	}
	return core.Evidence{
		RunID: "run-canonical",
		Trace: &trace.Trace{
			RunID:    "run-canonical",
			TraceIDs: []string{"trace-aaa", "trace-bbb"},
			Roots:    []*trace.Span{root1, root2},
			Spans:    []*trace.Span{root1, tool1, tool2, root2},
		},
		Output: core.Output{Answer: "The canonical answer."},
	}
}

// TestRenderSummaryGolden asserts the enriched summary equals the committed
// golden byte-for-byte (US7 additive-lines contract, T001/T023).
func TestRenderSummaryGolden(t *testing.T) {
	got, err := RenderSummary(canonicalEvidence(), nil)
	if err != nil {
		t.Fatalf("RenderSummary: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "run-golden.txt"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("summary != golden\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}

// TestRenderSummaryExistingLinesByteStable pins the byte-stability contract: the
// four pre-US7 lines (run/tools/spans/answer) are unchanged by a single byte,
// reproduced here from the exact legacy format strings and asserted as a prefix.
func TestRenderSummaryExistingLinesByteStable(t *testing.T) {
	ev := canonicalEvidence()
	got, err := RenderSummary(ev, nil)
	if err != nil {
		t.Fatalf("RenderSummary: %v", err)
	}
	prefix := fmt.Sprintf("run %s\ntools: %v\nspans: %d\nanswer: %s\n",
		ev.RunID, toolNames(ev), len(ev.Trace.Spans), ev.Output.Answer)
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("enriched summary must start with byte-identical pre-US7 lines\nwant prefix:\n%q\ngot:\n%q", prefix, got)
	}
}

// TestRenderSummaryDerivationErrors proves a malformed metric surfaces a wrapped
// error rather than a fabricated value (no silent fallback, invariant IV).
func TestRenderSummaryDerivationErrors(t *testing.T) {
	tests := []struct {
		name    string
		ev      core.Evidence
		wantSub string
	}{
		{
			name:    "malformed tokens",
			ev:      core.Evidence{RunID: "x", Trace: &trace.Trace{Spans: []*trace.Span{{Name: "s", Attrs: map[string]string{genai.InTokens: "abc"}}}}},
			wantSub: "tokens",
		},
		{
			name:    "malformed cost",
			ev:      core.Evidence{RunID: "x", Trace: &trace.Trace{Spans: []*trace.Span{{Name: "s", Attrs: map[string]string{genai.CostUSD: "notanumber"}}}}},
			wantSub: "cost",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RenderSummary(tt.ev, nil)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q missing substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}
