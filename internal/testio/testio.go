// Package testio provides shared test helpers for capturing process-level
// stdout/stderr. It imports testing and is intended to be used only from _test.go
// files (engine, driver, correlate); production code never imports it.
package testio

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
)

// captureResult carries the drained output plus any failure the reader hit, so
// CaptureStdio can surface a copy/close error instead of returning partial output
// as success (no silent fallbacks).
type captureResult struct {
	out string
	err error
}

// CaptureStdio redirects the process's real os.Stdout and os.Stderr to a pipe for
// the duration of fn and returns everything written to them. Restoration is
// deferred, so a panic or runtime.Goexit (e.g. a t.Fatalf inside fn) cannot leave
// the real streams pointed at a closed pipe and silently swallow the output of
// every later test in the binary. A drain error (io.Copy / reader Close) fails the
// test via t.Fatalf rather than being reported as a clean, but truncated, capture.
//
// It underpins the SC-005 silent-default contract across the seams: with no
// injected logger a drive/resolve path must reach neither real stream, so a stray
// fmt.Print regression surfaces here even though the discard slog handler swallows
// logger records. A clean run — including a SUT subprocess that writes into its
// own cmd buffers — captures nothing.
func CaptureStdio(t *testing.T, fn func()) string {
	t.Helper()
	result, cleanup := captureStdioAsync(t)
	// Panic/Goexit safety: still closes the writer + restores streams. The
	// close error is moot during a panic unwind (no return value to report),
	// so it is deliberately discarded here and surfaced only on the normal path.
	defer func() { _ = cleanup() }()
	fn()
	closeErr := cleanup() // normal path: close the writer so the reader reaches EOF and result can arrive
	got := <-result
	// Join the writer-close error with the reader's drain error so a failed pipe
	// finalization can't masquerade as a clean, but truncated, capture.
	if err := errors.Join(closeErr, got.err); err != nil {
		t.Fatalf("CaptureStdio: draining captured output: %v", err)
	}
	return got.out
}

// captureStdioAsync starts a capture: it redirects os.Stdout/os.Stderr into a pipe,
// spawns a reader goroutine, and returns a buffered channel that receives the
// captured bytes (and any drain error) once the reader drains the pipe, plus an
// idempotent cleanup that closes the writer (unblocking the reader), restores the
// real streams, and returns the writer-close error so the caller can surface it.
//
// Callers MUST defer cleanup so the streams are restored — and the reader
// unblocked — even if the code under capture panics or calls runtime.Goexit;
// receive from result only after cleanup has closed the writer. Split out from
// CaptureStdio so tests can observe reader-goroutine completion directly (a bounded
// receive on result) instead of sampling the flappy process-wide goroutine count.
func captureStdioAsync(t *testing.T) (result <-chan captureResult, cleanup func() error) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = w, w
	done := make(chan captureResult, 1) // buffered: the reader never blocks on send, so
	go func() {                         // it exits cleanly even with no receiver (panic path)
		var b bytes.Buffer
		_, copyErr := io.Copy(&b, r)
		// Close the read end once drained so pipe fds don't leak; join both failures
		// so a copy or close error reaches CaptureStdio rather than being discarded.
		done <- captureResult{out: b.String(), err: errors.Join(copyErr, r.Close())}
	}()
	var (
		once     sync.Once
		closeErr error
	)
	// Idempotent: the first call closes the writer (unblocking the reader) and
	// restores the streams; both this and any later call return the same stored
	// close error so CaptureStdio can surface it instead of swallowing it.
	cleanup = func() error {
		once.Do(func() {
			closeErr = w.Close()
			os.Stdout, os.Stderr = origOut, origErr
		})
		return closeErr
	}
	return done, cleanup
}
