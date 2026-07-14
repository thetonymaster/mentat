package ctl

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/trace"
	"go.uber.org/mock/gomock"
)

func TestSaveAndReadLastRoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // LastPath lives under $HOME/.mentat/last
	if err := SaveLast("run-123"); err != nil {
		t.Fatalf("SaveLast: %v", err)
	}
	got, err := ReadLast()
	if err != nil {
		t.Fatalf("ReadLast: %v", err)
	}
	if got != "run-123" {
		t.Fatalf("ReadLast = %q, want run-123", got)
	}
}

func TestReadLastErrorsWhenAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := ReadLast(); err == nil {
		t.Fatal("expected error when no last run recorded")
	}
}

func TestLastPathUnderHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/example")
	if LastPath() != "/tmp/example/.mentat/last" {
		t.Fatalf("LastPath = %q", LastPath())
	}
}

func TestSaveLastMkdirFails(t *testing.T) {
	// Create a regular file where the .mentat directory needs to go, so MkdirAll fails.
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Write a plain file at $HOME/.mentat so MkdirAll cannot create the directory.
	if err := os.WriteFile(home+"/.mentat", []byte("blocker"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := SaveLast("run-x"); err == nil {
		t.Fatal("expected error when mkdir is blocked")
	}
}

// TestResolve pins the FR-004 routing (feature 004, US3): ctl.Resolve is the
// shared HISTORICAL resolve helper (trace/tools/services/diff all route through
// it for saved run ids), so it must use the correlator's known-complete mode —
// ResolveComplete, one fetch pass, no stability polling. The explicit
// Resolve(...).Times(0) forbids the live stability-gated path.
func TestResolve(t *testing.T) {
	wantTrace := &trace.Trace{RunID: "trace-abc"}
	wantErr := errors.New("not found")

	tests := []struct {
		name      string
		runID     string
		retTrace  *trace.Trace
		retErr    error
		wantTrace *trace.Trace
		wantErr   bool
	}{
		{
			name:      "happy path returns trace",
			runID:     "run-abc",
			retTrace:  wantTrace,
			retErr:    nil,
			wantTrace: wantTrace,
			wantErr:   false,
		},
		{
			name:      "correlator error propagates",
			runID:     "run-missing",
			retTrace:  nil,
			retErr:    wantErr,
			wantTrace: nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCor := mocks.NewMockCorrelator(ctrl)
			mockSt := mocks.NewMockTraceStore(ctrl)

			mockCor.EXPECT().
				ResolveComplete(gomock.Any(), mockSt, tt.runID).
				Return(tt.retTrace, tt.retErr)
			// Historical resolution must never pay the live stability poll (FR-004).
			mockCor.EXPECT().
				Resolve(gomock.Any(), gomock.Any(), gomock.Any()).
				Times(0)

			got, err := Resolve(context.Background(), mockCor, mockSt, tt.runID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantTrace {
				t.Fatalf("Resolve() = %v, want %v", got, tt.wantTrace)
			}
		})
	}
}
