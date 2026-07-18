package ctl

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/engine"
)

// These tests lock in the audit-C4 win (feature 004, FR-004): every historical/
// saved-run ctl path (replay, diff) resolves through the correlator's UNCHANGED
// known-complete seam ResolveComplete — a single fetch, no stability poll — and
// MUST NEVER route through the live settle/strict Resolve (feature 008). Routing
// an immutable historical trace through the settle barrier would reintroduce the
// stability sleep for no benefit and could hard-error on a run that is already
// complete. Since ResolveComplete and Resolve are separate seam methods (not a
// flag), these gomock guards catch a future refactor that swaps the call at the
// ctl boundary — a compile-green mistake the type system alone cannot see.

// TestReplayResolvesViaKnownCompleteNotLiveResolve pins the ctl replay path
// (ReplayFeature → engine pinned branch): a SAVED run resolves via ResolveComplete
// exactly once, and the live Resolve is forbidden. The target command is `false`,
// so any accidental drive would fail — proving replay never drives, only resolves.
func TestReplayResolvesViaKnownCompleteNotLiveResolve(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets:      map[string]config.Target{"bot": {Adapter: "shell", Command: []string{"false"}, MaxConcurrency: 1}},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)

	// Single known-complete fetch; live settle/strict Resolve is forbidden.
	cor.EXPECT().ResolveComplete(gomock.Any(), st, "hist-run").Return(sampleForest(), nil).Times(1)
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	eng, err := engine.Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("engine.Build: %v", err)
	}

	feature := writeTempFeature(t, `Feature: replay
  Scenario: stored run had the right tools
    Given the agent target "bot"
    When I run scenario "ignored"
    Then the agent calls tools in order:
      | search    |
      | summarize |
`)
	var b bytes.Buffer
	if err := ReplayFeature(context.Background(), eng, "hist-run", feature, "", &b); err != nil {
		t.Fatalf("replay should pass against the stored forest: %v\n%s", err, b.String())
	}
}

// TestDiffResolvesViaKnownCompleteNotLiveResolve pins the ctl diff path: each of
// the two historical runs resolves via ResolveComplete exactly once, and the live
// Resolve is forbidden on both. The correlator is mocked at the seam, so the store
// is never touched — this guard is purely about which correlator method diff routes
// through.
func TestDiffResolvesViaKnownCompleteNotLiveResolve(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl) // never touched: resolution is mocked at the correlator seam
	cor := mocks.NewMockCorrelator(ctrl)

	cor.EXPECT().ResolveComplete(gomock.Any(), st, "A").Return(toolForest("A", "search", "summarize"), nil).Times(1)
	cor.EXPECT().ResolveComplete(gomock.Any(), st, "B").Return(toolForest("B", "search", "delete_record"), nil).Times(1)
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	var buf bytes.Buffer
	if err := Diff(context.Background(), cor, st, "A", "B", &buf); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(buf.String(), "delete_record") {
		t.Fatalf("diff output missing the expected differing tool:\n%s", buf.String())
	}
}

// TestDiffKnownCompleteSingleFetchNoPoll pins the single-fetch / no-poll SHAPE at
// the store level: with a REAL correlator, the known-complete resolve queries each
// run's tag EXACTLY once (Times(1)) and fetches/decodes each ref once. The live
// settle Resolve re-Queries inside its stability poll loop, so these Times(1)
// bounds go RED the moment ctl is routed through it — this is the store-count proof
// of the audit-C4 win, matching the existing ctl diff test pattern.
func TestDiffKnownCompleteSingleFetchNoPoll(t *testing.T) {
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)

	for _, id := range []string{"A", "B"} {
		st.EXPECT().Query(gomock.Any(), core.TraceQuery{Tag: "test.run.id", Value: id}).
			Return([]core.TraceRef{{TraceID: id}}, nil).Times(1)
		st.EXPECT().FetchPayload(gomock.Any(), id).Return([]byte("payload-"+id), nil).Times(1)
	}
	st.EXPECT().DecodePayload("A", gomock.Any()).Return(toolForest("A", "search", "summarize"), nil).Times(1)
	st.EXPECT().DecodePayload("B", gomock.Any()).Return(toolForest("B", "search", "delete_record"), nil).Times(1)

	var buf bytes.Buffer
	if err := Diff(context.Background(), newTestCorrelator(), st, "A", "B", &buf); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(buf.String(), "delete_record") {
		t.Fatalf("diff output missing the expected differing tool:\n%s", buf.String())
	}
}
