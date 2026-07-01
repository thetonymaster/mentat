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
	results := make([]core.JudgeVerdict, votes)
	g, gctx := errgroup.WithContext(ctx)
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
	for _, jv := range results {
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
		return core.Verdict{Pass: true}, nil
	case noMatchCount > matchCount:
		// FR-008: a failing verdict must always carry a non-empty, human-readable
		// reason. If every no-match vote returned a blank reason, substitute a
		// placeholder rather than emit a useless empty reason.
		if strings.TrimSpace(noMatchReason) == "" {
			noMatchReason = "semantic judge returned no-match without a reason"
		}
		return core.Verdict{Pass: false, Reasons: []string{noMatchReason}}, nil
	default:
		return core.Verdict{}, fmt.Errorf("semantic: %d-vote tie (%d match / %d no-match); majority is undefined", votes, matchCount, noMatchCount)
	}
}
