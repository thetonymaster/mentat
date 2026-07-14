package correlate

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// runIDRe constrains run IDs to characters that are safe inside an
// OTEL_RESOURCE_ATTRIBUTES value (k=v,k=v format): it must be non-empty and must
// not contain the reserved delimiters ',' or '='.
var runIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// PollConfig controls the stable-poll behaviour of Resolve.
type PollConfig struct {
	Interval  time.Duration
	StableFor int // consecutive stable iterations required
	Timeout   time.Duration
}

type correlator struct {
	idFn func() string
	poll PollConfig
}

// New returns a core.Correlator that uses idFn to generate run IDs and poll
// according to the given PollConfig.
func New(idFn func() string, poll PollConfig) core.Correlator {
	return &correlator{idFn: idFn, poll: poll}
}

// Inject sets spec.RunID and spec.Tags["test.run.id"] to a fresh run ID and
// returns it (spec §5 — tag-first correlation).
func (c *correlator) Inject(_ context.Context, spec *core.RunSpec) string {
	if spec == nil {
		panic("correlate: Inject called with nil *RunSpec (engine must construct it)")
	}
	id := c.idFn()
	if !runIDRe.MatchString(id) {
		panic(fmt.Sprintf("correlate: idFn returned invalid run id %q (must match [A-Za-z0-9._:-]+; it becomes an OTEL resource-attribute value and must not contain delimiters)", id))
	}
	spec.RunID = id
	if spec.Tags == nil {
		spec.Tags = map[string]string{}
	}
	spec.Tags["test.run.id"] = id
	return id
}

// refObservation caches one trace ref's last observed payload signature and the
// forest decoded from exactly those bytes — the hashed bytes and the decoded
// bytes are the same fetch, so releasing a cached forest after stable
// observations never opens a partial-evidence window (feature 004, FR-002).
type refObservation struct {
	payloadLen  int
	payloadHash uint64
	forest      *trace.Trace
}

// hashPayload is the cheap per-round change signal: FNV-1a over the payload
// bytes (research R1). Compared together with the payload length.
func hashPayload(payload []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(payload) // hash.Hash.Write never returns an error
	return h.Sum64()
}

// refsKey canonicalizes a ref set for round-over-round comparison. TraceIDs are
// hex (no NUL bytes), so the separator is unambiguous.
func refsKey(refs []core.TraceRef) string {
	var sb strings.Builder
	for _, ref := range refs {
		sb.WriteString(ref.TraceID)
		sb.WriteByte(0)
	}
	return sb.String()
}

// Resolve queries the store for all traces tagged runID, fetches and merges them
// into one forest, and polls until the observation is stable for StableFor
// consecutive iterations. An observation is stable when the ref set and every
// ref's payload bytes (length + FNV-1a hash) are unchanged from the previous
// round — strictly stronger than the feature-002 span-count comparison
// (Clarifications 2026-07-11) — and unchanged payloads are NOT re-decoded: full
// decode happens at most once per trace per change (FR-002, audit C1). Zero
// traces within Timeout is a hard error (invariant §4).
func (c *correlator) Resolve(ctx context.Context, store core.TraceStore, runID string) (*trace.Trace, error) {
	deadline := time.Now().Add(c.poll.Timeout)
	stable := 0
	lastSpanCount := -1
	lastRefsKey := ""
	cache := map[string]*refObservation{}
	// Byte-churn diagnostics (FR-002 guard): resets caused by payload byte
	// changes while the merged span count stayed constant are counted and named
	// in the unstable-at-deadline error, so store-side byte churn is diagnosable
	// as such rather than mistaken for a growing trace.
	churnResets, churnSpanCount := 0, 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("correlate: context cancelled resolving run %q: %w", runID, err)
		}

		refs, err := store.Query(ctx, core.TraceQuery{Tag: "test.run.id", Value: runID})
		if err != nil {
			return nil, fmt.Errorf("correlate: query tag=%q value=%q: %w", "test.run.id", runID, err)
		}

		bytesChanged, err := observeRefs(ctx, store, refs, cache)
		if err != nil {
			return nil, err
		}
		// A changed ref SET is a changed observation too: a trace appearing or
		// disappearing changes the merged forest even at equal span counts.
		key := refsKey(refs)
		refSetChanged := key != lastRefsKey
		lastRefsKey = key
		changed := bytesChanged || refSetChanged

		m := mergeRefs(runID, refs, cache)
		count := len(m.Spans)

		if !changed && count > 0 {
			stable++
			if stable >= c.poll.StableFor {
				// The returned forests were decoded from exactly the bytes the
				// stable observations hashed — no re-fetch, no re-decode.
				return m, nil
			}
		} else {
			if bytesChanged && count > 0 && count == lastSpanCount {
				churnResets++
				churnSpanCount = count
			}
			stable = 0
		}
		lastSpanCount = count

		if time.Now().After(deadline) {
			if count == 0 {
				return nil, fmt.Errorf("correlate: no trace for run %q within %v (0 spans seen)", runID, c.poll.Timeout)
			}
			// Spans present but never observed stable: the trace was still growing at
			// the deadline. Returning it best-effort (audit A3) risks running
			// comparators against a partial forest, so this is a hard error naming the
			// run, the last span count, the stability progress, and the timeout
			// (invariant §4 — no silent fallbacks). Resets caused by byte churn at a
			// constant span count are named explicitly (Clarifications 2026-07-11).
			msg := fmt.Sprintf("correlate: run %q unstable at deadline: %d spans, %d/%d stable iterations within %v", runID, count, stable, c.poll.StableFor, c.poll.Timeout)
			if churnResets > 0 {
				msg += fmt.Sprintf("; payload hash changed %d times with span count constant at %d", churnResets, churnSpanCount)
			}
			return nil, errors.New(msg)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("correlate: context cancelled resolving run %q: %w", runID, ctx.Err())
		case <-time.After(c.poll.Interval):
		}
	}
}

// ResolveComplete resolves a KNOWN-COMPLETE (saved/historical) run: one tag
// query plus one concurrent fetch+decode pass — reusing the live loop's
// fan-out (observeRefs) — with no stability loop and no sleep; the PollConfig
// plays no part (feature 004, FR-004, audit C4). An absent trace (zero merged
// spans) is the same descriptive not-found error class as live mode; fetch and
// decode failures keep the live wrapped-error contracts (invariant §4).
func (c *correlator) ResolveComplete(ctx context.Context, store core.TraceStore, runID string) (*trace.Trace, error) {
	refs, err := store.Query(ctx, core.TraceQuery{Tag: "test.run.id", Value: runID})
	if err != nil {
		return nil, fmt.Errorf("correlate: query tag=%q value=%q: %w", "test.run.id", runID, err)
	}
	cache := map[string]*refObservation{}
	if _, err := observeRefs(ctx, store, refs, cache); err != nil {
		return nil, err
	}
	m := mergeRefs(runID, refs, cache)
	if len(m.Spans) == 0 {
		return nil, fmt.Errorf("correlate: no trace for run %q (0 spans seen)", runID)
	}
	return m, nil
}

// mergeRefs merges the cached forests of every ref, in ref order, into one
// run-level forest (invariant §2 — a run may span multiple root traces).
func mergeRefs(runID string, refs []core.TraceRef, cache map[string]*refObservation) *trace.Trace {
	m := &trace.Trace{RunID: runID}
	for _, ref := range refs {
		obs := cache[ref.TraceID]
		m.Roots = append(m.Roots, obs.forest.Roots...)
		m.Spans = append(m.Spans, obs.forest.Spans...)
	}
	return m
}

// observeRefs fetches every ref's payload and applies the byte-level change
// check against the cache: an unchanged payload reuses the cached forest
// without decoding; a changed payload is decoded — those same bytes — and the
// cache updated. Returns whether any ref's bytes changed. Fetch and decode
// failures are hard errors (complete-or-loud, invariant §4).
//
// Fetches fan out concurrently within the round (FR-003, audit C3, research
// R3): errgroup.WithContext runs one goroutine per ref; the first error cancels
// the round's siblings and fails resolution with the same wrapped error the
// serial loop produced. Results land in ref-indexed slots, so the caller's
// merge stays deterministic in ref order regardless of completion order. The
// cache is read inside the goroutines but written only after Wait — reads and
// writes never overlap.
func observeRefs(ctx context.Context, store core.TraceStore, refs []core.TraceRef, cache map[string]*refObservation) (bool, error) {
	observed := make([]*refObservation, len(refs))
	freshDecode := make([]bool, len(refs))
	g, gctx := errgroup.WithContext(ctx)
	for i, ref := range refs {
		g.Go(func() error {
			payload, err := store.FetchPayload(gctx, ref.TraceID)
			if err != nil {
				return fmt.Errorf("correlate: get %s: %w", ref.TraceID, err)
			}
			size, hash := len(payload), hashPayload(payload)
			if prev, ok := cache[ref.TraceID]; ok && prev.payloadLen == size && prev.payloadHash == hash {
				observed[i] = prev // byte-identical to the cached observation: no decode
				return nil
			}
			forest, err := store.DecodePayload(ref.TraceID, payload)
			if err != nil {
				return fmt.Errorf("correlate: decode %s: %w", ref.TraceID, err)
			}
			if forest == nil {
				return fmt.Errorf("correlate: decode %s returned nil trace", ref.TraceID)
			}
			observed[i] = &refObservation{payloadLen: size, payloadHash: hash, forest: forest}
			freshDecode[i] = true
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return false, err
	}
	changed := false
	for i, ref := range refs {
		cache[ref.TraceID] = observed[i]
		if freshDecode[i] {
			changed = true
		}
	}
	return changed, nil
}
