package correlate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestInjectSetsRunIDAndTag(t *testing.T) {
	c := New(func() string { return "fixed-id" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	spec := &core.RunSpec{}
	id := c.Inject(context.Background(), spec)
	if id != "fixed-id" || spec.RunID != "fixed-id" || spec.Tags["test.run.id"] != "fixed-id" {
		t.Fatalf("inject wrong: id=%q spec=%+v", id, spec)
	}
}

func TestResolveStablePollsUntilCountStable(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "x"}}, nil).AnyTimes()
	// Span count grows 1,2,3 then stays at 3 — Resolve must wait for stability.
	calls := 0
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			calls++
			n := calls
			if n > 3 {
				n = 3
			}
			tr := &trace.Trace{RunID: id}
			for i := 0; i < n; i++ {
				tr.Spans = append(tr.Spans, &trace.Span{Name: "span"})
			}
			return tr, nil
		}).AnyTimes()

	c := New(func() string { return "x" }, PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: time.Second})
	tr, err := c.Resolve(context.Background(), st, "x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(tr.Spans) != 3 {
		t.Fatalf("want 3 stable spans, got %d", len(tr.Spans))
	}
}

func TestResolveQueryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	queryErr := errors.New("store unavailable")
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return(nil, queryErr).Times(1)

	c := New(func() string { return "run-1" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	_, err := c.Resolve(context.Background(), st, "run-1")
	if err == nil {
		t.Fatal("expected error from query failure, got nil")
	}
	if !errors.Is(err, queryErr) {
		t.Fatalf("want wrapped queryErr, got: %v", err)
	}
}

func TestResolveGetByIDError(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	getErr := errors.New("trace not found")
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "abc"}}, nil).Times(1)
	st.EXPECT().GetByID(gomock.Any(), "abc").
		Return(nil, getErr).Times(1)

	c := New(func() string { return "run-2" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	_, err := c.Resolve(context.Background(), st, "run-2")
	if err == nil {
		t.Fatal("expected error from GetByID failure, got nil")
	}
	if !errors.Is(err, getErr) {
		t.Fatalf("want wrapped getErr, got: %v", err)
	}
}

func TestResolveTimeoutZeroSpans(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	// Query always returns no refs — zero spans seen throughout
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()

	c := New(func() string { return "run-3" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: 25 * time.Millisecond})
	_, err := c.Resolve(context.Background(), st, "run-3")
	if err == nil {
		t.Fatal("expected timeout error with zero spans, got nil")
	}
	// Error must mention the runID and the timeout value (per spec §4 — descriptive)
	msg := err.Error()
	if !strings.Contains(msg, "run-3") || !strings.Contains(msg, "0 spans") {
		t.Fatalf("timeout error message not descriptive enough: %q", msg)
	}
}

// TestResolveHonorsContextCancellation proves that a cancelled context interrupts
// the poll loop immediately — even though Timeout is generous (5s), the function
// must return a context.Canceled-wrapped error before the timeout fires.
// If Resolve ignores ctx, this test HANGS for ~5s and then passes without the
// cancellation error, which would make the assertion fail (wrong error or nil error).
func TestResolveHonorsContextCancellation(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)

	// Return one span so the loop keeps polling (not zero-spans timeout path).
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "x"}}, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).
		Return(&trace.Trace{RunID: "x", Spans: []*trace.Span{{Name: "s"}}}, nil).AnyTimes()

	// Pre-cancel the context so the loop sees cancellation on its first iteration.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Generous timeout: without ctx check the loop would run for 5s.
	c := New(func() string { return "run-cancel" }, PollConfig{
		Interval:  time.Millisecond,
		StableFor: 100, // would need 100 stable polls — never reached
		Timeout:   5 * time.Second,
	})
	_, err := c.Resolve(ctx, st, "run-cancel")
	if err == nil {
		t.Fatal("want error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want errors.Is(err, context.Canceled), got: %v", err)
	}
}

// TestResolveMultiTraceForestMerge proves architecture invariant §2:
// Resolve merges Roots and Spans from EVERY matching TraceRef into one forest.
func TestResolveMultiTraceForestMerge(t *testing.T) {
	const runID = "multi-run"

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)

	// Query always returns two refs — both contribute to the merged forest.
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "t1"}, {TraceID: "t2"}}, nil).AnyTimes()

	// t1 has one root and two spans; t2 has one root and one span.
	t1 := &trace.Trace{
		RunID: "t1",
		Roots: []*trace.Span{{Name: "root-t1"}},
		Spans: []*trace.Span{{Name: "s1a"}, {Name: "s1b"}},
	}
	t2 := &trace.Trace{
		RunID: "t2",
		Roots: []*trace.Span{{Name: "root-t2"}},
		Spans: []*trace.Span{{Name: "s2a"}},
	}

	st.EXPECT().GetByID(gomock.Any(), "t1").Return(t1, nil).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), "t2").Return(t2, nil).AnyTimes()

	// StableFor: 2 ensures at least two identical-count polls before returning.
	c := New(func() string { return runID }, PollConfig{
		Interval:  time.Millisecond,
		StableFor: 2,
		Timeout:   time.Second,
	})
	merged, err := c.Resolve(context.Background(), st, runID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if merged.RunID != runID {
		t.Errorf("merged.RunID: want %q, got %q", runID, merged.RunID)
	}
	if len(merged.Roots) != 2 {
		t.Errorf("merged.Roots: want 2 (root-t1 + root-t2), got %d", len(merged.Roots))
	}
	if len(merged.Spans) != 3 {
		t.Errorf("merged.Spans: want 3 (s1a, s1b, s2a), got %d", len(merged.Spans))
	}
}
