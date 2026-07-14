package correlate

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/trace"
)

// This file is test INFRASTRUCTURE for feature 004 (correlation performance):
// counting and delayed-availability TraceStore stubs consumed by the
// resolution tests. It defines helpers only — no behaviour assertions live
// here.
//
// Both stores are gomock-backed (mocks.MockTraceStore with DoAndReturn
// wrappers, per the repo mock convention) and safe for concurrent use:
// resolution fans fetches out concurrently, so every counter is mutex-guarded
// and all stub state is immutable after construction.

// storeCounters records TraceStore call counts (per trace ID for fetch/decode)
// plus an ordered event log. Safe for concurrent use.
//
// The event log lets tests reconstruct per-round stability decisions: each
// "query" event opens a poll round; a "decode <id>" inside a round marks it as
// a changed (reset) observation, a round with only "fetch <id>" events is an
// unchanged (stable) observation. The FR-006 observation-parity test replays
// the feature-002 corpus sequences through this log.
type storeCounters struct {
	mu      sync.Mutex
	queries int
	fetches map[string]int
	decodes map[string]int
	events  []string
}

func newStoreCounters() *storeCounters {
	return &storeCounters{fetches: make(map[string]int), decodes: make(map[string]int)}
}

// Queries returns how many times Query was called.
func (c *storeCounters) Queries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queries
}

// Fetches returns how many times FetchPayload was called with the given trace ID.
func (c *storeCounters) Fetches(traceID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetches[traceID]
}

// TotalFetches returns the FetchPayload call count summed across all trace IDs.
func (c *storeCounters) TotalFetches() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, n := range c.fetches {
		total += n
	}
	return total
}

// Decodes returns how many times DecodePayload was called with the given trace ID.
func (c *storeCounters) Decodes(traceID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decodes[traceID]
}

// TotalDecodes returns the DecodePayload call count summed across all trace IDs.
func (c *storeCounters) TotalDecodes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, n := range c.decodes {
		total += n
	}
	return total
}

// Events returns a copy of the ordered call log ("query", "fetch <id>",
// "decode <id>").
func (c *storeCounters) Events() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.events))
	copy(out, c.events)
	return out
}

func (c *storeCounters) countQuery() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries++
	c.events = append(c.events, "query")
}

func (c *storeCounters) countFetch(traceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetches[traceID]++
	c.events = append(c.events, "fetch "+traceID)
}

func (c *storeCounters) countDecode(traceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decodes[traceID]++
	c.events = append(c.events, "decode "+traceID)
}

// storedTrace pairs a trace ID (the TraceRef value Query returns) with the
// trace served for it. Slice order is the Query ref order — ref-order merge
// determinism in resolution tests depends on it.
type storedTrace struct {
	id string
	tr *trace.Trace
}

// canonicalPayload mirrors InMemStore's hermetic payload definition: a
// deterministic canonical serialization of the forest (encoding/json sorts map
// keys), so content-identical traces are byte-identical across rounds.
func canonicalPayload(tr *trace.Trace) []byte {
	b, err := json.Marshal(tr)
	if err != nil {
		panic(fmt.Sprintf("counterstore: canonical serialization: %v", err))
	}
	return b
}

// indexStored derives the Query ref slice (preserving order), the payload map,
// and the trace lookup map from a stored-trace list. Payloads are derived once
// at construction so repeated fetches are byte-identical.
func indexStored(stored []storedTrace) ([]core.TraceRef, map[string][]byte, map[string]*trace.Trace) {
	refs := make([]core.TraceRef, len(stored))
	payloads := make(map[string][]byte, len(stored))
	byID := make(map[string]*trace.Trace, len(stored))
	for i, s := range stored {
		refs[i] = core.TraceRef{TraceID: s.id}
		payloads[s.id] = canonicalPayload(s.tr)
		byID[s.id] = s.tr
	}
	return refs, payloads, byID
}

// queryFunc / fetchFunc / decodeFunc are the DoAndReturn handler shapes for the
// counted TraceStore calls.
type (
	queryFunc  func(ctx context.Context, q core.TraceQuery) ([]core.TraceRef, error)
	fetchFunc  func(ctx context.Context, id string) ([]byte, error)
	decodeFunc func(id string, payload []byte) (*trace.Trace, error)
)

// newCountingStoreFuncs returns a gomock TraceStore whose Query, FetchPayload
// and DecodePayload delegate to the given handlers, counting every call (per
// trace ID for fetch/decode). Use this variant when a test needs custom
// per-poll behaviour (e.g. changing payload sequences); newCountingStore covers
// the fixed-trace case. Caps() is stubbed to the zero StoreCaps.
func newCountingStoreFuncs(ctrl *gomock.Controller, query queryFunc, fetch fetchFunc, decode decodeFunc) (*mocks.MockTraceStore, *storeCounters) {
	counters := newStoreCounters()
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Caps().Return(core.StoreCaps{}).AnyTimes()
	st.EXPECT().Query(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
			counters.countQuery()
			return query(ctx, q)
		}).AnyTimes()
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, id string) ([]byte, error) {
			counters.countFetch(id)
			return fetch(ctx, id)
		}).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(id string, payload []byte) (*trace.Trace, error) {
			counters.countDecode(id)
			return decode(id, payload)
		}).AnyTimes()
	return st, counters
}

// newCountingStore returns a gomock TraceStore serving a fixed set of traces,
// with all calls counted. Query returns one TraceRef per stored trace in slice
// order (whatever the queried tag/value); FetchPayload returns the trace's
// canonical payload (byte-identical every round); DecodePayload returns the
// stored trace for its ID. Unknown IDs are descriptive errors (no silent nils).
func newCountingStore(ctrl *gomock.Controller, stored []storedTrace) (*mocks.MockTraceStore, *storeCounters) {
	refs, payloads, byID := indexStored(stored)
	return newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			return refs, nil
		},
		func(_ context.Context, id string) ([]byte, error) {
			p, ok := payloads[id]
			if !ok {
				return nil, fmt.Errorf("counterstore: no stored trace with id %q", id)
			}
			return p, nil
		},
		func(id string, _ []byte) (*trace.Trace, error) {
			tr, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("counterstore: no stored trace with id %q", id)
			}
			return tr, nil
		})
}

// newDelayedStore returns a gomock TraceStore that simulates trace-ingestion
// lag: until lag has elapsed since start, Query succeeds with zero refs (a
// live Tempo pre-ingestion) and FetchPayload returns a descriptive not-found
// error; once elapsed, both serve the stored traces exactly as
// newCountingStore does. Pass start=time.Now() for "lag since construction".
// Visibility is decided by comparing time.Since(start) to lag — no sleeps —
// and start/lag/stored are immutable after construction, so the store is safe
// for concurrent use.
func newDelayedStore(ctrl *gomock.Controller, lag time.Duration, start time.Time, stored []storedTrace) (*mocks.MockTraceStore, *storeCounters) {
	refs, payloads, byID := indexStored(stored)
	available := func() bool { return time.Since(start) >= lag }
	return newCountingStoreFuncs(ctrl,
		func(context.Context, core.TraceQuery) ([]core.TraceRef, error) {
			if !available() {
				return nil, nil
			}
			return refs, nil
		},
		func(_ context.Context, id string) ([]byte, error) {
			if !available() {
				return nil, fmt.Errorf("counterstore: trace %q not yet available (lag %v, elapsed %v since start)", id, lag, time.Since(start))
			}
			p, ok := payloads[id]
			if !ok {
				return nil, fmt.Errorf("counterstore: no stored trace with id %q", id)
			}
			return p, nil
		},
		func(id string, _ []byte) (*trace.Trace, error) {
			tr, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("counterstore: no stored trace with id %q", id)
			}
			return tr, nil
		})
}
