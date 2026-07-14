package correlate

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"regexp"
	"sort"
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

// zeroSpanChecklist is the curated triage list (research R8) appended ONLY to
// the zero-span timeout error: when no trace is found at all, these are the
// three things to check. The unstable-at-deadline error omits it — that trace
// exists, so it is a stability problem, not a "where is it" problem. The
// alignment of items (2)/(3) under item (1) is deliberate (readable in stderr).
const zeroSpanChecklist = "\n\tchecklist: (1) is the collector/Tempo up? (deploy: make harness-up)" +
	"\n\t           (2) does the SUT export OTLP to the endpoint above?" +
	"\n\t           (3) were OTEL_RESOURCE_ATTRIBUTES applied? (run with -vv to see injected env)"

// traceQLByRunID renders the exact TraceQL query the correlator issues for a run
// id, mirroring store/tempo.go's `{ .%s = "%s" }` with the tag-first correlation
// key (invariant §5). It is the single source of truth for the query text shared
// by the resolve.start narration and the enriched not-found/unstable errors, so
// the two can never drift.
func traceQLByRunID(runID string) string {
	return fmt.Sprintf("{ .%s = %q }", "test.run.id", runID)
}

// storeQueryLines renders the correlator's store endpoint and the exact TraceQL
// query it issued as two indented lines, led by a newline so it appends onto a
// one-line error message, so a live-resolve failure is diagnosable from the
// message alone (FR-003).
func (c *correlator) storeQueryLines(runID string) string {
	return fmt.Sprintf("\n\tstore: %s\n\tquery: %s", c.endpoint, traceQLByRunID(runID))
}

type correlator struct {
	idFn     func() string
	poll     PollConfig
	logger   *slog.Logger
	endpoint string
}

// New returns a core.Correlator that uses idFn to generate run IDs and poll
// according to the given PollConfig. Options are applied over a silent
// (discard-handler) logger default so the seam narrates nothing unless a caller
// opts in via WithLogger; the variadic keeps existing New(idFn, poll) call sites
// compiling.
func New(idFn func() string, poll PollConfig, opts ...Option) core.Correlator {
	o := resolveOptions(opts)
	return &correlator{idFn: idFn, poll: poll, logger: o.logger, endpoint: o.endpoint}
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

// canonicalRefs returns a copy of refs sorted by TraceID — the canonical ref
// order. A store's query order is not a contract (the same ref set may come
// back in a different order per call), so both resolution modes canonicalize
// immediately after every Query: the stability key (refsKey) becomes
// order-independent and the merge order (mergeRefs) deterministic. The store's
// slice is never mutated — sort a copy.
func canonicalRefs(refs []core.TraceRef) []core.TraceRef {
	out := make([]core.TraceRef, len(refs))
	copy(out, refs)
	sort.Slice(out, func(i, j int) bool { return out[i].TraceID < out[j].TraceID })
	return out
}

// refsKey flattens an already-canonical (sorted-by-TraceID, via canonicalRefs)
// ref set into a string for round-over-round comparison. TraceIDs are hex (no
// NUL bytes), so the separator is unambiguous.
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
// decode happens at most once per trace per change (FR-002, audit C1). The
// ref-set comparison is order-independent: refs are canonicalized (sorted by
// TraceID) after every query, so a store returning the same set in a different
// order is a stable observation. Zero traces within Timeout is a hard error
// (invariant §4).
func (c *correlator) Resolve(ctx context.Context, store core.TraceStore, runID string) (*trace.Trace, error) {
	start := time.Now()
	deadline := start.Add(c.poll.Timeout)
	c.logger.InfoContext(ctx, "resolve.start", "run_id", runID, "store_endpoint", c.endpoint, "query", traceQLByRunID(runID))
	round := 0
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
		round++
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("correlate: context cancelled resolving run %q: %w", runID, err)
		}

		refs, err := store.Query(ctx, core.TraceQuery{Tag: "test.run.id", Value: runID})
		if err != nil {
			return nil, fmt.Errorf("correlate: query tag=%q value=%q: %w", "test.run.id", runID, err)
		}
		refs = canonicalRefs(refs)

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
		} else {
			if bytesChanged && count > 0 && count == lastSpanCount {
				churnResets++
				churnSpanCount = count
			}
			stable = 0
		}
		c.logger.DebugContext(ctx, "resolve.poll", "round", round, "spans_seen", count, "stable_streak", stable)
		if !changed && count > 0 && stable >= c.poll.StableFor {
			// The returned forests were decoded from exactly the bytes the
			// stable observations hashed — no re-fetch, no re-decode.
			c.logger.InfoContext(ctx, "resolve.done", "run_id", runID, "spans", len(m.Spans), "roots", len(m.Roots), "rounds", round, "elapsed", time.Since(start))
			return m, nil
		}
		lastSpanCount = count

		if time.Now().After(deadline) {
			if count == 0 {
				return nil, fmt.Errorf("correlate: no trace for run %q within %v (0 spans seen)%s%s", runID, c.poll.Timeout, c.storeQueryLines(runID), zeroSpanChecklist)
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
			// FR-003: name the store and query so the failure is diagnosable from
			// the message alone — but NO checklist: the trace exists (spans
			// present), so this is a stability problem, not a "where is it" one.
			msg += c.storeQueryLines(runID)
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
// plays no part (feature 004, FR-004, audit C4). Refs are canonicalized the
// same way as live mode, so the merge is in sorted-TraceID order regardless of
// query order. An absent trace (zero merged spans) is the same descriptive
// not-found error class as live mode; fetch and decode failures keep the live
// wrapped-error contracts (invariant §4).
func (c *correlator) ResolveComplete(ctx context.Context, store core.TraceStore, runID string) (*trace.Trace, error) {
	refs, err := store.Query(ctx, core.TraceQuery{Tag: "test.run.id", Value: runID})
	if err != nil {
		return nil, fmt.Errorf("correlate: query tag=%q value=%q: %w", "test.run.id", runID, err)
	}
	refs = canonicalRefs(refs)
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

// mergeRefs merges the cached forests of every ref, in canonical sorted-TraceID
// order (callers pass canonicalRefs output), into one run-level forest
// (invariant §2 — a run may span multiple root traces).
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
