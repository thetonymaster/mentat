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

// completenessSentinelKey is the strict-mode in-trace declaration (contracts §2):
// the SUT sets it on exactly one span to the total span count of the whole merged
// run forest (all roots), INCLUDING the sentinel-bearing span itself. Strict
// resolution scans the merged forest for it each poll round and concludes only at
// exact equality (data-model "State machine per poll round").
const completenessSentinelKey = "test.span.count"

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
func (c *correlator) Resolve(ctx context.Context, store core.TraceStore, req core.ResolveRequest) (*trace.Trace, error) {
	// req.Contract carries the per-run completeness barriers. The settle-window
	// barrier (settle mode) is enforced in the poll loop below (008 T010): it gates
	// the conclusion behind req.Contract.Settle elapsing, additive over the 002
	// stability gate. A zero-value contract (Settle == 0, the pre-008 default) leaves
	// resolution byte-identical to the prior string-argument behaviour. Strict-mode
	// sentinel handling lands later (008 T022).
	runID := req.RunID
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

		// Feature-008 strict mode (008 T022): a per-round scan of the merged forest
		// for the test.span.count sentinel supersedes the settle window entirely
		// (data-model "State machine per poll round"). The SUT declares the whole
		// forest's self-inclusive span count in one span; resolution concludes ONLY
		// at exact equality, and every mismatch is a distinct hard error — never a
		// verdict over partial evidence (invariant §4). This is a wholly separate
		// termination path from the settle/002 gate below, keyed on Mode; settle-mode
		// callers never enter it, so their behaviour (and the 002 gate) is untouched.
		if req.Contract.Mode == "strict" {
			sentinels := sentinelSpans(m)
			// declared is parsed ONCE, in case 1 (the confirmed single sentinel), and
			// reused by the deadline path below — the deadline's count-short branch is
			// reachable only with exactly one sentinel that parsed here.
			var declared int
			switch len(sentinels) {
			case 0:
				// No declaration observed yet. The sentinel may arrive in any later
				// batch (contracts §2), so keep polling; concluding "no sentinel" from
				// this partial forest would false-fail a still-exporting run. Only the
				// deadline turns this into the missing-sentinel hard error.
			case 1:
				d, ok := sentinels[0].AttrInt(completenessSentinelKey)
				if !ok {
					// The sentinel key is present (sentinelSpans matched it) but its
					// value is non-integer. Discarding the ok bool here would yield a
					// silent declared=0 and a misleading "exceed …=0" / "of 0 declared"
					// error — a silent fallback (Constitution IV / FR-013). Name the run,
					// the sentinel span, and the raw value instead.
					return nil, fmt.Errorf("correlate: run %q: strict mode: sentinel span %s has non-integer test.span.count=%q", runID, sentinels[0].ID, sentinels[0].Attr(completenessSentinelKey))
				}
				declared = d
				switch {
				case count > declared:
					// The run emitted more spans than it declared: the declaration is
					// violated, so hard-error immediately.
					return nil, fmt.Errorf("correlate: run %q: strict mode: %d spans exceed declared test.span.count=%d", runID, count, declared)
				case count == declared:
					c.logger.InfoContext(ctx, "resolve.done", "run_id", runID, "spans", len(m.Spans), "roots", len(m.Roots), "rounds", round, "elapsed", time.Since(start), "mode", "strict")
					return m, nil
				}
				// count < declared: the run is still flushing toward its declared total;
				// keep polling until it reaches equality or the deadline fires.
			default:
				// ≥2 sentinels: which count is authoritative? Ambiguous declaration —
				// hard-error immediately, naming the offending span ids.
				return nil, fmt.Errorf("correlate: run %q: strict mode: %d sentinel spans found (want exactly 1): [%s]", runID, len(sentinels), strings.Join(sentinelIDs(sentinels), ", "))
			}
			c.logger.DebugContext(ctx, "resolve.poll", "round", round, "spans_seen", count, "sentinels", len(sentinels))

			if time.Now().After(deadline) {
				if count == 0 {
					// Zero spans at timeout keeps the UNCHANGED cross-mode not-found error
					// (data-model "all modes → zero spans → hard error (unchanged)"): the
					// trace never arrived, so the "where is it" triage checklist applies —
					// strict does not shadow it with a missing-sentinel message.
					return nil, fmt.Errorf("correlate: no trace for run %q within %v (0 spans seen)%s%s", runID, c.poll.Timeout, c.storeQueryLines(runID), zeroSpanChecklist)
				}
				if len(sentinels) == 0 {
					return nil, fmt.Errorf("correlate: run %q: strict mode: no test.span.count sentinel found within %v (%d spans seen)", runID, c.poll.Timeout, count)
				}
				// declared was parsed (and validated non-zero-ok) in case 1 above; this
				// branch is reached only with exactly one sentinel, so reuse it.
				return nil, fmt.Errorf("correlate: run %q: strict mode: %d of %d declared spans within %v", runID, count, declared, c.poll.Timeout)
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("correlate: context cancelled resolving run %q: %w", runID, ctx.Err())
			case <-time.After(c.poll.Interval):
			}
			continue
		}

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
		// Feature-002 stability gate: the observation is stable for StableFor rounds.
		stabilityMet := !changed && count > 0 && stable >= c.poll.StableFor
		// Feature-008 settle barrier (settle mode, 008 T010): a live resolution may
		// CONCLUDE only once the settle window measured from Resolve entry (=
		// drive-return, the engine calls this synchronously) has elapsed AND the 002
		// stability gate holds. The settle window is an ADDITIVE condition over the
		// unchanged 002 gate, never a replacement. Settle == 0 makes the window check
		// vacuously true, so this reduces byte-for-byte to the pre-008, 002-only
		// behaviour existing callers rely on. Late spans arriving inside the window
		// change the payload bytes, reset the 002 gate, and are therefore included in
		// the returned forest. (Strict mode — the test.span.count sentinel — is a
		// later task, 008 T022; it is not handled here.)
		if stabilityMet && time.Since(start) >= req.Contract.Settle {
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
			// Settle mode with a non-zero window (008 FR-013): the completeness
			// barrier — not the bare 002 gate — is what remained unmet, so name the
			// unsatisfied barrier per contracts §4. Which one depends on whether the
			// window itself has yet to elapse (spans may still be flushing) or the
			// window elapsed but the span count never stabilised. Settle == 0 falls
			// through to the byte-identical feature-002 unstable-at-deadline error
			// below, so no pre-008 caller sees a message change.
			if req.Contract.Settle > 0 {
				waitingOn := "span-count stability"
				if remaining := req.Contract.Settle - time.Since(start); remaining > 0 {
					waitingOn = fmt.Sprintf("settle window (%v remaining)", remaining)
				}
				return nil, fmt.Errorf("correlate: run %q: completeness not reached within %v: waiting on %s (spans seen: %d)%s", runID, c.poll.Timeout, waitingOn, count, c.storeQueryLines(runID))
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

// sentinelSpans returns every span in the merged forest bearing the strict-mode
// test.span.count sentinel attribute, in forest (canonical merge) order. It scans
// the WHOLE merged forest — every root — because the run's declared count spans
// all traces (invariant §2; contracts §2).
func sentinelSpans(m *trace.Trace) []*trace.Span {
	var out []*trace.Span
	for _, s := range m.Spans {
		if _, ok := s.Attrs[completenessSentinelKey]; ok {
			out = append(out, s)
		}
	}
	return out
}

// sentinelIDs projects sentinel-bearing spans onto their span ids, for the
// ambiguous-declaration (≥2 sentinels) error.
func sentinelIDs(spans []*trace.Span) []string {
	ids := make([]string, len(spans))
	for i, s := range spans {
		ids[i] = s.ID
	}
	return ids
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
		m.TraceIDs = append(m.TraceIDs, ref.TraceID)
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
