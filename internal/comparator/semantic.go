package comparator

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/thetonymaster/mentat/internal/core"
)

// semanticMatcher is the "semantic" result matcher. It judges whether the run's
// final answer (Evidence.Output.Answer) *means* the author's expected meaning,
// delegating each verdict to a core.Judge seam. It reads Evidence only — never a
// TraceStore or Driver (Constitution I) — and never returns a guessed verdict:
// a judge failure or an undecidable tally is a hard error (Constitution IV).
type semanticMatcher struct {
	judge core.Judge
	votes int
}

// NewSemantic builds the "semantic" matcher over judge j with best-of-N voting.
// votes < 1 is clamped to 1 (defence in depth — the config layer already
// guarantees an odd votes >= 1).
func NewSemantic(j core.Judge, votes int) core.Matcher {
	return semanticMatcher{judge: j, votes: votes}
}

func (semanticMatcher) Name() string { return "semantic" }

// maxConcurrentVotes bounds the goroutine fan-out in Match. Best-of-N voting is
// realistically small (1/3/5/7) and runs fully concurrent under this cap; a
// pathological judge.votes would otherwise spawn that many simultaneous judge
// round-trips per checked step and, across concurrently-running scenarios, risk
// thundering-herd rate limits and cost spikes against the backend. Larger N still
// runs to completion, just in bounded waves — the index-ordered results keep the
// verdict deterministic regardless of the cap.
const maxConcurrentVotes = 8

// Match runs the judge `votes` times over (Candidate=ev.Output.Answer,
// Expected=want) and returns the strict-majority verdict. target is unused in v1
// (final answer only). A blank want fails fast before any judge call (FR-013);
// any judge error is wrapped, never swallowed (FR-007); an even-vote tie is a
// hard error (FR-015). A failing majority carries a judge no-match reason (FR-008).
func (m semanticMatcher) Match(ctx context.Context, ev core.Evidence, want, _ string) (core.Verdict, error) {
	if strings.TrimSpace(want) == "" {
		return core.Verdict{}, fmt.Errorf(`semantic: expected meaning is empty; the result means "..." requires a non-empty meaning`)
	}

	votes := m.votes
	if votes < 1 {
		votes = 1
	}

	req := core.JudgeRequest{Candidate: ev.Output.Answer, Expected: want}

	// Run the best-of-N votes concurrently: for a network-backed judge this keeps
	// per-check latency at ~one round-trip instead of N. Results land in an
	// index-ordered slice so the "first no-match reason" below is deterministically
	// the lowest-index no-match vote — independent of goroutine completion order.
	// SetLimit bounds the fan-out (see maxConcurrentVotes); g.Go then blocks the
	// dispatch loop once the cap is reached, pacing large N into waves.
	results := make([]core.JudgeVerdict, votes)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentVotes)
	for i := 0; i < votes; i++ {
		g.Go(func() error {
			jv, err := m.judge.Judge(gctx, req)
			if err != nil {
				return fmt.Errorf("semantic: judge vote %d/%d: %w", i+1, votes, err)
			}
			results[i] = jv
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return core.Verdict{}, err
	}

	var matchCount, noMatchCount int
	var noMatchReason string
	// Sum each completed vote's token usage into one summable ledger row for this
	// verdict (US6). Every vote goes to the same judge, so carry the (last non-empty)
	// model id; a zero-value/unmetered vote contributes nothing. usage.Calls therefore
	// counts the judge calls actually made (== votes on the metered success path).
	var usage core.JudgeUsage
	for _, jv := range results {
		usage.Calls += jv.Usage.Calls
		usage.InputTokens += jv.Usage.InputTokens
		usage.OutputTokens += jv.Usage.OutputTokens
		if jv.Usage.Model != "" {
			usage.Model = jv.Usage.Model
		}
		if jv.Match {
			matchCount++
			continue
		}
		noMatchCount++
		if noMatchReason == "" {
			noMatchReason = jv.Reason
		}
	}

	switch {
	case matchCount > noMatchCount:
		return core.Verdict{Pass: true, Judge: &usage}, nil
	case noMatchCount > matchCount:
		// FR-008: a failing verdict must always carry a non-empty, human-readable
		// reason. If every no-match vote returned a blank reason, substitute a
		// placeholder rather than emit a useless empty reason.
		if strings.TrimSpace(noMatchReason) == "" {
			noMatchReason = "semantic judge returned no-match without a reason"
		}
		return core.Verdict{Pass: false, Reasons: []string{noMatchReason}, Judge: &usage}, nil
	default:
		// Config-unreachable (votes is guaranteed odd >= 1): this returns a hard error
		// and no verdict, so usage is intentionally not attached to a discarded verdict.
		return core.Verdict{}, fmt.Errorf("semantic: %d-vote tie (%d match / %d no-match); majority is undefined", votes, matchCount, noMatchCount)
	}
}
