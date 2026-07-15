package testio

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestCaptureStdioCapturesBothStreams proves the helper returns everything fn
// writes to BOTH real streams (stdout and stderr are redirected to one pipe).
func TestCaptureStdioCapturesBothStreams(t *testing.T) {
	got := CaptureStdio(t, func() {
		if _, err := fmt.Fprint(os.Stdout, "out-line "); err != nil {
			t.Errorf("write to redirected stdout: %v", err)
		}
		if _, err := fmt.Fprint(os.Stderr, "err-line"); err != nil {
			t.Errorf("write to redirected stderr: %v", err)
		}
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

// TestCaptureStdioRestoresAfterPanic covers CaptureStdio's own panic path: when fn
// panics, the deferred cleanup must still restore os.Stdout/os.Stderr so later
// tests do not write into a closed pipe.
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

// TestCaptureStdioReaderCompletesAfterPanicCleanup pins the goroutine-leak fix with
// a DIRECT completion signal instead of a process-wide goroutine count: once
// cleanup closes the writer on the panic path, the reader must reach EOF and send
// its result. If the writer-close were dropped, io.Copy would block forever and
// this bounded receive would time out.
func TestCaptureStdioReaderCompletesAfterPanicCleanup(t *testing.T) {
	result, cleanup := captureStdioAsync(t)
	func() {
		defer func() { _ = recover() }()
		defer cleanup() // models CaptureStdio's deferred cleanup during panic unwind
		panic("boom")
	}()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("reader reported a drain error: %v", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader goroutine did not complete after panic-path cleanup")
	}
}
