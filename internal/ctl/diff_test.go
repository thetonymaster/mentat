package ctl

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// diffErrWriter is an io.Writer that always fails.
type diffErrWriter struct{}

func (diffErrWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write: disk full")
}

func toolForest(run string, tools ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	for i, name := range tools {
		s := &trace.Span{ID: run + string(rune('a'+i)),
			Attrs: map[string]string{genai.Op: genai.OpExecuteTool, genai.ToolName: name}}
		tr.Spans = append(tr.Spans, s)
	}
	return tr
}

// stubForestByID stubs the feature-004 store seam pair (FetchPayload +
// DecodePayload) on st to serve per-id forests: the payload is byte-stable per
// id across polls (so Resolve's stability gate converges exactly as the old
// constant GetByID stub did) and the decode is keyed by the trace id.
func stubForestByID(st *mocks.MockTraceStore, forest func(id string) (*trace.Trace, error)) {
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) ([]byte, error) {
			return []byte("payload-" + id), nil
		}).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(id string, _ []byte) (*trace.Trace, error) {
			return forest(id)
		}).AnyTimes()
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
		name        string
		idA         string
		idB         string
		useFast     bool // use short-timeout correlator (for error cases)
		setupMock   func(st *mocks.MockTraceStore)
		wantErr     bool
		wantErrSubs []string // substrings expected in the error
		wantOut     []string // substrings expected in stdout
		wantAbsent  []string // substrings that must NOT appear
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
				stubForestByID(st, func(id string) (*trace.Trace, error) {
					if id == "A" {
						return toolForest("A", "search", "summarize"), nil
					}
					return toolForest("B", "search", "delete_record"), nil
				})
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
				stubForestByID(st, func(id string) (*trace.Trace, error) {
					return toolForest(id, "search", "summarize"), nil
				})
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
				stubForestByID(st, func(id string) (*trace.Trace, error) {
					if id == "short" {
						return toolForest(id, "search"), nil
					}
					return toolForest(id, "search", "summarize", "finalize"), nil
				})
			},
			wantOut: []string{"—"},
		},
		{
			// Both resolves fail here (Query returns zero refs for BOTH ids):
			// the error must surface BOTH failures descriptively, each naming
			// its run id — the second failure is never lost silently (US3).
			name:    "error_on_both_resolves_surfaces_both",
			idA:     "missing-A",
			idB:     "missing-B",
			useFast: true,
			setupMock: func(st *mocks.MockTraceStore) {
				// Query returns zero refs → both resolves fail not-found
				st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
						return nil, nil
					}).AnyTimes()
			},
			wantErr:     true,
			wantErrSubs: []string{"diff: run missing-A", "diff: run missing-B"},
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
				stubForestByID(st, func(id string) (*trace.Trace, error) {
					if id == "A" {
						return toolForest("A", "search"), nil
					}
					// run B: the store errors → Resolve fails for B
					return nil, errors.New("store: trace B not found")
				})
			},
			wantErr:     true,
			wantErrSubs: []string{"diff: run B"},
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
			if tt.wantErr {
				for _, sub := range tt.wantErrSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
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

// TestDiffResolvesRunsConcurrently pins the US3 diff parallelization (research
// R4, tasks T012/T013): diff's two known-complete resolves must OVERLAP in
// time, not run serially — a saved-run diff pays ~one resolve latency, not two.
// Proven two ways with a delayed, instrumented mock correlator (150ms per
// resolve): the in-flight high-water mark must reach 2, and the wall clock must
// beat the 300ms serial sum. The correlator is mocked at the SEAM, so this also
// re-asserts the FR-004 routing: only ResolveComplete is expected; a Resolve
// call would be an unexpected gomock call.
//
// Deliberately NOT t.Parallel(): asserts an elapsed-time bound.
func TestDiffResolvesRunsConcurrently(t *testing.T) {
	const lag = 150 * time.Millisecond

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl) // never touched: resolution is mocked at the correlator seam
	cor := mocks.NewMockCorrelator(ctrl)

	var mu sync.Mutex
	inFlight, highWater := 0, 0
	cor.EXPECT().ResolveComplete(gomock.Any(), st, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ core.TraceStore, runID string) (*trace.Trace, error) {
			mu.Lock()
			inFlight++
			if inFlight > highWater {
				highWater = inFlight
			}
			mu.Unlock()
			time.Sleep(lag)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return toolForest(runID, "search", "summarize"), nil
		}).Times(2)

	var buf bytes.Buffer
	start := time.Now()
	err := Diff(context.Background(), cor, st, "A", "B", &buf)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(buf.String(), "identical") {
		t.Fatalf("diff output wrong (same sequences must be identical):\n%s", buf.String())
	}

	mu.Lock()
	hw := highWater
	mu.Unlock()
	if hw != 2 {
		t.Fatalf("diff resolves did not overlap: in-flight high-water = %d, want 2", hw)
	}
	if limit := 250 * time.Millisecond; elapsed >= limit {
		t.Fatalf("diff resolves look serial: elapsed %v >= %v (serial sum would be ~%v)", elapsed, limit, 2*lag)
	}
}

func svcForest(run string, services ...string) *trace.Trace {
	tr := &trace.Trace{RunID: run}
	base := time.Unix(0, 0)
	for i, name := range services {
		tr.Spans = append(tr.Spans, &trace.Span{
			ID:    run + string(rune('a'+i)),
			Start: base.Add(time.Duration(i) * time.Millisecond),
			Attrs: map[string]string{"service.name": name},
		})
	}
	return tr
}

func TestDiffServices(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: q.Value}}, nil
		}).AnyTimes()
	stubForestByID(st, func(id string) (*trace.Trace, error) {
		if id == "A" {
			return svcForest("A", "auth", "inventory", "payment"), nil
		}
		return svcForest("B", "auth", "payment", "inventory"), nil
	})

	var buf bytes.Buffer
	if err := DiffServices(context.Background(), newTestCorrelator(), st, "A", "B", &buf); err != nil {
		t.Fatalf("DiffServices: %v", err)
	}
	// Position 2 differs: A has inventory, B has payment.
	for _, want := range []string{"inventory", "payment", "≠"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("output missing %q in:\n%s", want, buf.String())
		}
	}
}

func TestDiffWriteError(t *testing.T) {
	tests := []struct {
		name       string
		idA        string
		idB        string
		wantErrSub string
	}{
		{
			name:       "header write error is returned",
			idA:        "A",
			idB:        "B",
			wantErrSub: "diff: write header",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
					return []core.TraceRef{{TraceID: q.Value}}, nil
				}).AnyTimes()
			stubForestByID(st, func(id string) (*trace.Trace, error) {
				return toolForest(id, "search"), nil
			})

			cor := newTestCorrelator()
			err := Diff(context.Background(), cor, st, tt.idA, tt.idB, diffErrWriter{})
			if err == nil {
				t.Fatal("expected error from failing writer, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
			}
		})
	}
}
