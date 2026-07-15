// Package testio provides shared test helpers for capturing process-level
// stdout/stderr. It imports testing and is intended to be used only from _test.go
// files (engine, driver, correlate); production code never imports it.
package testio

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
)

// CaptureStdio redirects the process's real os.Stdout and os.Stderr to a pipe for
// the duration of fn and returns everything written to them. Restoration is
// deferred, so a panic or runtime.Goexit (e.g. a t.Fatalf inside fn) cannot leave
// the real streams pointed at a closed pipe and silently swallow the output of
// every later test in the binary.
//
// It underpins the SC-005 silent-default contract across the seams: with no
// injected logger a drive/resolve path must reach neither real stream, so a stray
// fmt.Print regression surfaces here even though the discard slog handler swallows
// logger records. A clean run — including a SUT subprocess that writes into its
// own cmd buffers — captures nothing.
func CaptureStdio(t *testing.T, fn func()) string {
	t.Helper()
	out, cleanup := captureStdioAsync(t)
	defer cleanup() // runs even if fn panics/Goexits: closes the writer + restores streams
	fn()
	cleanup() // normal path: close the writer so the reader reaches EOF and out can receive
	return <-out
}

// captureStdioAsync starts a capture: it redirects os.Stdout/os.Stderr into a pipe,
// spawns a reader goroutine, and returns a buffered channel that receives the
// captured bytes once the reader drains the pipe, plus an idempotent cleanup that
// closes the writer (unblocking the reader) and restores the real streams.
//
// Callers MUST defer cleanup so the streams are restored — and the reader
// unblocked — even if the code under capture panics or calls runtime.Goexit;
// receive from out only after cleanup has closed the writer. Split out from
// CaptureStdio so tests can observe reader-goroutine completion directly (a bounded
// receive on out) instead of sampling the flappy process-wide goroutine count.
func captureStdioAsync(t *testing.T) (out <-chan string, cleanup func()) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = w, w
	done := make(chan string, 1) // buffered: the reader never blocks on send, so it
	go func() {                  // exits cleanly even with no receiver (panic path)
		defer r.Close() // release the read end once drained so pipe fds don't leak
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	var once sync.Once
	cleanup = func() {
		once.Do(func() {
			_ = w.Close()
			os.Stdout, os.Stderr = origOut, origErr
		})
	}
	return done, cleanup
}
