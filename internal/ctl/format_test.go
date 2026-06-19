package ctl

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// formatErrWriter always fails on Write (reuse pattern from run_test.go).
type formatErrWriter struct{}

func (formatErrWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write: disk full")
}

func sampleForest() *trace.Trace {
	root := &trace.Span{ID: "1", Name: "invoke_agent researchbot",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent, genai.InTokens: "1200", genai.OutTokens: "600"}}
	t1 := &trace.Span{ID: "2", ParentID: "1", Name: "execute_tool search",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "search"}}
	t2 := &trace.Span{ID: "3", ParentID: "1", Name: "execute_tool summarize",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: "summarize"}}
	return &trace.Trace{RunID: "r1", Roots: []*trace.Span{root}, Spans: []*trace.Span{root, t1, t2}}
}

func TestFormatForest(t *testing.T) {
	tests := []struct {
		name     string
		tr       *trace.Trace
		wantSubs []string
	}{
		{
			name:     "nil trace prints marker",
			tr:       nil,
			wantSubs: []string{"(no trace)"},
		},
		{
			name:     "empty trace prints header with zero counts",
			tr:       &trace.Trace{RunID: "r0"},
			wantSubs: []string{"r0", "0 spans"},
		},
		{
			name: "happy path shows root, children, and token counts",
			tr:   sampleForest(),
			wantSubs: []string{
				"invoke_agent researchbot",
				"execute_tool search",
				"execute_tool summarize",
				"in=1200",
				"out=600",
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FormatForest panicked: %v", r)
				}
			}()
			var b bytes.Buffer
			if err := FormatForest(tt.tr, &b); err != nil {
				t.Fatalf("FormatForest returned unexpected error: %v", err)
			}
			out := b.String()
			for _, want := range tt.wantSubs {
				if !strings.Contains(out, want) {
					t.Fatalf("FormatForest output missing %q in:\n%s", want, out)
				}
			}
		})
	}

	t.Run("write error on nil trace is returned", func(t *testing.T) {
		err := FormatForest(nil, formatErrWriter{})
		if err == nil {
			t.Fatal("expected error from failing writer, got nil")
		}
		if !strings.Contains(err.Error(), "ctl:") {
			t.Fatalf("error missing 'ctl:' prefix, got: %v", err)
		}
	})

	t.Run("write error on forest header is returned", func(t *testing.T) {
		err := FormatForest(sampleForest(), formatErrWriter{})
		if err == nil {
			t.Fatal("expected error from failing writer, got nil")
		}
		if !strings.Contains(err.Error(), "ctl:") {
			t.Fatalf("error missing 'ctl:' prefix, got: %v", err)
		}
	})
}

func TestFormatTools(t *testing.T) {
	tests := []struct {
		name     string
		tr       *trace.Trace
		wantSubs []string
	}{
		{
			name:     "nil trace prints marker",
			tr:       nil,
			wantSubs: []string{"(no trace)"},
		},
		{
			name:     "empty trace prints zero tool calls",
			tr:       &trace.Trace{RunID: "r0"},
			wantSubs: []string{"r0", "0 tool call"},
		},
		{
			name: "happy path lists tools in sequence",
			tr:   sampleForest(),
			wantSubs: []string{
				"1. search",
				"2. summarize",
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FormatTools panicked: %v", r)
				}
			}()
			var b bytes.Buffer
			if err := FormatTools(tt.tr, &b); err != nil {
				t.Fatalf("FormatTools returned unexpected error: %v", err)
			}
			out := b.String()
			for _, want := range tt.wantSubs {
				if !strings.Contains(out, want) {
					t.Fatalf("FormatTools output missing %q in:\n%s", want, out)
				}
			}
		})
	}

	t.Run("write error on nil trace is returned", func(t *testing.T) {
		err := FormatTools(nil, formatErrWriter{})
		if err == nil {
			t.Fatal("expected error from failing writer, got nil")
		}
		if !strings.Contains(err.Error(), "ctl:") {
			t.Fatalf("error missing 'ctl:' prefix, got: %v", err)
		}
	})

	t.Run("write error on tools header is returned", func(t *testing.T) {
		err := FormatTools(sampleForest(), formatErrWriter{})
		if err == nil {
			t.Fatal("expected error from failing writer, got nil")
		}
		if !strings.Contains(err.Error(), "ctl:") {
			t.Fatalf("error missing 'ctl:' prefix, got: %v", err)
		}
	})
}

// serviceForest builds a trace where each span carries a service.name resource
// attr and a real Start, so ServiceSequence orders them first-seen.
func serviceForest(run string, services ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	base := time.Unix(0, 0)
	for i, name := range services {
		s := &trace.Span{
			ID:    run + string(rune('a'+i)),
			Name:  "POST",
			Start: base.Add(time.Duration(i) * time.Millisecond),
			Attrs: map[string]string{"service.name": name},
		}
		tr.Spans = append(tr.Spans, s)
	}
	return tr
}

func TestFormatServices(t *testing.T) {
	tests := []struct {
		name     string
		tr       *trace.Trace
		wantSubs []string
		wantErr  bool
	}{
		{
			name:     "nil trace prints marker",
			tr:       nil,
			wantSubs: []string{"(no trace)"},
		},
		{
			name:     "lists distinct services in first-seen order",
			tr:       serviceForest("r1", "auth", "inventory", "payment", "notify"),
			wantSubs: []string{"4 service call", "1. auth", "2. inventory", "3. payment", "4. notify"},
		},
		{
			name:    "span missing service.name is a hard error",
			tr:      &trace.Trace{RunID: "r2", Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{}}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			err := FormatServices(tt.tr, &b)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FormatServices err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(b.String(), want) {
					t.Fatalf("output missing %q in:\n%s", want, b.String())
				}
			}
		})
	}
}
