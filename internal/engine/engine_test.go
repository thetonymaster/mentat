package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/trace"
)

// stubStoredTrace stubs the feature-004 store seam pair (FetchPayload +
// DecodePayload) on st to serve tr for every trace id, with a byte-stable
// payload so the correlator's stability gate converges exactly as the old
// constant-trace GetByID stub did.
func stubStoredTrace(st *mocks.MockTraceStore, tr *trace.Trace) {
	st.EXPECT().FetchPayload(gomock.Any(), gomock.Any()).Return([]byte("payload"), nil).AnyTimes()
	st.EXPECT().DecodePayload(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()
}

func TestDriveProducesEvidenceFromStore(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "run-1"}}, nil).AnyTimes()
	stubStoredTrace(st, tr)

	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ev, err := eng.Drive(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if ev.RunID != "run-1" || ev.Output.Answer != "hi" || len(ev.Trace.Spans) != 1 {
		t.Fatalf("evidence wrong: %+v", ev)
	}
}

func TestDriveUnknownTarget(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{
			name:    "unknown target returns descriptive error",
			target:  "nonexistent",
			wantErr: `unknown target "nonexistent"`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets:      map[string]config.Target{},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			_, err = eng.Drive(context.Background(), tt.target, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestDriveUnknownAdapter(t *testing.T) {
	tests := []struct {
		name    string
		adapter string
		wantErr string
	}{
		{
			name:    "unknown adapter returns descriptive error",
			adapter: "unknownadapter",
			wantErr: `no driver for adapter "unknownadapter"`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets: map[string]config.Target{
					"mytarget": {Adapter: tt.adapter, Command: []string{"echo"}, MaxConcurrency: 1},
				},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			_, err = eng.Drive(context.Background(), "mytarget", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestComparatorLookup(t *testing.T) {
	tests := []struct {
		name       string
		comparator string
		wantFound  bool
	}{
		{"sequence comparator registered", "sequence", true},
		{"budgets comparator registered", "budgets", true},
		{"result comparator registered", "result", true},
		{"unknown comparator not found", "notexist", false},
	}
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, found := eng.Comparator(tt.comparator)
			if found != tt.wantFound {
				t.Fatalf("Comparator(%q) found=%v want=%v", tt.comparator, found, tt.wantFound)
			}
		})
	}
}

func TestDriveResolveError(t *testing.T) {
	tests := []struct {
		name        string
		wantErrSubs []string
	}{
		{
			name:        "resolve failure is wrapped with engine prefix and run id",
			wantErrSubs: []string{"engine: resolve run", "run-1", "store down"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets: map[string]config.Target{
					"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
				},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			cor := mocks.NewMockCorrelator(ctrl)
			cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1")
			cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("store down"))

			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			_, err = eng.Drive(context.Background(), "echo", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			for _, sub := range tt.wantErrSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q does not contain %q", err.Error(), sub)
				}
			}
		})
	}
}

func TestDriveSemaphoreRespectsContextCancellation(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{
			name:    "cancelled context returns error instead of blocking on full semaphore",
			wantErr: "engine: drive",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets: map[string]config.Target{
					"sut": {Adapter: "shell", Command: []string{"echo", "hi"}, MaxConcurrency: 1},
				},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			// Driver must NOT be called — context is cancelled before slot is free.
			cor := mocks.NewMockCorrelator(ctrl)
			cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-cancel").AnyTimes()

			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			// Fill the single slot so any Drive call must wait.
			eng.sems["sut"] <- struct{}{}

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-cancel

			done := make(chan error, 1)
			go func() {
				_, runErr := eng.Drive(ctx, "sut", nil)
				done <- runErr
			}()
			select {
			case err = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("Drive blocked waiting on semaphore after context cancellation")
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled in error chain, got: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}

			// Release the slot we filled manually so the engine is clean.
			<-eng.sems["sut"]
		})
	}
}

func TestDrivePinned(t *testing.T) {
	pinnedTrace := &trace.Trace{Spans: []*trace.Span{{Name: "stored"}}, Roots: []*trace.Span{{Name: "stored"}}}

	tests := []struct {
		name        string
		runID       string
		resolveRet  *trace.Trace
		resolveErr  error
		wantErrSubs []string
		wantRunID   string
	}{
		{
			name:       "pinned happy path returns evidence without injecting or driving",
			runID:      "pinned-abc",
			resolveRet: pinnedTrace,
			wantRunID:  "pinned-abc",
		},
		{
			name:        "pinned resolve-error path returns wrapped error with run id",
			runID:       "pinned-xyz",
			resolveErr:  errors.New("trace not found"),
			wantErrSubs: []string{"pinned-xyz", "trace not found"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets:      map[string]config.Target{},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			cor := mocks.NewMockCorrelator(ctrl)
			// Inject must NOT be called — the absent EXPECT proves drive/inject is bypassed.
			// A pinned run is by definition saved/historical (plan U1): the pinned
			// branch must use the known-complete mode. Resolve(...).Times(0) forbids
			// the live stability-gated path (FR-004: live-only, never historical).
			cor.EXPECT().ResolveComplete(gomock.Any(), gomock.Any(), tt.runID).Return(tt.resolveRet, tt.resolveErr)
			cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			eng.PinRun(tt.runID)

			ev, err := eng.Drive(context.Background(), "unused", nil)
			if len(tt.wantErrSubs) > 0 {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				for _, sub := range tt.wantErrSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Drive pinned: %v", err)
			}
			if ev.RunID != tt.wantRunID {
				t.Fatalf("RunID got %q, want %q", ev.RunID, tt.wantRunID)
			}
			if ev.Trace != tt.resolveRet {
				t.Fatalf("Trace got %v, want %v", ev.Trace, tt.resolveRet)
			}
		})
	}
}

func TestDriveRunError(t *testing.T) {
	tests := []struct {
		name        string
		command     []string
		wantErrSubs []string
	}{
		{
			name:        "nonexistent binary causes driver error wrapped with engine: drive",
			command:     []string{"/nonexistent/mentat-no-such-binary"},
			wantErrSubs: []string{"engine: drive"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets: map[string]config.Target{
					"sut": {Adapter: "shell", Command: tt.command, MaxConcurrency: 1},
				},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			cor := correlate.New(func() string { return "run-x" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			_, err = eng.Drive(context.Background(), "sut", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			for _, sub := range tt.wantErrSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q does not contain %q", err.Error(), sub)
				}
			}
		})
	}
}

// TestDriveOnceFailureEvidence pins the A2/A6 contract on the Evidence a failed
// driveOnce returns: FailureMsg carries the wrapped engine error on every failure
// path, and a resolve failure RETAINS the real driver Output (the driver did
// succeed) — it is no longer dropped.
func TestDriveOnceFailureEvidence(t *testing.T) {
	tests := []struct {
		name            string
		mode            string // "resolve" | "driver"
		command         []string
		wantFailureKind string
		wantAnswer      string // real driver Output retained iff resolve failure
		wantMsgSub      string
	}{
		{
			name:            "resolve failure retains real driver output and carries msg",
			mode:            "resolve",
			command:         []string{"sh", "-c", "echo hi"},
			wantFailureKind: core.FailureKindResolve,
			wantAnswer:      "hi",
			wantMsgSub:      "resolve",
		},
		{
			name:            "driver failure carries wrapped driver error msg",
			mode:            "driver",
			command:         []string{"/nonexistent/mentat-no-such-binary"},
			wantFailureKind: core.FailureKindDriver,
			wantAnswer:      "",
			wantMsgSub:      "engine: drive",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets: map[string]config.Target{
					"sut": {Adapter: "shell", Command: tt.command, MaxConcurrency: 1},
				},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			var cor core.Correlator
			if tt.mode == "resolve" {
				mc := mocks.NewMockCorrelator(ctrl)
				mc.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1")
				mc.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("store down"))
				cor = mc
			} else {
				cor = correlate.New(func() string { return "run-x" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
			}
			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			ev, err := eng.driveOnce(context.Background(), "sut", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !ev.Failed {
				t.Fatalf("ev.Failed = false, want true: %+v", ev)
			}
			if ev.FailureKind != tt.wantFailureKind {
				t.Fatalf("FailureKind = %q, want %q", ev.FailureKind, tt.wantFailureKind)
			}
			if ev.Output.Answer != tt.wantAnswer {
				t.Fatalf("Output.Answer = %q, want %q (real driver output must be retained on resolve failure)", ev.Output.Answer, tt.wantAnswer)
			}
			if ev.FailureMsg == "" {
				t.Fatal("FailureMsg is empty; want the wrapped engine error text")
			}
			if ev.FailureMsg != err.Error() {
				t.Fatalf("FailureMsg = %q, want equal to returned error %q", ev.FailureMsg, err.Error())
			}
			if !strings.Contains(ev.FailureMsg, tt.wantMsgSub) {
				t.Fatalf("FailureMsg %q does not contain %q", ev.FailureMsg, tt.wantMsgSub)
			}
		})
	}
}

func TestAggregateComparatorLookup(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := eng.AggregateComparator("aggregate-cel"); !ok {
		t.Fatalf("aggregate-cel must be registered")
	}
	if _, ok := eng.AggregateComparator("nope"); ok {
		t.Fatalf("unknown aggregate comparator must not be found")
	}
}

func TestDriveNCollectsSamples(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	stubStoredTrace(st, tr)

	var mu sync.Mutex
	var n int
	cor := correlate.New(func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("run-%d", n)
	}, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "echo", nil, 3, false)
	if err != nil {
		t.Fatalf("DriveN: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("got %d samples, want 3", len(evs))
	}
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Failed {
			t.Fatalf("unexpected failed sample: %+v", ev)
		}
		seen[ev.RunID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("run ids not distinct: %v", seen)
	}
}

func TestDriveNResolveFailureBecomesSample(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-x").Times(2)
	// first resolve OK, second fails -> failed sample, not an aborted batch.
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}}
	gomock.InOrder(
		cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), "run-x").Return(tr, nil),
		cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), "run-x").Return(nil, errors.New("store down")),
	)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "echo", nil, 2, false)
	if err != nil {
		t.Fatalf("DriveN must not error on a per-run resolve failure: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d samples, want 2", len(evs))
	}
	if evs[0].Failed {
		t.Fatalf("first run should have succeeded")
	}
	if !evs[1].Failed || evs[1].FailureKind != core.FailureKindResolve {
		t.Fatalf("second run want failed/resolve, got %+v", evs[1])
	}
}

func TestDriveNPinnedRejectsMulti(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	eng.PinRun("pinned-1")
	if _, err := eng.DriveN(context.Background(), "x", nil, 2, false); err == nil {
		t.Fatalf("pinned + n>1 must error")
	}
}

// TestDriveNPinnedSingleRunResolvesStoredRunWithoutDriving pins the replay
// routing (feature 004, plan U1 + FR-004): the godog steps always call DriveN
// (never Drive), so a PINNED engine's n=1 DriveN must take the same
// known-complete pinned branch as Drive — resolve the stored run id via
// ResolveComplete, with no Inject, no SUT execution, and no live stability
// poll. Before this pin, DriveN(n=1) bypassed the pinned branch into
// driveOnce's live path: it re-DROVE the SUT and resolved a FRESH injected run
// id, so replay evaluated a new run instead of the stored one.
func TestDriveNPinnedSingleRunResolvesStoredRunWithoutDriving(t *testing.T) {
	pinnedTrace := &trace.Trace{Spans: []*trace.Span{{Name: "stored"}}, Roots: []*trace.Span{{Name: "stored"}}}
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		// No targets: any attempt to live-drive fails with "unknown target", so a
		// successful DriveN proves the pinned resolve-only branch was taken.
		Targets: map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	// Inject must NOT be called (no EXPECT): replay never mints a fresh run id.
	cor.EXPECT().ResolveComplete(gomock.Any(), gomock.Any(), "pinned-n1").Return(pinnedTrace, nil)
	// The live stability-gated mode is forbidden for a pinned run (FR-004).
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	eng.PinRun("pinned-n1")

	evs, err := eng.DriveN(context.Background(), "unused", nil, 1, false)
	if err != nil {
		t.Fatalf("DriveN pinned n=1: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("want 1 evidence, got %d", len(evs))
	}
	if evs[0].RunID != "pinned-n1" {
		t.Fatalf("RunID: want %q, got %q", "pinned-n1", evs[0].RunID)
	}
	if evs[0].Trace != pinnedTrace {
		t.Fatalf("Trace: want the stored pinned forest, got %v", evs[0].Trace)
	}
}

func TestDriveNInvalidN(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := eng.DriveN(context.Background(), "x", nil, 0, false); err == nil {
		t.Fatalf("n=0 must error")
	}
}

func TestDriveNParallel(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 4},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "t"}}, nil).AnyTimes()
	stubStoredTrace(st, tr)

	var mu sync.Mutex
	var counter int
	cor := correlate.New(func() string {
		mu.Lock()
		defer mu.Unlock()
		counter++
		return fmt.Sprintf("run-%d", counter)
	}, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "echo", nil, 3, true)
	if err != nil {
		t.Fatalf("DriveN parallel: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("got %d samples, want 3", len(evs))
	}
}

func TestDriveNSerialCancelledContext(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"echo": {Adapter: "shell", Command: []string{"sh", "-c", "echo hi"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	_, err = eng.DriveN(ctx, "echo", nil, 3, false)
	if err == nil {
		t.Fatalf("cancelled context must error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled in chain, got: %v", err)
	}
}

func TestDriveNParallelStructuralError(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// "unknown target" is structural — parallel DriveN must return it as error
	_, err = eng.DriveN(context.Background(), "nosuch", nil, 2, true)
	if err == nil {
		t.Fatalf("structural error in parallel mode must abort")
	}
}

func TestDriveNSerialStructuralError(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := correlate.New(func() string { return "run-1" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := eng.DriveN(context.Background(), "nosuch", nil, 3, false); err == nil || !strings.Contains(err.Error(), `unknown target "nosuch"`) {
		t.Fatalf("expected serial structural error to abort with unknown-target message, got: %v", err)
	}
}

func TestEngine_Pricing(t *testing.T) {
	cfg := config.Config{Pricing: config.Pricing{"m": {InputPerMTok: 1, OutputPerMTok: 2}}}
	eng, err := Build(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if eng.Pricing()["m"].InputPerMTok != 1 {
		t.Errorf("pricing not exposed: %+v", eng.Pricing())
	}
	if eng.Pricing()["m"].OutputPerMTok != 2 {
		t.Errorf("OutputPerMTok not exposed: %+v", eng.Pricing())
	}
}

func TestDriveHTTPTarget(t *testing.T) {
	var gotScenario, gotBaggage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotScenario = r.Header.Get("X-Scenario")
		gotBaggage = r.Header.Get("baggage")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"confirmed"}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"checkout": {
				Adapter:        "http",
				MaxConcurrency: 8,
				HTTP:           config.HTTP{URL: srv.URL, Method: http.MethodPost},
			},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "POST", Attrs: map[string]string{"service.name": "gateway"}}}}

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	st.EXPECT().Query(gomock.Any(), gomock.Any()).Return([]core.TraceRef{{TraceID: "run-http"}}, nil).AnyTimes()
	stubStoredTrace(st, tr)

	cor := correlate.New(func() string { return "run-http" }, correlate.PollConfig{Interval: time.Millisecond, StableFor: 1, Timeout: time.Second})

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ev, err := eng.Drive(context.Background(), "checkout", []string{"--scenario", "happy"})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}
	if ev.Output.Status != http.StatusCreated {
		t.Errorf("Status = %d, want 201", ev.Output.Status)
	}
	if ev.Output.Answer != `{"status":"confirmed"}` {
		t.Errorf("Answer = %q", ev.Output.Answer)
	}
	if gotScenario != "happy" {
		t.Errorf("SUT saw X-Scenario = %q, want happy", gotScenario)
	}
	if !strings.Contains(gotBaggage, "test.run.id=run-http") {
		t.Errorf("SUT saw baggage %q, missing test.run.id=run-http", gotBaggage)
	}
}

// TestDriveOnceRunBudget pins feature 003 (US1): the engine derives a per-run
// context.WithTimeout from the scenario context and the target's resolved budget,
// attributes a timeout to the phase in flight (drive/resolve), and skips the
// timeout entirely for an unbounded budget. A non-positive Timeout means "no
// per-run bound" (a zero-value budget from a hand-built config), never an
// instant-expiring 0-duration deadline.
func TestDriveOnceRunBudget(t *testing.T) {
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	tests := []struct {
		name         string
		command      []string
		budget       config.RunBudget
		setupCor     func(ctrl *gomock.Controller) core.Correlator
		wantErr      bool
		wantSubs     []string
		wantFailKind string
	}{
		{
			name:    "drive-phase budget timeout names target and phase",
			command: []string{"sleep", "1"},
			budget:  config.RunBudget{Timeout: 50 * time.Millisecond, KillGrace: 100 * time.Millisecond},
			setupCor: func(ctrl *gomock.Controller) core.Correlator {
				mc := mocks.NewMockCorrelator(ctrl)
				mc.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1")
				// Resolve must NOT be called — the drive phase times out first.
				return mc
			},
			wantErr:      true,
			wantSubs:     []string{"run timeout", "phase: drive", "sut", "50ms"},
			wantFailKind: core.FailureKindDriver,
		},
		{
			name:    "resolve-phase budget timeout attributes the resolve phase",
			command: []string{"sh", "-c", "echo hi"},
			budget:  config.RunBudget{Timeout: 80 * time.Millisecond, KillGrace: 100 * time.Millisecond},
			setupCor: func(ctrl *gomock.Controller) core.Correlator {
				mc := mocks.NewMockCorrelator(ctrl)
				mc.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1")
				mc.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, _ core.TraceStore, _ string) (*trace.Trace, error) {
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-time.After(2 * time.Second):
							return tr, nil
						}
					})
				return mc
			},
			wantErr:      true,
			wantSubs:     []string{"run timeout", "phase: resolve", "80ms"},
			wantFailKind: core.FailureKindResolve,
		},
		{
			name:    "unbounded budget does not apply a timeout",
			command: []string{"sh", "-c", "echo hi"},
			budget:  config.RunBudget{Unbounded: true, KillGrace: 100 * time.Millisecond},
			setupCor: func(ctrl *gomock.Controller) core.Correlator {
				mc := mocks.NewMockCorrelator(ctrl)
				mc.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1")
				mc.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(tr, nil)
				return mc
			},
			wantErr: false,
		},
		{
			name:    "within a generous budget the run succeeds",
			command: []string{"sh", "-c", "echo hi"},
			budget:  config.RunBudget{Timeout: 5 * time.Second, KillGrace: 100 * time.Millisecond},
			setupCor: func(ctrl *gomock.Controller) core.Correlator {
				mc := mocks.NewMockCorrelator(ctrl)
				mc.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1")
				mc.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(tr, nil)
				return mc
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				OTLPEndpoint: "http://localhost:4318",
				Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
				Targets: map[string]config.Target{
					"sut": {Adapter: "shell", Command: tt.command, MaxConcurrency: 1, Budget: tt.budget},
				},
			}
			ctrl := gomock.NewController(t)
			st := mocks.NewMockTraceStore(ctrl)
			cor := tt.setupCor(ctrl)
			eng, err := Build(cfg, st, cor)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			ev, err := eng.driveOnce(context.Background(), "sut", nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (ev=%+v)", ev)
				}
				for _, sub := range tt.wantSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				if tt.wantFailKind != "" && ev.FailureKind != tt.wantFailKind {
					t.Fatalf("FailureKind = %q, want %q", ev.FailureKind, tt.wantFailKind)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.Failed {
				t.Fatalf("unexpected failed evidence: %+v", ev)
			}
		})
	}
}

// countingDriver counts Run invocations and always errors. Paired with a correlator
// that injects an empty run id, its error is a structural (empty-RunID) failure that
// aborts a DriveN batch — letting the test assert how many iterations actually drove.
type countingDriver struct{ n atomic.Int64 }

func (d *countingDriver) Run(_ context.Context, _ core.RunSpec) (core.RunResult, error) {
	d.n.Add(1)
	return core.RunResult{}, errors.New("structural boom")
}

// TestDriveNParallelStructuralErrorCancelsSiblings pins feature 003 (US4/FR-008):
// a structural failure in a parallel @runs(N) batch cancels iterations that have not
// started driving, so strictly fewer than N iterations drive the SUT.
func TestDriveNParallelStructuralErrorCancelsSiblings(t *testing.T) {
	const n = 8
	drv := &countingDriver{}
	registry.ResetForTest(t) // reopen the (possibly sealed) registry to register a custom seam
	registry.RegisterDriver("counterr", drv)

	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			// max_concurrency 1 serializes drives, so once the first structural error
			// cancels the batch the queued iterations must skip driving.
			"svc": {Adapter: "counterr", Command: []string{"x"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("").AnyTimes() // empty runID → structural
	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := eng.DriveN(context.Background(), "svc", nil, n, true); err == nil {
		t.Fatal("expected the structural error to abort the batch")
	}
	if got := drv.n.Load(); got >= n {
		t.Fatalf("drove %d of %d iterations; a structural error must cancel not-yet-started iterations", got, n)
	}
}

// cancelOnFirstDriveDriver cancels the parent context on its FIRST Run and returns
// success. The sync.Once guard means only the first drive in a parallel batch
// cancels; paired with a correlator that injects a NON-empty run id, its success is
// not a structural error. A mid-batch cancellation with no structural error can then
// surface only via DriveN's post-Wait parent-context check.
type cancelOnFirstDriveDriver struct {
	once   sync.Once
	cancel context.CancelFunc
}

func (d *cancelOnFirstDriveDriver) Run(_ context.Context, _ core.RunSpec) (core.RunResult, error) {
	d.once.Do(d.cancel)
	return core.RunResult{Output: core.Output{Stdout: "ok"}}, nil
}

// TestDriveNParallelCancelledMidBatch pins CLAUDE.md invariant #4 (no silent
// fallbacks) for the parallel path: when the parent context is cancelled mid-batch
// (after the pre-check passes, while goroutines run) with NO structural error,
// un-started iterations leave zero-value Evidence. DriveN must NOT return
// (evs, nil) — it must surface the cancellation the same way the serial path does.
//
// Deterministic by MaxConcurrency:1 — exactly one goroutine acquires the semaphore
// and drives first; it cancels the parent (propagating to batchCtx) before any
// sibling can acquire the slot, so siblings either early-return at the batchCtx guard
// (zero-value sample) or bail at the semaphore select (failed sample). In every
// interleaving structErr stays nil, so only the post-Wait ctx.Err() check can catch it.
func TestDriveNParallelCancelledMidBatch(t *testing.T) {
	const n = 8
	drv := &cancelOnFirstDriveDriver{}
	registry.ResetForTest(t) // reopen the sealed registry to register a custom seam
	registry.RegisterDriver("cancelfirst", drv)

	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			// max_concurrency 1 serializes drives, so the first drive cancels the
			// parent before any sibling can acquire the slot.
			"svc": {Adapter: "cancelfirst", Command: []string{"x"}, MaxConcurrency: 1},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	cor := mocks.NewMockCorrelator(ctrl)
	// Non-empty run id → a drive failure is a typed sample, never structural. Resolve
	// returns a valid trace regardless of ctx state (the first drive resolves under the
	// now-cancelled ctx, and the mock must not care).
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-1").AnyTimes()
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	drv.cancel = cancel

	_, err = eng.DriveN(ctx, "svc", nil, n, true)
	if err == nil {
		t.Fatal("mid-batch cancellation with no structural error must surface as an error, got nil (silent partial success)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled in chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), `DriveN "svc" cancelled`) {
		t.Fatalf("error %q does not contain %q", err.Error(), `DriveN "svc" cancelled`)
	}
}

// instantDriver succeeds immediately — drive time ≈ 0, isolating resolve time so
// batch wall-clock measures only how resolution waits compose.
type instantDriver struct{}

func (instantDriver) Run(context.Context, core.RunSpec) (core.RunResult, error) {
	return core.RunResult{Output: core.Output{Answer: "ok"}}, nil
}

// TestDriveNParallelResolveOverlapsOutsideSlot pins feature 004 US1/FR-001: the
// per-target concurrency slot bounds SUT execution ONLY. With limit 1 and 10
// parallel runs whose resolution lags ~300ms each, the ingestion waits must
// overlap: batch wall time stays under 4× lag (generous, CI-safe bound). Holding
// the slot through cor.Resolve serializes the waits (~10× lag) and fails this.
//
// No t.Parallel: the assertion is wall-clock-bound and Build mutates the global
// registry; background load would turn a real regression signal into flake noise.
func TestDriveNParallelResolveOverlapsOutsideSlot(t *testing.T) {
	const (
		n   = 10
		lag = 300 * time.Millisecond
	)
	registry.ResetForTest(t) // reopen the sealed registry to register a custom seam
	registry.RegisterDriver("instant-overlap", instantDriver{})

	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"sut": {Adapter: "instant-overlap", Command: []string{"x"}, MaxConcurrency: 1},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-ov").AnyTimes()
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ core.TraceStore, _ string) (*trace.Trace, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(lag):
				return tr, nil
			}
		}).Times(n)

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	start := time.Now()
	evs, err := eng.DriveN(context.Background(), "sut", nil, n, true)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("DriveN: %v", err)
	}
	if len(evs) != n {
		t.Fatalf("got %d samples, want %d", len(evs), n)
	}
	for i, ev := range evs {
		if ev.Failed {
			t.Fatalf("sample %d unexpectedly failed: %+v", i, ev)
		}
	}
	if limit := 4 * lag; elapsed >= limit {
		t.Fatalf("parallel batch took %v, want < %v: resolution waits are serialized behind the SUT-execution slot (FR-001)", elapsed, limit)
	}
}

// recordingDriver records each Run's [start, end] interval (thread-safe) and holds
// the SUT slot briefly so overlapping drives are detectable.
type recordingDriver struct {
	mu        sync.Mutex
	hold      time.Duration
	intervals [][2]time.Time
}

func (d *recordingDriver) Run(context.Context, core.RunSpec) (core.RunResult, error) {
	start := time.Now()
	time.Sleep(d.hold)
	end := time.Now()
	d.mu.Lock()
	d.intervals = append(d.intervals, [2]time.Time{start, end})
	d.mu.Unlock()
	return core.RunResult{Output: core.Output{Answer: "ok"}}, nil
}

// TestDriveNParallelLimitOneKeepsDrivesSerialized guards the other half of
// FR-001: narrowing the slot's scope must NOT weaken it — with limit 1 the
// semaphore still gates SUT execution, so no two drives may overlap even while
// their resolutions do. This is a preservation guard: it must pass before AND
// after the slot-scope change.
func TestDriveNParallelLimitOneKeepsDrivesSerialized(t *testing.T) {
	const n = 6
	drv := &recordingDriver{hold: 30 * time.Millisecond}
	registry.ResetForTest(t) // reopen the sealed registry to register a custom seam
	registry.RegisterDriver("recording-serial", drv)

	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"sut": {Adapter: "recording-serial", Command: []string{"x"}, MaxConcurrency: 1},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-ser").AnyTimes()
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).Return(tr, nil).Times(n)

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "sut", nil, n, true)
	if err != nil {
		t.Fatalf("DriveN: %v", err)
	}
	if len(evs) != n {
		t.Fatalf("got %d samples, want %d", len(evs), n)
	}

	drv.mu.Lock()
	intervals := append([][2]time.Time{}, drv.intervals...)
	drv.mu.Unlock()
	if len(intervals) != n {
		t.Fatalf("driver ran %d times, want %d", len(intervals), n)
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0].Before(intervals[j][0]) })
	for i := 1; i < len(intervals); i++ {
		if intervals[i][0].Before(intervals[i-1][1]) {
			t.Fatalf("drives %d and %d overlap (%v starts before %v ends): limit=1 must serialize SUT execution",
				i-1, i, intervals[i][0], intervals[i-1][1])
		}
	}
}

// TestDriveNParallelResolveConcurrencyCappedAtEight pins feature 004's internal
// resolve bound (research R2): releasing the SUT slot before resolution must not
// allow unbounded concurrent resolutions — at most 8 run at once per engine
// (store protection), yet resolution still overlaps (>1 in flight), never
// re-serializing the batch. With 16 slots and 16 parallel runs the current
// slot-held-through-resolve code drives all 16 resolves concurrently (>8): red.
func TestDriveNParallelResolveConcurrencyCappedAtEight(t *testing.T) {
	const n = 16
	registry.ResetForTest(t) // reopen the sealed registry to register a custom seam
	registry.RegisterDriver("instant-bound", instantDriver{})

	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets: map[string]config.Target{
			"sut": {Adapter: "instant-bound", Command: []string{"x"}, MaxConcurrency: n},
		},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	cor.EXPECT().Inject(gomock.Any(), gomock.Any()).Return("run-bound").AnyTimes()

	// In-flight high-water mark over concurrent Resolve calls. The 250ms hold keeps
	// every resolve in flight long enough that unbounded concurrency reliably
	// exceeds the cap (generous, CI-safe).
	var mu sync.Mutex
	inFlight, highWater := 0, 0
	cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ core.TraceStore, _ string) (*trace.Trace, error) {
			mu.Lock()
			inFlight++
			if inFlight > highWater {
				highWater = inFlight
			}
			mu.Unlock()
			defer func() { mu.Lock(); inFlight--; mu.Unlock() }()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
				return tr, nil
			}
		}).Times(n)

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	evs, err := eng.DriveN(context.Background(), "sut", nil, n, true)
	if err != nil {
		t.Fatalf("DriveN: %v", err)
	}
	if len(evs) != n {
		t.Fatalf("got %d samples, want %d", len(evs), n)
	}

	mu.Lock()
	got := highWater
	mu.Unlock()
	if got > 8 {
		t.Fatalf("max concurrent resolves = %d, want <= 8 (engine resolve bound, store protection)", got)
	}
	if got < 2 {
		t.Fatalf("max concurrent resolves = %d, want >= 2: the bound must not re-serialize resolution", got)
	}
}

// TestDriveResolveSlotWaitCancelledReturnsWrappedError pins the error contract of
// withResolveSlot's ctx.Done() branch: waiting for a resolve slot under a cancelled
// context must return a wrapped error naming the failing operation, not a bare
// ctx.Err() (repo convention: errors wrapped with %w naming the concrete thing).
// The semaphore is saturated first — all maxConcurrentResolves slots held by
// blocking resolves — so the select's send case can never fire and the ctx.Done()
// branch is hit deterministically.
func TestDriveResolveSlotWaitCancelledReturnsWrappedError(t *testing.T) {
	cfg := config.Config{
		OTLPEndpoint: "http://localhost:4318",
		Poll:         config.PollSpec{Interval: "1ms", StableFor: 1, Timeout: "1s"},
		Targets:      map[string]config.Target{},
	}
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}

	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)

	// Each in-flight resolve signals started, then blocks until release closes —
	// holding its resolve slot so the semaphore stays full for the 9th call.
	started := make(chan struct{}, maxConcurrentResolves)
	release := make(chan struct{})
	cor.EXPECT().ResolveComplete(gomock.Any(), gomock.Any(), "pinned-full").DoAndReturn(
		func(ctx context.Context, _ core.TraceStore, _ string) (*trace.Trace, error) {
			started <- struct{}{}
			<-release
			return tr, nil
		}).Times(maxConcurrentResolves)

	eng, err := Build(cfg, st, cor)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	eng.PinRun("pinned-full") // Drive resolves the pinned run: no Inject, no SUT

	var wg sync.WaitGroup
	for i := 0; i < maxConcurrentResolves; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := eng.Drive(context.Background(), "unused", nil); err != nil {
				t.Errorf("saturating Drive: %v", err)
			}
		}()
	}
	// Wait until all slots are provably held (each resolve signalled started).
	for i := 0; i < maxConcurrentResolves; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for resolves to saturate the semaphore")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: with the semaphore full, only ctx.Done() can fire
	_, err = eng.Drive(ctx, "unused", nil)

	close(release) // unblock and drain the saturating resolves before asserting
	wg.Wait()

	if err == nil {
		t.Fatal("expected error from Drive with cancelled context and full semaphore, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is(err, context.Canceled) = false; err = %v", err)
	}
	if want := "wait for trace-resolution slot"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}
