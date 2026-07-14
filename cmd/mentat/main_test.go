package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/engine"
)

// TestNewLoggerLevels pins the verbosity->level mapping for the shared logger
// helper both binaries use (FR-001): no flags is a silent discard handler
// (SC-005, byte-identical happy path), -v emits Info but suppresses Debug, and
// -vv (Debug) emits both; when both flags are set, -vv (Debug) wins. Narration
// must go to the provided writer only.
func TestNewLoggerLevels(t *testing.T) {
	t.Parallel()
	const (
		infoProbe  = "probe-info-msg"
		debugProbe = "probe-debug-msg"
	)
	tests := []struct {
		name      string
		verbose   bool
		debug     bool
		wantInfo  bool
		wantDebug bool
		wantEmpty bool
	}{
		{name: "no flags is silent discard", verbose: false, debug: false, wantEmpty: true},
		{name: "-v emits info suppresses debug", verbose: true, debug: false, wantInfo: true, wantDebug: false},
		{name: "-vv emits info and debug", verbose: false, debug: true, wantInfo: true, wantDebug: true},
		{name: "both set debug wins", verbose: true, debug: true, wantInfo: true, wantDebug: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := engine.NewLogger(&buf, tt.verbose, tt.debug)
			logger.Info(infoProbe)
			logger.Debug(debugProbe)
			out := buf.String()
			if tt.wantEmpty {
				if len(out) != 0 {
					t.Fatalf("silent default wrote %d bytes, want 0: %q", len(out), out)
				}
				return
			}
			if got := strings.Contains(out, infoProbe); got != tt.wantInfo {
				t.Fatalf("info present=%v want %v (out=%q)", got, tt.wantInfo, out)
			}
			if got := strings.Contains(out, debugProbe); got != tt.wantDebug {
				t.Fatalf("debug present=%v want %v (out=%q)", got, tt.wantDebug, out)
			}
		})
	}
}
