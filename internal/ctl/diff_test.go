package ctl

import (
	"bytes"
	"context"
	"errors"
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

func newFastCorrelator() core.Correlator {
	return correlate.New(func() string { return "" }, correlate.PollConfig{
		Interval:  time.Millisecond,
		StableFor: 1,
		Timeout:   5 * time.Millisecond,
	})
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name       string
		idA        string
		idB        string
		useFast    bool // use short-timeout correlator (for error cases)
		setupMock  func(st *mocks.MockTraceStore)
		wantErr    bool
		wantErrSub string
		wantOut    []string // substrings expected in stdout
		wantAbsent []string // substrings that must NOT appear
	}{
		{
			name: "differing_positions",
			idA:  "A",
			idB:  "B",
			setupMock: func(st *mocks.MockTraceStore) {
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
			},
			wantOut: []string{"summarize", "delete_record"},
		},
		{
			name: "identical_sequences",
			idA:  "X",
			idB:  "Y",
			setupMock: func(st *mocks.MockTraceStore) {
				st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
						return []core.TraceRef{{TraceID: q.Value}}, nil
					}).AnyTimes()
				st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, id string) (*trace.Trace, error) {
						return toolForest(id, "search", "summarize"), nil
					}).AnyTimes()
			},
			wantOut: []string{"identical"},
		},
		{
			name: "different_lengths",
			idA:  "short",
			idB:  "long",
			setupMock: func(st *mocks.MockTraceStore) {
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
			},
			wantOut: []string{"—"},
		},
		{
			name:    "error_on_idA_resolve",
			idA:     "missing-A",
			idB:     "B",
			useFast: true,
			setupMock: func(st *mocks.MockTraceStore) {
				// Query returns zero refs → correlator times out resolving missing-A
				st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
						return nil, nil
					}).AnyTimes()
			},
			wantErr:    true,
			wantErrSub: "diff: run missing-A",
		},
		{
			name:    "error_on_idB_resolve",
			idA:     "A",
			idB:     "B",
			useFast: true,
			setupMock: func(st *mocks.MockTraceStore) {
				st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
						return []core.TraceRef{{TraceID: q.Value}}, nil
					}).AnyTimes()
				st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, id string) (*trace.Trace, error) {
						if id == "A" {
							return toolForest("A", "search"), nil
						}
						// run B: GetByID returns an error → Resolve fails for B
						return nil, errors.New("store: trace B not found")
					}).AnyTimes()
			},
			wantErr:    true,
			wantErrSub: "diff: run B",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			tt.setupMock(st)

			var cor core.Correlator
			if tt.useFast {
				cor = newFastCorrelator()
			} else {
				cor = newTestCorrelator()
			}

			var buf bytes.Buffer
			err := Diff(context.Background(), cor, st, tt.idA, tt.idB, &buf)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Diff() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.wantErrSub != "" {
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
			}
			for _, sub := range tt.wantOut {
				if !strings.Contains(buf.String(), sub) {
					t.Fatalf("output does not contain %q:\n%s", sub, buf.String())
				}
			}
			for _, sub := range tt.wantAbsent {
				if strings.Contains(buf.String(), sub) {
					t.Fatalf("output must not contain %q but does:\n%s", sub, buf.String())
				}
			}
		})
	}
}
