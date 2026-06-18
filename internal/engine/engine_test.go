package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/correlate"
	"github.com/thetonymaster/mentat/internal/trace"
)

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
	st.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(tr, nil).AnyTimes()

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
			cor.EXPECT().Resolve(gomock.Any(), gomock.Any(), tt.runID).Return(tt.resolveRet, tt.resolveErr)

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
