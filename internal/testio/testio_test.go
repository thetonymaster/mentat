package testio

import (
	"fmt"
	"os"
	"testing"
)

// TestCaptureStdioCapturesBothStreams proves the helper returns everything fn
// writes to BOTH real streams (stdout and stderr are redirected to one pipe).
func TestCaptureStdioCapturesBothStreams(t *testing.T) {
	got := CaptureStdio(t, func() {
		fmt.Fprint(os.Stdout, "out-line ")
		fmt.Fprint(os.Stderr, "err-line")
	})
	if want := "out-line err-line"; got != want {
		t.Fatalf("CaptureStdio = %q, want %q", got, want)
	}
}

// TestCaptureStdioEmptyWhenSilent is the load-bearing SC-005 shape: a run that
// touches neither real stream captures the empty string.
func TestCaptureStdioEmptyWhenSilent(t *testing.T) {
	if got := CaptureStdio(t, func() {}); got != "" {
		t.Fatalf("CaptureStdio(silent) = %q, want empty", got)
	}
}

// TestCaptureStdioRestoresAfterPanic pins the reason this helper exists in one
// place: the deferred restore must return os.Stdout/os.Stderr to the real streams
// even when fn panics (or calls runtime.Goexit), so a failing test cannot leave
// every later test writing into a closed pipe. Recover the propagated panic and
// assert the originals are back.
func TestCaptureStdioRestoresAfterPanic(t *testing.T) {
	origOut, origErr := os.Stdout, os.Stderr
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected fn's panic to propagate out of CaptureStdio")
			}
		}()
		_ = CaptureStdio(t, func() { panic("boom") })
	}()
	if os.Stdout != origOut || os.Stderr != origErr {
		t.Fatal("CaptureStdio did not restore os.Stdout/os.Stderr after fn panicked")
	}
}
