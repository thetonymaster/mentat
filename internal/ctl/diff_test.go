package ctl

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func toolForest(run string, tools ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	for i, name := range tools {
		s := &trace.Span{ID: run + string(rune('a'+i)),
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: name}}
		tr.Spans = append(tr.Spans, s)
	}
	return tr
}

func newTestCorrelator() core.Correlator {
	return correlate.New(func() string { return "" }, correlate.PollConfig{
		Interval:  time.Millisecond,
		StableFor: 1,
		Timeout:   time.Second,
	})
}

func TestDiffMarksDifferingPositions(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			if id == "A" {
				return toolForest("A", "search", "summarize"), nil
			}
			return toolForest("B", "search", "delete_record"), nil
		}).AnyTimes()
	cor := newTestCorrelator()

	var b bytes.Buffer
	if err := Diff(context.Background(), cor, st, "A", "B", &b); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(b.String(), "summarize") || !strings.Contains(b.String(), "delete_record") {
		t.Fatalf("diff did not surface the differing tools:\n%s", b.String())
	}
}

func TestDiffIdenticalSequences(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			return toolForest(id, "search", "summarize"), nil
		}).AnyTimes()
	cor := newTestCorrelator()

	var b bytes.Buffer
	if err := Diff(context.Background(), cor, st, "X", "Y", &b); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(b.String(), "identical") {
		t.Fatalf("expected 'identical' in output for matching sequences:\n%s", b.String())
	}
}

func TestDiffDifferentLengths(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			if id == "short" {
				return toolForest(id, "search"), nil
			}
			return toolForest(id, "search", "summarize", "finalize"), nil
		}).AnyTimes()
	cor := newTestCorrelator()

	var b bytes.Buffer
	if err := Diff(context.Background(), cor, st, "short", "long", &b); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	// "—" padding character should appear for the shorter side
	if !strings.Contains(b.String(), "—") {
		t.Fatalf("diff did not show padding '—' for different-length sequences:\n%s", b.String())
	}
}

func TestDiffErrorOnFirstResolve(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return nil, nil // returns zero refs → resolve will timeout with no spans
		}).AnyTimes()
	cor := correlate.New(func() string { return "" }, correlate.PollConfig{
		Interval:  time.Millisecond,
		StableFor: 1,
		Timeout:   5 * time.Millisecond,
	})

	var b bytes.Buffer
	err := Diff(context.Background(), cor, st, "missing-A", "B", &b)
	if err == nil {
		t.Fatal("expected error when first run cannot be resolved, got nil")
	}
	if !strings.Contains(err.Error(), "diff: run missing-A") {
		t.Fatalf("error does not name the failing run: %v", err)
	}
}
