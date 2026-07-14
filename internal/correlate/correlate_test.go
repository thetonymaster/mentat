package correlate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
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
// must return because the observation was stable for StableFor consecutive polls,
// NOT because the deadline fired. We assert the exact FetchPayload poll count to
// convergence so the test can ONLY pass via the stability path.
//
// The payload of fetch call#N encodes min(N, 3) spans: the bytes change while the
// trace grows 1,2,3 then stay byte-identical. With StableFor:2, the loop (one ref
// ⇒ one fetch per round) behaves as:
//
//	fetch#1 → spans=1  (new bytes)      decode  stable=0
//	fetch#2 → spans=2  (bytes changed)  decode  stable=0
//	fetch#3 → spans=3  (bytes changed)  decode  stable=0
//	fetch#4 → spans=3  (bytes equal)    reuse   stable=1  (1 < 2, keep going)
//	fetch#5 → spans=3  (bytes equal)    reuse   stable=2  (2 >= 2) → RETURN
//
// So convergence requires exactly 5 fetches and 3 decodes. Timeout is 1s with a
// 1ms interval, so a timeout-exit (which would happen if StableFor were
// ignored/broken) would take on the order of ~1000 polls — a wildly different
// count. Asserting fetches == 5 thus genuinely distinguishes the stability-exit
// from the timeout fallback.
func TestResolveStablePollsUntilCountStable(t *testing.T) {
	const wantPolls = 5 // FetchPayload calls to stability per the trace above

	ctrl := gomock.NewController(t)
	st, counters := spansPayloadStore(ctrl, func(call int) string {
		if call > 3 {
			call = 3
		}
		return fmt.Sprintf("spans=%d", call)
	})

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
	if calls := counters.Fetches("t1"); calls != wantPolls {
		t.Fatalf("want %d payload fetches to stability (stability-exit), got %d", wantPolls, calls)
	}
	if d := counters.Decodes("t1"); d != 3 {
		t.Fatalf("want 3 decodes (one per byte change, none after), got %d", d)
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
	st.EXPECT().FetchPayload(gomock.Any(), "t").Return([]byte("payload-t"), nil).AnyTimes()
	st.EXPECT().DecodePayload("t", gomock.Any()).
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

// TestResolveFetchPayloadError pins the fetch-error half of complete-or-loud:
// a failing payload fetch fails resolution with the wrapped `correlate: get
// <id>` error (the same contract the pre-004 GetByID path had).
func TestResolveFetchPayloadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	getErr := errors.New("trace not found")
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "abc"}}, nil).Times(1)
	st.EXPECT().FetchPayload(gomock.Any(), "abc").
		Return(nil, getErr).Times(1)

	c := New(func() string { return "run-2" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	_, err := c.Resolve(context.Background(), st, "run-2")
	if err == nil {
		t.Fatal("expected error from FetchPayload failure, got nil")
	}
	if !errors.Is(err, getErr) {
		t.Fatalf("want wrapped getErr, got: %v", err)
	}
	if !strings.Contains(err.Error(), "correlate: get abc") {
		t.Fatalf("want the existing wrapped-error format %q, got: %v", "correlate: get abc: ...", err)
	}
}

// TestResolveDecodePayloadError pins the decode-error half: undecodable payload
// bytes are a hard, wrapped error naming the trace id — never a silent skip.
func TestResolveDecodePayloadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	decErr := errors.New("malformed OTLP JSON")
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "abc"}}, nil).Times(1)
	st.EXPECT().FetchPayload(gomock.Any(), "abc").
		Return([]byte("garbage"), nil).Times(1)
	st.EXPECT().DecodePayload("abc", gomock.Any()).
		Return(nil, decErr).Times(1)

	c := New(func() string { return "run-dec" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	_, err := c.Resolve(context.Background(), st, "run-dec")
	if err == nil {
		t.Fatal("expected error from DecodePayload failure, got nil")
	}
	if !errors.Is(err, decErr) {
		t.Fatalf("want wrapped decErr, got: %v", err)
	}
	if !strings.Contains(err.Error(), "decode abc") {
		t.Fatalf("want error naming the decoded trace id, got: %v", err)
	}
}

// TestResolveDecodePayloadNilTrace proves invariant §4: a misbehaving store
// returning (nil, nil) from DecodePayload must produce a descriptive error, not
// a nil-pointer panic.
func TestResolveDecodePayloadNilTrace(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "nil-trace"}}, nil).Times(1)
	st.EXPECT().FetchPayload(gomock.Any(), "nil-trace").
		Return([]byte("p"), nil).Times(1)
	st.EXPECT().DecodePayload("nil-trace", gomock.Any()).
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
	const endpoint = "http://localhost:3200"

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return([]core.TraceRef{{TraceID: "growing"}}, nil).AnyTimes()

	// Span count strictly grows every poll — the payload bytes (and the count
	// they decode to) are never observed equal to the previous poll, so the
	// stability gate never trips before the deadline. lastN is only read after
	// Resolve returns (all fetch work joined), so it is race-free and equals
	// len(m.Spans) at the deadline.
	var lastN int
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string) ([]byte, error) {
			lastN++
			return []byte(fmt.Sprintf("spans=%d", lastN)), nil
		}).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(id string, payload []byte) (*trace.Trace, error) {
			var n int
			if _, err := fmt.Sscanf(string(payload), "spans=%d", &n); err != nil {
				return nil, fmt.Errorf("undecodable payload %q: %w", payload, err)
			}
			tr := &trace.Trace{RunID: id}
			for i := 0; i < n; i++ {
				tr.Spans = append(tr.Spans, &trace.Span{Name: "span"})
			}
			return tr, nil
		}).AnyTimes()

	c := New(func() string { return runID }, PollConfig{
		Interval:  time.Millisecond,
		StableFor: 2,
		Timeout:   25 * time.Millisecond,
	}, WithEndpoint(endpoint))
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
		"unstable at deadline",         // feature-002 wording — must not regress
		"store: " + endpoint,           // FR-003: names the queried store
		`query: { .test.run.id = "unstable-run" }`, // FR-003: exact query shape
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("deadline error missing %q:\n%s", want, msg)
		}
	}
	// The trace EXISTS (spans present) — this is a stability problem, not a
	// "where is it" problem, so the zero-span triage checklist must NOT appear.
	if strings.Contains(msg, "checklist:") {
		t.Fatalf("unstable-deadline error must NOT carry the zero-span checklist:\n%s", msg)
	}
}

// TestResolveTimeoutZeroSpans pins the zero-span timeout error. Beyond the
// feature-002 descriptive fields (run id, timeout, "0 spans"), FR-003 requires
// the message be diagnosable from the text ALONE: it must name the store
// endpoint the correlator queried, the exact TraceQL query shape (matching
// store/tempo.go's `{ .%s = "%s" }`), and the curated triage checklist — no
// trace was found at all, so "where is it" guidance is warranted.
func TestResolveTimeoutZeroSpans(t *testing.T) {
	const endpoint = "http://localhost:3200"

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	// Query always returns no refs — zero spans seen throughout
	st.EXPECT().Query(gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()

	c := New(func() string { return "run-3" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: 25 * time.Millisecond}, WithEndpoint(endpoint))
	_, err := c.Resolve(context.Background(), st, "run-3")
	if err == nil {
		t.Fatal("expected timeout error with zero spans, got nil")
	}
	// Error must mention the runID and the timeout value (per spec §4 — descriptive)
	msg := err.Error()
	if !strings.Contains(msg, "run-3") || !strings.Contains(msg, "0 spans") {
		t.Fatalf("timeout error message not descriptive enough: %q", msg)
	}
	// FR-003 enrichment: store endpoint, exact query shape, and the checklist —
	// each is a pinned substring users script against.
	for _, want := range []string{
		"store: " + endpoint,
		`query: { .test.run.id = "run-3" }`,
		"checklist:",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("zero-span timeout error missing enrichment %q:\n%s", want, msg)
		}
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
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).
		Return([]byte("payload-x"), nil).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).
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

// --- Feature 004 (US2): decode-once stability poll with byte-level change checks ---

// nSpanTrace builds a trace with n spans, each carrying the given attrs.
func nSpanTrace(n int, attrs map[string]string) *trace.Trace {
	tr := &trace.Trace{}
	for i := 0; i < n; i++ {
		a := map[string]string{}
		for k, v := range attrs {
			a[k] = v
		}
		sp := &trace.Span{Name: fmt.Sprintf("span-%d", i), Attrs: a}
		tr.Spans = append(tr.Spans, sp)
	}
	if n > 0 {
		tr.Roots = []*trace.Span{tr.Spans[0]}
	}
	return tr
}

// spansPayloadStore builds a counting store with a single ref "t1" whose fetch
// payload is produced per fetch-call number by payloadFn, and whose decode
// parses "spans=N" out of the payload bytes and returns an N-span trace —
// decode genuinely decodes the fetched bytes, mirroring a real store.
func spansPayloadStore(ctrl *gomock.Controller, payloadFn func(call int) string) (*mocks.MockTraceStore, *storeCounters) {
	var mu sync.Mutex
	calls := 0
	return newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: "t1"}}, nil
		},
		func(_ context.Context, _ string) ([]byte, error) {
			mu.Lock()
			calls++
			p := payloadFn(calls)
			mu.Unlock()
			return []byte(p), nil
		},
		func(_ string, payload []byte) (*trace.Trace, error) {
			var n int
			if _, err := fmt.Sscanf(string(payload), "spans=%d", &n); err != nil {
				return nil, fmt.Errorf("spansPayloadStore: undecodable payload %q: %w", payload, err)
			}
			return nSpanTrace(n, nil), nil
		})
}

// TestResolveDecodesOncePerTraceWhenPayloadStable pins FR-002 (audit C1): an
// N-round stability poll over an already-complete trace performs exactly ONE
// full decode plus one cheap payload byte-check per round — not one decode per
// round. The counting store proves it: fetches == poll rounds, decodes == 1.
func TestResolveDecodesOncePerTraceWhenPayloadStable(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	st, counters := newCountingStore(ctrl, []storedTrace{{id: "t1", tr: nSpanTrace(3, nil)}})

	c := New(func() string { return "run-once" }, PollConfig{Interval: time.Millisecond, StableFor: 3, Timeout: 2 * time.Second})
	got, err := c.Resolve(context.Background(), st, "run-once")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.Spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(got.Spans))
	}
	// Constant payload, StableFor 3 → rounds: 1 changed (decode) + 3 stable = 4.
	if rounds := counters.Queries(); rounds != 4 {
		t.Fatalf("want 4 poll rounds (1 changed + 3 stable), got %d", rounds)
	}
	if f := counters.Fetches("t1"); f != 4 {
		t.Fatalf("want 4 payload fetches (one cheap check per round), got %d", f)
	}
	if d := counters.Decodes("t1"); d != 1 {
		t.Fatalf("want exactly 1 full decode for a byte-stable trace, got %d", d)
	}
}

// TestResolveChangedPayloadRedecodesAndResetsStability pins the changed-payload
// path: a byte change re-decodes the (new) payload AND resets the stability
// count — even though the span count never changes (2 spans throughout), the
// byte change at round 3 must force two more stable observations. Payload
// sequence by round: v1, v1, v2, v2, v2 with StableFor 2 →
// reset, stable(1), reset(byte change!), stable(1), stable(2) — 5 rounds,
// 2 decodes, and the returned forest is decoded from the v2 bytes.
func TestResolveChangedPayloadRedecodesAndResetsStability(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	var mu sync.Mutex
	calls := 0
	st, counters := newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: "t1"}}, nil
		},
		func(_ context.Context, _ string) ([]byte, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n <= 2 {
				return []byte("version=1"), nil
			}
			return []byte("version=2"), nil
		},
		func(_ string, payload []byte) (*trace.Trace, error) {
			var v int
			if _, err := fmt.Sscanf(string(payload), "version=%d", &v); err != nil {
				return nil, fmt.Errorf("undecodable payload %q: %w", payload, err)
			}
			return nSpanTrace(2, map[string]string{"version": fmt.Sprint(v)}), nil
		})

	c := New(func() string { return "run-chg" }, PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: 2 * time.Second})
	got, err := c.Resolve(context.Background(), st, "run-chg")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Spans[0].Attr("version") != "2" {
		t.Fatalf("returned forest not decoded from the changed (v2) bytes: version=%q", got.Spans[0].Attr("version"))
	}
	// The byte change at constant span count MUST reset stability: 5 rounds, not
	// the 3 a span-count-only gate would take.
	if rounds := counters.Queries(); rounds != 5 {
		t.Fatalf("want 5 poll rounds (byte change resets stability), got %d", rounds)
	}
	if d := counters.Decodes("t1"); d != 2 {
		t.Fatalf("want 2 decodes (v1 once, v2 once), got %d", d)
	}
}

// churningPayloadStore serves a single ref whose payload bytes differ on every
// fetch while the decoded span count stays constant at 3 — store-side byte
// churn, the case the span-count gate of feature 002 cannot see.
func churningPayloadStore(ctrl *gomock.Controller) (*mocks.MockTraceStore, *storeCounters) {
	var mu sync.Mutex
	calls := 0
	return newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: "t1"}}, nil
		},
		func(_ context.Context, _ string) ([]byte, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			return []byte(fmt.Sprintf("churn-%d", n)), nil
		},
		func(string, []byte) (*trace.Trace, error) {
			return nSpanTrace(3, nil), nil
		})
}

// TestResolveByteChurnAtConstantSpanCountIsInstability pins the deliberate
// strengthening (Clarifications 2026-07-11): ANY payload byte change counts as
// instability, even when the span count is constant. A store whose bytes churn
// every round must never satisfy the stability gate — hard error at deadline,
// no forest released (the feature-002 span-count gate would have returned
// success here).
func TestResolveByteChurnAtConstantSpanCountIsInstability(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	st, _ := churningPayloadStore(ctrl)

	c := New(func() string { return "run-churn" }, PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: 50 * time.Millisecond})
	tr, err := c.Resolve(context.Background(), st, "run-churn")
	if err == nil {
		t.Fatalf("want hard error for byte churn at constant span count, got nil (tr=%v)", tr)
	}
	if tr != nil {
		t.Fatalf("want nil trace on hard error, got %v", tr)
	}
	if !strings.Contains(err.Error(), "unstable at deadline") {
		t.Fatalf("want unstable-at-deadline error, got: %v", err)
	}
}

// TestResolveUnstableDeadlineErrorNamesByteChurnAtConstantSpanCount pins the
// second clarification guard: when resets were caused by byte changes while the
// span count stayed constant, the deadline error must SAY so — store-side byte
// churn must be diagnosable as such, not mistaken for a growing trace (repo
// error convention: name the concrete thing that failed).
func TestResolveUnstableDeadlineErrorNamesByteChurnAtConstantSpanCount(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	st, _ := churningPayloadStore(ctrl)

	c := New(func() string { return "run-churn-msg" }, PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: 50 * time.Millisecond})
	_, err := c.Resolve(context.Background(), st, "run-churn-msg")
	if err == nil {
		t.Fatal("want hard error for byte churn at constant span count, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"payload hash changed", "span count constant at 3"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("deadline error does not name byte-churn-at-constant-span-count (%q missing): %q", want, msg)
		}
	}
}

// spanCountBaselineDecisions replays the feature-002 stability algorithm — the
// merged-span-count comparison this feature replaces — over a span-count
// sequence, returning the per-round decisions ("reset"/"stable") and whether it
// converged. This is the FR-006 parity oracle.
func spanCountBaselineDecisions(counts []int, stableFor int) (decisions []string, converged bool) {
	lastCount, stable := -1, 0
	for _, n := range counts {
		if n > 0 && n == lastCount {
			stable++
			decisions = append(decisions, "stable")
			if stable >= stableFor {
				return decisions, true
			}
		} else {
			stable = 0
			decisions = append(decisions, "reset")
		}
		lastCount = n
	}
	return decisions, false
}

// roundDecisions derives the byte-level loop's per-round decisions from a
// counting store's event log: each "query" opens a round; a round containing a
// decode observed a change (reset), a fetch-only round observed no change
// (stable). Valid for single-ref sequences with nonzero span counts — exactly
// the corpus shapes replayed by the parity test.
func roundDecisions(events []string) []string {
	var decisions []string
	inRound, reset := false, false
	flush := func() {
		if !inRound {
			return
		}
		if reset {
			decisions = append(decisions, "reset")
		} else {
			decisions = append(decisions, "stable")
		}
	}
	for _, e := range events {
		switch {
		case e == "query":
			flush()
			inRound, reset = true, false
		case strings.HasPrefix(e, "decode "):
			reset = true
		}
	}
	flush()
	return decisions
}

// TestResolveObservationParityWithSpanCountBaseline is the FR-006 guard
// (Clarifications 2026-07-11): replaying the existing corpus poll sequences —
// growing 1,2,3,3,3; strictly-growing; constant-trace — through the byte-level
// change check yields the SAME per-round stable/reset decisions and final
// outcome as the feature-002 span-count baseline.
func TestResolveObservationParityWithSpanCountBaseline(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		payloadFn func(call int) string // fetch payload per round (single ref ⇒ 1 fetch/round)
		countFn   func(round int) int   // span count per 1-indexed round (baseline input)
		stableFor int
		timeout   time.Duration
		wantErr   bool
	}{
		{
			name: "growing 1,2,3 then stable (corpus: stable-poll fixture)",
			payloadFn: func(call int) string {
				if call > 3 {
					call = 3
				}
				return fmt.Sprintf("spans=%d", call)
			},
			countFn: func(round int) int {
				if round > 3 {
					round = 3
				}
				return round
			},
			stableFor: 2,
			timeout:   2 * time.Second,
			wantErr:   false,
		},
		{
			name:      "strictly growing (corpus: unstable-at-deadline fixture)",
			payloadFn: func(call int) string { return fmt.Sprintf("spans=%d", call) },
			countFn:   func(round int) int { return round },
			stableFor: 2,
			timeout:   40 * time.Millisecond,
			wantErr:   true,
		},
		{
			name:      "constant trace (corpus: steps fixtures, constant per run across polls)",
			payloadFn: func(int) string { return "spans=2" },
			countFn:   func(int) int { return 2 },
			stableFor: 3,
			timeout:   2 * time.Second,
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			st, counters := spansPayloadStore(ctrl, tt.payloadFn)

			c := New(func() string { return "run-parity" }, PollConfig{Interval: time.Millisecond, StableFor: tt.stableFor, Timeout: tt.timeout})
			_, err := c.Resolve(context.Background(), st, "run-parity")
			if (err != nil) != tt.wantErr {
				t.Fatalf("outcome diverged from span-count baseline: err=%v wantErr=%v", err, tt.wantErr)
			}

			rounds := counters.Queries()
			counts := make([]int, rounds)
			for i := range counts {
				counts[i] = tt.countFn(i + 1)
			}
			wantDecisions, wantConverged := spanCountBaselineDecisions(counts, tt.stableFor)
			if wantConverged == tt.wantErr {
				t.Fatalf("test wiring drifted: baseline converged=%v but wantErr=%v", wantConverged, tt.wantErr)
			}
			gotDecisions := roundDecisions(counters.Events())
			if len(gotDecisions) != len(wantDecisions) {
				t.Fatalf("round count diverged from baseline:\ngot  %v\nwant %v", gotDecisions, wantDecisions)
			}
			for i := range wantDecisions {
				if gotDecisions[i] != wantDecisions[i] {
					t.Fatalf("round %d decision diverged from span-count baseline:\ngot  %v\nwant %v", i+1, gotDecisions, wantDecisions)
				}
			}
		})
	}
}

// TestResolveReturnsForestFromFinalStableBytes closes the spec edge case "trace
// grows between the last observation and the final decode": the returned forest
// must be the one decoded from the SAME bytes the final stable observations
// hashed — one decode, no re-fetch/re-decode after stability. The decode stub
// tags each decode with a sequence number; only the first may ever be returned.
func TestResolveReturnsForestFromFinalStableBytes(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	var mu sync.Mutex
	decodeSeq := 0
	st, counters := newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: "t1"}}, nil
		},
		func(context.Context, string) ([]byte, error) {
			return []byte("constant-payload"), nil
		},
		func(string, []byte) (*trace.Trace, error) {
			mu.Lock()
			decodeSeq++
			seq := decodeSeq
			mu.Unlock()
			return nSpanTrace(1, map[string]string{"decode.seq": fmt.Sprint(seq)}), nil
		})

	c := New(func() string { return "run-final" }, PollConfig{Interval: time.Millisecond, StableFor: 3, Timeout: 2 * time.Second})
	got, err := c.Resolve(context.Background(), st, "run-final")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if seq := got.Spans[0].Attr("decode.seq"); seq != "1" {
		t.Fatalf("returned forest is not the one decoded from the stable bytes: decode.seq=%q (re-decoded after stability)", seq)
	}
	if d := counters.Decodes("t1"); d != 1 {
		t.Fatalf("want exactly 1 decode, got %d", d)
	}
	// No extra fetch after the final stable observation either: every fetch
	// belongs to a counted poll round.
	if f, rounds := counters.Fetches("t1"), counters.Queries(); f != rounds {
		t.Fatalf("want fetches == poll rounds (no post-stability re-fetch), got fetches=%d rounds=%d", f, rounds)
	}
}

// TestResolveWaitsOutIngestionLagThenConverges pins the recovery path: a store
// that has not yet ingested the run's trace (Query returns zero refs) must keep
// polling — not error early — and converge once the trace appears and its bytes
// are observed stable. (The zero-trace TIMEOUT hard error is covered by
// TestResolveTimeoutZeroSpans.)
func TestResolveWaitsOutIngestionLagThenConverges(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	st, counters := newDelayedStore(ctrl, 20*time.Millisecond, time.Now(),
		[]storedTrace{{id: "t1", tr: nSpanTrace(2, nil)}})

	c := New(func() string { return "run-lag" }, PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: 2 * time.Second})
	got, err := c.Resolve(context.Background(), st, "run-lag")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.Spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(got.Spans))
	}
	// Once available the payload is byte-stable, so exactly one decode; the
	// appearance round resets stability (new ref set), then StableFor unchanged
	// rounds follow — at least 3 fetch rounds after availability.
	if d := counters.Decodes("t1"); d != 1 {
		t.Fatalf("want 1 decode after availability, got %d", d)
	}
	if f := counters.Fetches("t1"); f < 3 {
		t.Fatalf("want ≥3 post-availability fetch rounds (appear + 2 stable), got %d", f)
	}
}

// TestResolveQueryOrderFlappingStabilizesAndMergesInCanonicalIDOrder pins the
// order-canonicalization contract: store query order is NOT part of the ref-set
// identity. Query returns the SAME 3-ref set in a DIFFERENT order every round
// (rotating) while every ref's payload bytes stay identical — the stability
// counter must NOT reset on order alone, so Resolve converges via the stability
// path in exactly 1 changed + StableFor stable rounds (without canonicalization
// it never stabilizes and dies with the unstable-at-deadline hard error). The
// merged forest — a multi-root forest, one root per ref (invariant §2) — is in
// canonical sorted-TraceID order, and canonicalization must sort a COPY: the
// slices the store returned keep their original order.
func TestResolveQueryOrderFlappingStabilizesAndMergesInCanonicalIDOrder(t *testing.T) {
	t.Parallel()
	base := []string{"t2", "t3", "t1"} // deliberately non-sorted
	forests := map[string]*trace.Trace{}
	for _, id := range base {
		sp := &trace.Span{Name: "span-" + id}
		forests[id] = &trace.Trace{Roots: []*trace.Span{sp}, Spans: []*trace.Span{sp}}
	}

	var mu sync.Mutex
	calls := 0
	var returned [][]core.TraceRef // every slice handed to Resolve, in call order
	ctrl := gomock.NewController(t)
	st, counters := newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			mu.Lock()
			defer mu.Unlock()
			refs := make([]core.TraceRef, len(base))
			for i := range base {
				refs[i] = core.TraceRef{TraceID: base[(i+calls)%len(base)]}
			}
			calls++
			returned = append(returned, refs)
			return refs, nil
		},
		func(_ context.Context, id string) ([]byte, error) {
			return []byte("payload-" + id), nil // byte-identical every round
		},
		func(id string, _ []byte) (*trace.Trace, error) {
			return forests[id], nil
		})

	c := New(func() string { return "run-flap" }, PollConfig{Interval: time.Millisecond, StableFor: 3, Timeout: 2 * time.Second})
	got, err := c.Resolve(context.Background(), st, "run-flap")
	if err != nil {
		t.Fatalf("resolve with order-flapping (byte-identical) refs: %v", err)
	}

	// Stability path, not deadline: 1 changed round (fresh decodes) + 3 stable.
	if rounds := counters.Queries(); rounds != 4 {
		t.Fatalf("want 4 poll rounds (1 changed + StableFor=3 stable; order flap must not reset), got %d", rounds)
	}

	// Merge order is canonical sorted-TraceID order, never the flapping query order.
	wantOrder := []string{"span-t1", "span-t2", "span-t3"}
	if len(got.Spans) != len(wantOrder) {
		t.Fatalf("want %d spans, got %d (%v)", len(wantOrder), len(got.Spans), spanNames(got))
	}
	for i, want := range wantOrder {
		if got.Spans[i].Name != want {
			t.Fatalf("merge order not canonical sorted-TraceID at %d: want %q, got %q (full: %v)", i, want, got.Spans[i].Name, spanNames(got))
		}
	}
	if len(got.Roots) != 3 {
		t.Fatalf("multi-root forest: want 3 roots (one per trace), got %d", len(got.Roots))
	}

	// Canonicalization sorted a copy: the store-returned slices are unmutated.
	mu.Lock()
	defer mu.Unlock()
	for call, refs := range returned {
		for i := range refs {
			if want := base[(i+call)%len(base)]; refs[i].TraceID != want {
				t.Fatalf("store-returned slice mutated (call %d, index %d): want %q, got %q — canonicalization must sort a copy", call, i, want, refs[i].TraceID)
			}
		}
	}
}

// TestResolvePerRoundFetchesOverlapAndMergeInCanonicalIDOrder pins FR-003
// (audit C3): within a poll round the per-trace fetches must OVERLAP rather
// than execute serially, while the merge stays deterministic in canonical
// sorted-TraceID order (not completion order — latencies here are deliberately
// reversed so the last ref finishes first). Proven two ways: the in-flight
// high-water mark across fetches must exceed 1, and the wall clock must beat
// the serial sum (2 rounds × 200ms of summed latency = 400ms serial vs ~160ms
// overlapped; 300ms is the generous, CI-safe boundary).
//
// Deliberately NOT t.Parallel(): the elapsed-time bound would be distorted by
// sibling tests competing for the same cores.
func TestResolvePerRoundFetchesOverlapAndMergeInCanonicalIDOrder(t *testing.T) {
	refIDs := []string{"t1", "t2", "t3", "t4"}
	latency := map[string]time.Duration{ // reversed: first ref is slowest
		"t1": 80 * time.Millisecond,
		"t2": 60 * time.Millisecond,
		"t3": 40 * time.Millisecond,
		"t4": 20 * time.Millisecond,
	}
	forests := map[string]*trace.Trace{}
	refs := make([]core.TraceRef, len(refIDs))
	for i, id := range refIDs {
		sp := &trace.Span{Name: "span-" + id}
		forests[id] = &trace.Trace{Roots: []*trace.Span{sp}, Spans: []*trace.Span{sp}}
		refs[i] = core.TraceRef{TraceID: id}
	}

	var mu sync.Mutex
	inFlight, highWater := 0, 0
	ctrl := gomock.NewController(t)
	st, _ := newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return refs, nil
		},
		func(_ context.Context, id string) ([]byte, error) {
			mu.Lock()
			inFlight++
			if inFlight > highWater {
				highWater = inFlight
			}
			mu.Unlock()
			time.Sleep(latency[id])
			mu.Lock()
			inFlight--
			mu.Unlock()
			return []byte("payload-" + id), nil
		},
		func(id string, _ []byte) (*trace.Trace, error) {
			return forests[id], nil
		})

	c := New(func() string { return "run-fanout" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: 5 * time.Second})
	start := time.Now()
	got, err := c.Resolve(context.Background(), st, "run-fanout")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Merge order: ref order, even though t4 (fastest) completed first.
	wantOrder := []string{"span-t1", "span-t2", "span-t3", "span-t4"}
	if len(got.Spans) != len(wantOrder) {
		t.Fatalf("want %d spans, got %d", len(wantOrder), len(got.Spans))
	}
	for i, want := range wantOrder {
		if got.Spans[i].Name != want {
			t.Fatalf("merge order not ref order at %d: want %q, got %q (full: %v)", i, want, got.Spans[i].Name, spanNames(got))
		}
	}

	mu.Lock()
	hw := highWater
	mu.Unlock()
	if hw < 2 {
		t.Fatalf("per-round fetches did not overlap: in-flight high-water = %d, want >= 2", hw)
	}
	if limit := 300 * time.Millisecond; elapsed >= limit {
		t.Fatalf("per-round fetches look serial: elapsed %v >= %v (serial sum would be ~400ms)", elapsed, limit)
	}
}

func spanNames(tr *trace.Trace) []string {
	names := make([]string, len(tr.Spans))
	for i, sp := range tr.Spans {
		names[i] = sp.Name
	}
	return names
}

// TestResolveFirstFetchErrorFailsRoundAndCancelsSiblings pins the FR-003 error
// contract: a fetch failure fails resolution with the same wrapped error as the
// serial loop (`correlate: get <id>: ...`), and the failing fetch cancels the
// round's in-flight siblings — Resolve must return in ~the failing fetch's
// latency (5ms), not wait out the slow sibling's 500ms.
//
// Deliberately NOT t.Parallel(): asserts an elapsed-time bound.
func TestResolveFirstFetchErrorFailsRoundAndCancelsSiblings(t *testing.T) {
	fetchErr := errors.New("store: trace bad exploded")
	ctrl := gomock.NewController(t)
	st, _ := newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return []core.TraceRef{{TraceID: "slow"}, {TraceID: "bad"}}, nil
		},
		func(ctx context.Context, id string) ([]byte, error) {
			if id == "bad" {
				time.Sleep(5 * time.Millisecond)
				return nil, fetchErr
			}
			// "slow": honours cancellation; without it, succeeds after 500ms.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
				return []byte("payload-slow"), nil
			}
		},
		func(id string, _ []byte) (*trace.Trace, error) {
			return nSpanTrace(1, nil), nil
		})

	c := New(func() string { return "run-err" }, PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: 5 * time.Second})
	start := time.Now()
	tr, err := c.Resolve(context.Background(), st, "run-err")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("want fetch error to fail resolution, got nil (tr=%v)", tr)
	}
	if tr != nil {
		t.Fatalf("want nil trace on fetch error, got %v", tr)
	}
	if !errors.Is(err, fetchErr) {
		t.Fatalf("want wrapped fetchErr, got: %v", err)
	}
	if !strings.Contains(err.Error(), "correlate: get bad:") {
		t.Fatalf("want the existing wrapped-error format %q, got: %v", "correlate: get bad: ...", err)
	}
	if limit := 250 * time.Millisecond; elapsed >= limit {
		t.Fatalf("failing fetch did not cancel the round: elapsed %v >= %v (serial would wait out the 500ms sibling)", elapsed, limit)
	}
}

// --- Feature 004 (US3): known-complete resolution for historical inspection ---

// TestResolveCompleteSingleFetchPassNoStabilitySleep pins FR-004 (audit C4):
// known-complete resolution performs exactly ONE query and ONE fetch+decode
// pass with ZERO stability sleeps. The PollConfig is deliberately hostile
// (hour-scale Interval and Timeout, StableFor 100): if ResolveComplete
// consulted the stability loop or slept even one interval, the elapsed bound
// (and the test's own timeout) would blow up — the poll config must play no
// part in this mode.
//
// Deliberately NOT t.Parallel(): asserts an elapsed-time bound.
func TestResolveCompleteSingleFetchPassNoStabilitySleep(t *testing.T) {
	ctrl := gomock.NewController(t)
	st, counters := newCountingStore(ctrl, []storedTrace{{id: "t1", tr: nSpanTrace(3, nil)}})

	c := New(func() string { return "run-hist" }, PollConfig{Interval: time.Hour, StableFor: 100, Timeout: time.Hour})
	// Bounded ctx: with the hour-scale hostile PollConfig, a regression into the
	// live polling loop must fail in seconds, not as a 10-minute test panic.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	got, err := c.ResolveComplete(ctx, st, "run-hist")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveComplete: %v", err)
	}
	if got.RunID != "run-hist" {
		t.Fatalf("RunID: want %q, got %q", "run-hist", got.RunID)
	}
	if len(got.Spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(got.Spans))
	}
	if q := counters.Queries(); q != 1 {
		t.Fatalf("want exactly 1 query (no poll rounds), got %d", q)
	}
	if f := counters.Fetches("t1"); f != 1 {
		t.Fatalf("want exactly 1 payload fetch (one pass), got %d", f)
	}
	if d := counters.Decodes("t1"); d != 1 {
		t.Fatalf("want exactly 1 decode (one pass), got %d", d)
	}
	if limit := 500 * time.Millisecond; elapsed >= limit {
		t.Fatalf("known-complete resolution slept: elapsed %v >= %v (Interval is %v — any stability sleep would show)", elapsed, limit, time.Hour)
	}
}

// TestResolveCompleteMultiRefFetchesOverlapAndMergeInCanonicalIDOrder pins the
// fan-out reuse: the single known-complete fetch pass overlaps its per-trace
// fetches (in-flight high-water > 1, wall clock beats the 200ms serial sum)
// and merges in CANONICAL sorted-TraceID order — regardless of Query order
// (deliberately non-sorted: t3,t1,t4,t2) and regardless of completion order
// (latencies are reversed so t1, first in sorted order, finishes last). Each
// ref carries its own root — the multi-root forest case (invariant §2): a
// historical run spanning 4 traces merges into one forest with 4 roots.
//
// Deliberately NOT t.Parallel(): asserts an elapsed-time bound.
func TestResolveCompleteMultiRefFetchesOverlapAndMergeInCanonicalIDOrder(t *testing.T) {
	refIDs := []string{"t3", "t1", "t4", "t2"} // Query order: deliberately non-sorted
	latency := map[string]time.Duration{       // reversed vs sorted-ID order: t1 is slowest
		"t1": 80 * time.Millisecond,
		"t2": 60 * time.Millisecond,
		"t3": 40 * time.Millisecond,
		"t4": 20 * time.Millisecond,
	}
	forests := map[string]*trace.Trace{}
	refs := make([]core.TraceRef, len(refIDs))
	for i, id := range refIDs {
		sp := &trace.Span{Name: "span-" + id}
		forests[id] = &trace.Trace{Roots: []*trace.Span{sp}, Spans: []*trace.Span{sp}}
		refs[i] = core.TraceRef{TraceID: id}
	}

	var mu sync.Mutex
	inFlight, highWater := 0, 0
	ctrl := gomock.NewController(t)
	st, counters := newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return refs, nil
		},
		func(_ context.Context, id string) ([]byte, error) {
			mu.Lock()
			inFlight++
			if inFlight > highWater {
				highWater = inFlight
			}
			mu.Unlock()
			time.Sleep(latency[id])
			mu.Lock()
			inFlight--
			mu.Unlock()
			return []byte("payload-" + id), nil
		},
		func(id string, _ []byte) (*trace.Trace, error) {
			return forests[id], nil
		})

	c := New(func() string { return "run-hist-multi" }, PollConfig{Interval: time.Hour, StableFor: 100, Timeout: time.Hour})
	// Bounded ctx: with the hour-scale hostile PollConfig, a regression into the
	// live polling loop must fail in seconds, not as a 10-minute test panic.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	got, err := c.ResolveComplete(ctx, st, "run-hist-multi")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveComplete: %v", err)
	}

	// Merge order: canonical sorted-TraceID order — not the (non-sorted) Query
	// order t3,t1,t4,t2, and not completion order (t4, fastest, completed first).
	wantOrder := []string{"span-t1", "span-t2", "span-t3", "span-t4"}
	if len(got.Spans) != len(wantOrder) {
		t.Fatalf("want %d spans, got %d", len(wantOrder), len(got.Spans))
	}
	for i, want := range wantOrder {
		if got.Spans[i].Name != want {
			t.Fatalf("merge order not canonical sorted-TraceID at %d: want %q, got %q (full: %v)", i, want, got.Spans[i].Name, spanNames(got))
		}
	}
	if len(got.Roots) != 4 {
		t.Fatalf("multi-root forest: want 4 roots (one per trace), got %d", len(got.Roots))
	}

	mu.Lock()
	hw := highWater
	mu.Unlock()
	if hw < 2 {
		t.Fatalf("known-complete fetches did not overlap: in-flight high-water = %d, want >= 2", hw)
	}
	if limit := 160 * time.Millisecond; elapsed >= limit {
		t.Fatalf("known-complete fetches look serial: elapsed %v >= %v (serial sum would be ~200ms)", elapsed, limit)
	}
	// Exactly one pass: 1 query, 1 fetch + 1 decode per ref.
	if q := counters.Queries(); q != 1 {
		t.Fatalf("want exactly 1 query, got %d", q)
	}
	for _, id := range refIDs {
		if f := counters.Fetches(id); f != 1 {
			t.Fatalf("ref %s: want exactly 1 fetch, got %d", id, f)
		}
		if d := counters.Decodes(id); d != 1 {
			t.Fatalf("ref %s: want exactly 1 decode, got %d", id, d)
		}
	}
}

// TestResolveCompleteAbsentTraceIsDescriptiveNotFound pins the FR-004 absence
// contract: a historical run id with no stored trace (zero refs from Query)
// fails with the same descriptive not-found error class as live mode — naming
// the run and the zero span count — never a nil-trace success (invariant §4).
func TestResolveCompleteAbsentTraceIsDescriptiveNotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	st, counters := newCountingStore(ctrl, nil) // no stored traces: Query returns zero refs

	c := New(func() string { return "run-gone" }, PollConfig{Interval: time.Hour, StableFor: 100, Timeout: time.Hour})
	// Bounded ctx: with the hour-scale hostile PollConfig, a regression into the
	// live polling loop must fail in seconds, not as a 10-minute test panic.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tr, err := c.ResolveComplete(ctx, st, "run-gone")
	if err == nil {
		t.Fatalf("want descriptive not-found error for absent trace, got nil (tr=%v)", tr)
	}
	if tr != nil {
		t.Fatalf("want nil trace on not-found, got %v", tr)
	}
	msg := err.Error()
	for _, want := range []string{`no trace for run "run-gone"`, "0 spans"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("not-found error missing %q (live-mode contract): %q", want, msg)
		}
	}
	// Absence is decided by the single pass — no retry rounds.
	if q := counters.Queries(); q != 1 {
		t.Fatalf("want exactly 1 query (no retry loop on absence), got %d", q)
	}
}

// TestResolveCompleteErrorPaths pins complete-or-loud for the known-complete
// mode: query, fetch and decode failures are wrapped hard errors with the SAME
// contract (message shape + errors.Is chain) as live resolution — never a
// partial forest, never a zero-value success.
func TestResolveCompleteErrorPaths(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("store exploded")

	tests := []struct {
		name     string
		setup    func(st *mocks.MockTraceStore)
		wantSubs []string
	}{
		{
			name: "query error is wrapped naming tag and value",
			setup: func(st *mocks.MockTraceStore) {
				st.EXPECT().Query(gomock.Any(), gomock.Any()).Return(nil, sentinel).Times(1)
			},
			wantSubs: []string{`tag="test.run.id"`, `value="run-cerr"`},
		},
		{
			name: "fetch error is wrapped with the live-mode get contract",
			setup: func(st *mocks.MockTraceStore) {
				st.EXPECT().Query(gomock.Any(), gomock.Any()).
					Return([]core.TraceRef{{TraceID: "abc"}}, nil).Times(1)
				st.EXPECT().FetchPayload(gomock.Any(), "abc").Return(nil, sentinel).Times(1)
			},
			wantSubs: []string{"correlate: get abc"},
		},
		{
			name: "decode error is wrapped with the live-mode decode contract",
			setup: func(st *mocks.MockTraceStore) {
				st.EXPECT().Query(gomock.Any(), gomock.Any()).
					Return([]core.TraceRef{{TraceID: "abc"}}, nil).Times(1)
				st.EXPECT().FetchPayload(gomock.Any(), "abc").Return([]byte("garbage"), nil).Times(1)
				st.EXPECT().DecodePayload("abc", gomock.Any()).Return(nil, sentinel).Times(1)
			},
			wantSubs: []string{"correlate: decode abc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			tt.setup(st)

			c := New(func() string { return "run-cerr" }, PollConfig{Interval: time.Hour, StableFor: 100, Timeout: time.Hour})
			tr, err := c.ResolveComplete(context.Background(), st, "run-cerr")
			if err == nil {
				t.Fatalf("want wrapped hard error, got nil (tr=%v)", tr)
			}
			if tr != nil {
				t.Fatalf("want nil trace on hard error, got %v", tr)
			}
			if !errors.Is(err, sentinel) {
				t.Fatalf("want errors.Is(err, sentinel), got: %v", err)
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error missing %q: %q", sub, err.Error())
				}
			}
		})
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

	st.EXPECT().FetchPayload(gomock.Any(), "t1").Return([]byte("payload-t1"), nil).AnyTimes()
	st.EXPECT().FetchPayload(gomock.Any(), "t2").Return([]byte("payload-t2"), nil).AnyTimes()
	st.EXPECT().DecodePayload("t1", gomock.Any()).Return(t1, nil).AnyTimes()
	st.EXPECT().DecodePayload("t2", gomock.Any()).Return(t2, nil).AnyTimes()

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

// --- Feature 005 (US1): structured slog narration ---

// logRecord is one captured slog record: its level, message, and attribute
// values rendered via slog.Value.String() (so the TraceQL query — which carries
// spaces and quotes that render awkwardly through the text handler — is asserted
// structurally rather than by fighting the text handler's escaping).
type logRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// capturingHandler is a slog.Handler that records every enabled record for
// structured assertion. Its Enabled honours the configured level so a test can
// prove a Debug-only record (resolve.poll) is suppressed at Info.
type capturingHandler struct {
	mu    *sync.Mutex
	recs  *[]logRecord
	level slog.Level
	pre   []slog.Attr
}

// newCapturingHandler returns a handler filtering at level and a snapshot getter
// for the records it captured.
func newCapturingHandler(level slog.Level) (*capturingHandler, func() []logRecord) {
	var mu sync.Mutex
	recs := &[]logRecord{}
	h := &capturingHandler{mu: &mu, recs: recs, level: level}
	return h, func() []logRecord {
		mu.Lock()
		defer mu.Unlock()
		out := make([]logRecord, len(*recs))
		copy(out, *recs)
		return out
	}
}

func (h *capturingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	for _, a := range h.pre {
		attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, logRecord{level: r.Level, msg: r.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}

func (h *capturingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	nh := *h
	nh.pre = append(append([]slog.Attr{}, h.pre...), as...)
	return &nh
}

func (h *capturingHandler) WithGroup(string) slog.Handler { return h }

// recByMsg returns the first captured record whose message equals msg.
func recByMsg(recs []logRecord, msg string) (logRecord, bool) {
	for _, r := range recs {
		if r.msg == msg {
			return r, true
		}
	}
	return logRecord{}, false
}

// captureStdio redirects the process's real stdout+stderr for the duration of fn
// and returns everything written to them — proving the SC-005 silent-default
// contract: with no injected logger the correlator must reach neither stream.
func captureStdio(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = w, w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout, os.Stderr = origOut, origErr
	return <-done
}

// TestResolveNarratesLifecycle pins the correlate half of US1: a successful
// stability-gated resolve narrates resolve.start (Info: run_id, store_endpoint,
// query), one resolve.poll per round (Debug: round, spans_seen, stable_streak),
// and resolve.done (Info: run_id, spans, roots, rounds, elapsed) — done only on
// the successful return.
func TestResolveNarratesLifecycle(t *testing.T) {
	const (
		endpoint = "http://localhost:3200"
		runID    = "run-cap"
	)
	ctrl := gomock.NewController(t)
	st, _ := newCountingStore(ctrl, []storedTrace{{id: "t1", tr: nSpanTrace(2, nil)}})

	h, records := newCapturingHandler(slog.LevelDebug)
	c := New(func() string { return runID },
		PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: time.Second},
		WithEndpoint(endpoint), WithLogger(slog.New(h)))
	got, err := c.Resolve(context.Background(), st, runID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.Spans) != 2 || len(got.Roots) != 1 {
		t.Fatalf("want 2 spans / 1 root, got %d / %d", len(got.Spans), len(got.Roots))
	}
	recs := records()

	// resolve.start — Info, at the top of Resolve.
	start, ok := recByMsg(recs, "resolve.start")
	if !ok {
		t.Fatalf("no resolve.start record; captured: %+v", recs)
	}
	if start.level != slog.LevelInfo {
		t.Fatalf("resolve.start level = %v, want Info", start.level)
	}
	if start.attrs["run_id"] != runID {
		t.Fatalf("resolve.start run_id = %q, want %q", start.attrs["run_id"], runID)
	}
	if start.attrs["store_endpoint"] != endpoint {
		t.Fatalf("resolve.start store_endpoint = %q, want %q", start.attrs["store_endpoint"], endpoint)
	}
	if want := `{ .test.run.id = "run-cap" }`; start.attrs["query"] != want {
		t.Fatalf("resolve.start query = %q, want %q", start.attrs["query"], want)
	}

	// resolve.poll — Debug, once per poll round, carrying round/spans_seen/stable_streak.
	polls := 0
	for _, r := range recs {
		if r.msg != "resolve.poll" {
			continue
		}
		polls++
		if r.level != slog.LevelDebug {
			t.Fatalf("resolve.poll level = %v, want Debug", r.level)
		}
		for _, k := range []string{"round", "spans_seen", "stable_streak"} {
			if _, ok := r.attrs[k]; !ok {
				t.Fatalf("resolve.poll missing %q: %+v", k, r)
			}
		}
	}
	if polls != 3 {
		t.Fatalf("want 3 resolve.poll records (1 changed + StableFor=2 stable), got %d", polls)
	}

	// resolve.done — Info, only on the successful stable return.
	done, ok := recByMsg(recs, "resolve.done")
	if !ok {
		t.Fatalf("no resolve.done record; captured: %+v", recs)
	}
	if done.level != slog.LevelInfo {
		t.Fatalf("resolve.done level = %v, want Info", done.level)
	}
	if done.attrs["run_id"] != runID {
		t.Fatalf("resolve.done run_id = %q, want %q", done.attrs["run_id"], runID)
	}
	if done.attrs["spans"] != "2" {
		t.Fatalf("resolve.done spans = %q, want 2", done.attrs["spans"])
	}
	if done.attrs["roots"] != "1" {
		t.Fatalf("resolve.done roots = %q, want 1", done.attrs["roots"])
	}
	if done.attrs["rounds"] != "3" {
		t.Fatalf("resolve.done rounds = %q, want 3 (final round count)", done.attrs["rounds"])
	}
	if _, ok := done.attrs["elapsed"]; !ok {
		t.Fatalf("resolve.done missing elapsed: %+v", done)
	}
}

// TestResolveInfoLevelSuppressesPollDebug proves resolve.poll is a Debug record:
// at an Info handler it must not be emitted, while the Info resolve.start and
// resolve.done still are.
func TestResolveInfoLevelSuppressesPollDebug(t *testing.T) {
	ctrl := gomock.NewController(t)
	st, _ := newCountingStore(ctrl, []storedTrace{{id: "t1", tr: nSpanTrace(2, nil)}})

	h, records := newCapturingHandler(slog.LevelInfo)
	c := New(func() string { return "run-info" },
		PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: time.Second},
		WithEndpoint("http://localhost:3200"), WithLogger(slog.New(h)))
	if _, err := c.Resolve(context.Background(), st, "run-info"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	recs := records()
	for _, r := range recs {
		if r.msg == "resolve.poll" {
			t.Fatalf("resolve.poll must be Debug; it leaked at Info: %+v", r)
		}
	}
	if _, ok := recByMsg(recs, "resolve.start"); !ok {
		t.Fatalf("resolve.start (Info) must still be emitted at Info level; captured: %+v", recs)
	}
	if _, ok := recByMsg(recs, "resolve.done"); !ok {
		t.Fatalf("resolve.done (Info) must still be emitted at Info level; captured: %+v", recs)
	}
}

// TestResolveSilentByDefaultEmitsZeroBytes pins SC-005 for the correlator: with
// the default (discard) logger a successful resolve writes nothing to the
// process's real stdout/stderr.
func TestResolveSilentByDefaultEmitsZeroBytes(t *testing.T) {
	ctrl := gomock.NewController(t)
	st, _ := newCountingStore(ctrl, []storedTrace{{id: "t1", tr: nSpanTrace(2, nil)}})

	c := New(func() string { return "run-silent" },
		PollConfig{Interval: time.Millisecond, StableFor: 2, Timeout: time.Second}) // no WithLogger
	out := captureStdio(t, func() {
		if _, err := c.Resolve(context.Background(), st, "run-silent"); err != nil {
			t.Errorf("resolve: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("silent default must emit zero bytes, got:\n%q", out)
	}
}
