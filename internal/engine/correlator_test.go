package engine

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/correlate"
)

// TestPollConfig pins the single source of truth for the poll defaults shared by
// both binaries (audit D6): an empty cfg.Poll resolves to the named default
// constants, overrides pass through verbatim, and a malformed duration is a
// returned, field-named error (library code never os.Exit, unlike the old
// main.go parseDur helper).
func TestPollConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		poll    config.PollSpec
		want    correlate.PollConfig
		wantErr string // substring; empty means no error
	}{
		{
			name: "empty uses named defaults",
			poll: config.PollSpec{},
			want: correlate.PollConfig{
				Interval:  defaultPollInterval,
				Timeout:   defaultPollTimeout,
				StableFor: defaultPollStableFor,
			},
		},
		{
			name: "defaults are the documented literals",
			poll: config.PollSpec{},
			want: correlate.PollConfig{
				Interval:  200 * time.Millisecond,
				Timeout:   30 * time.Second,
				StableFor: 3,
			},
		},
		{
			name: "overrides pass through",
			poll: config.PollSpec{Interval: "50ms", Timeout: "5s", StableFor: 7},
			want: correlate.PollConfig{
				Interval:  50 * time.Millisecond,
				Timeout:   5 * time.Second,
				StableFor: 7,
			},
		},
		{
			name:    "malformed interval names poll.interval and value",
			poll:    config.PollSpec{Interval: "notaduration"},
			wantErr: "poll.interval",
		},
		{
			name:    "malformed timeout names poll.timeout and value",
			poll:    config.PollSpec{Timeout: "notaduration"},
			wantErr: "poll.timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pollConfig(config.Config{Poll: tt.poll})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (got=%+v)", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q missing substring %q", err.Error(), tt.wantErr)
				}
				if !strings.Contains(err.Error(), "notaduration") {
					t.Fatalf("error %q should name the bad value %q", err.Error(), "notaduration")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("pollConfig = %+v; want %+v", got, tt.want)
			}
		})
	}
}

// TestParseDur pins the error-returning duration parser used by BuildCorrelator:
// empty is the default, valid parses, malformed is a wrapped error naming the bad
// value (never os.Exit).
func TestParseDur(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		s       string
		def     time.Duration
		want    time.Duration
		wantErr string
	}{
		{name: "empty uses default", s: "", def: 5 * time.Second, want: 5 * time.Second},
		{name: "parses valid duration", s: "200ms", def: time.Second, want: 200 * time.Millisecond},
		{name: "malformed is wrapped error", s: "nope", def: time.Second, wantErr: "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDur(tt.s, tt.def)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q missing substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseDur(%q,%v) = %v; want %v", tt.s, tt.def, got, tt.want)
			}
		})
	}
}

// TestBuildCorrelator asserts the shared composition root returns a usable
// core.Correlator for a valid config and a returned error (not a panic, not a
// zero-value success) for a malformed poll duration.
func TestBuildCorrelator(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	tests := []struct {
		name    string
		cfg     config.Config
		wantErr bool
	}{
		{
			name: "valid config returns correlator",
			cfg:  config.Config{Poll: config.PollSpec{Interval: "50ms", Timeout: "5s", StableFor: 7}},
		},
		{
			name: "empty poll uses defaults and returns correlator",
			cfg:  config.Config{},
		},
		{
			name:    "malformed poll duration returns error",
			cfg:     config.Config{Poll: config.PollSpec{Interval: "notaduration"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cor, err := BuildCorrelator(tt.cfg, logger)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (cor=%v)", cor)
				}
				if cor != nil {
					t.Fatalf("expected nil correlator on error, got %v", cor)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cor == nil {
				t.Fatalf("expected non-nil correlator")
			}
		})
	}
}
