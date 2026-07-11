package correlate

import (
	"context"
	"errors"
	"fmt"
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

// TestInjectNilSpecPanics proves invariant §4: a nil *RunSpec is a caller-unreachable
// wiring bug (the engine always constructs the spec), so Inject panics with an explicit,
// descriptive message rather than a bare runtime nil-pointer dereference.
func TestInjectNilSpecPanics(t *testing.T) {
	c := New(func() string { return "fixed-id" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("want panic on nil spec, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "correlate: Inject called with nil") {
			t.Fatalf("panic message not descriptive enough: %q", msg)
		}
	}()

	c.Inject(context.Background(), nil)
}

// TestInjectInvalidRunIDPanics proves invariant §4: the run id becomes an
// OTEL_RESOURCE_ATTRIBUTES value (k=v,k=v format) downstream, so an id containing
// the reserved delimiters ',' or '=' (or an empty id) would silently corrupt that
// variable and break correlation. A bad id from idFn is a wiring bug (bad generator),
// so Inject panics with a descriptive "invalid run id" message.
func TestInjectInvalidRunIDPanics(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "comma delimiter", id: "bad,id"},
		{name: "equals delimiter", id: "bad=id"},
		{name: "empty", id: ""},
		{name: "space", id: "bad id"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			c := New(func() string { return tt.id }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("want panic on invalid run id %q, got none", tt.id)
				}
				msg := fmt.Sprintf("%v", r)
				if !strings.Contains(msg, "invalid run id") {
					t.Fatalf("panic message not descriptive enough: %q", msg)
				}
			}()

			c.Inject(context.Background(), &core.RunSpec{})
		})
	}
}

// TestResolveStablePollsUntilCountStable pins the stability-detection path: Resolve
// must return because the span count was observed stable for StableFor consecutive
// polls, NOT because the deadline fired. We assert the exact GetByID poll count to
// convergence so the test can ONLY pass via the stability path.
//
// Span count per GetByID call#N is min(N, 3): grows 1,2,3 then stays 3. With
// StableFor:2, the loop (one ref ⇒ one GetByID per iteration) behaves as:
//
//	call#1 → 1   (1 != lastCount -1)   stable=0  lastCount=1
//	call#2 → 2   (2 != 1)              stable=0  lastCount=2
//	call#3 → 3   (3 != 2)              stable=0  lastCount=3
//	call#4 → 3   (3 == 3, >0)          stable=1  (1 < 2, keep going)
//	call#5 → 3   (3 == 3, >0)          stable=2  (2 >= 2) → RETURN
//
// So convergence requires exactly 5 GetByID calls. Timeout is 1s with a 1ms interval,
// so a timeout-exit (which would happen if StableFor were ignored/broken) would take
// on the order of ~1000 polls — a wildly different count. Asserting calls == 5 thus
// genuinely distinguishes the stability-exit from the timeout fallback.
func TestResolveStablePollsUntilCountStable(t *testing.T) {
	const wantPolls = 5 // GetByID calls to stability per the trace above

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
	// The exact poll count proves Resolve exited via the stability path, not the
	// ~1000-poll timeout fallback.
	if calls != wantPolls {
		t.Fatalf("want %d GetByID polls to stability (stability-exit), got %d", wantPolls, calls)
	}
}

// TestResolveQueriesByTestRunIDTag is the F3 regression pin for invariant §5
// (tag-first correlation): Resolve must ALWAYS query the store by the tag
// "test.run.id" with a value equal to the run id it was handed. It passes against
// the current code and exists to catch future drift — querying a different tag or
// the wrong value would silently resolve the wrong trace (or none) instead of
// failing loud, defeating correlation.
func TestResolveQueriesByTestRunIDTag(t *testing.T) {
	const runID = "pin-run-42"

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)

	var gotQuery core.TraceQuery
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			gotQuery = q
			return []core.TraceRef{{TraceID: "t"}}, nil
		}).AnyTimes()
	st.EXPECT().GetByID(gomock.Any(), "t").
		Return(&trace.Trace{RunID: "t", Spans: []*trace.Span{{Name: "s"}}}, nil).AnyTimes()

	c := New(func() string { return runID }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	if _, err := c.Resolve(context.Background(), st, runID); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if gotQuery.Tag != "test.run.id" {
		t.Errorf("query tag: want %q, got %q", "test.run.id", gotQuery.Tag)
	}
	if gotQuery.Value != runID {
		t.Errorf("query value: want %q (the run id), got %q", runID, gotQuery.Value)
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
	// Error must name the concrete tag and value queried (repo error convention).
	msg := err.Error()
	if !strings.Contains(msg, `tag="test.run.id"`) || !strings.Contains(msg, `value="run-1"`) {
		t.Fatalf("query error missing tag/value context: %q", msg)
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

// TestResolveGetByIDNilTrace proves invariant §4: a misbehaving store returning
// (nil, nil) from GetByID must produce a descriptive error, not a nil-pointer panic.
func TestResolveGetByIDNilTrace(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "nil-trace"}}, nil).Times(1)
	st.EXPECT().GetByID(gomock.Any(), "nil-trace").
		Return(nil, nil).Times(1)

	c := New(func() string { return "run-nil" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	_, err := c.Resolve(context.Background(), st, "run-nil")
	if err == nil {
		t.Fatal("expected error from nil trace, got nil")
	}
	if !strings.Contains(err.Error(), "returned nil trace") {
		t.Fatalf("want error mentioning nil trace, got: %v", err)
	}
}

// TestResolveDeadlineUnstableSpansIsHardError proves audit finding A3: when the
// deadline fires while the span count is still CHANGING (never stabilised) and
// nonzero, Resolve must NOT return the merged trace as a best-effort success — it
// must hard-error (invariant §4, no silent fallbacks). The error names the run id,
// the last observed span count, the stability progress, and the timeout so an
// operator can distinguish "trace still growing at deadline" from "trace never
// arrived" (the zero-span case, which keeps its own error).
func TestResolveDeadlineUnstableSpansIsHardError(t *testing.T) {
	const runID = "unstable-run"

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "growing"}}, nil).AnyTimes()

	// Span count strictly grows every poll — the count is never observed equal to
	// the previous poll, so the stability gate never trips before the deadline.
	// Resolve runs synchronously in this goroutine, so lastN is read race-free
	// after Resolve returns and equals len(m.Spans) at the deadline.
	var lastN int
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id string) (*trace.Trace, error) {
			lastN++
			tr := &trace.Trace{RunID: id}
			for i := 0; i < lastN; i++ {
				tr.Spans = append(tr.Spans, &trace.Span{Name: "span"})
			}
			return tr, nil
		}).AnyTimes()

	c := New(func() string { return runID }, PollConfig{
		Interval:  time.Millisecond,
		StableFor: 2,
		Timeout:   25 * time.Millisecond,
	})
	tr, err := c.Resolve(context.Background(), st, runID)
	if err == nil {
		t.Fatalf("want hard error on unstable-at-deadline, got nil (tr=%v)", tr)
	}
	if tr != nil {
		t.Fatalf("want nil trace on hard error, got %v", tr)
	}
	msg := err.Error()
	for _, want := range []string{
		runID,                          // names the run
		fmt.Sprintf("%d spans", lastN), // last observed span count
		"stable",                       // stability progress
		"25ms",                         // the timeout
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("deadline error missing %q: %q", want, msg)
		}
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
