// Package testio provides shared test helpers for capturing process-level
// stdout/stderr. It imports testing and is intended to be used only from _test.go
// files (engine, driver, correlate); production code never imports it.
package testio

import (
	"bytes"
	"io"
	"os"
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
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = w, w
	defer func() {
		// Close w here too so the reader goroutine reaches EOF and exits even when
		// fn panics or calls runtime.Goexit (t.Fatalf) — the normal path already
		// closed it below, so this is a harmless double close (error discarded).
		_ = w.Close()
		os.Stdout, os.Stderr = origOut, origErr
	}()
	done := make(chan string, 1) // buffered: the reader never blocks on send, so it
	go func() {                  // exits cleanly even on the panic path (no receiver)
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close() // normal path: unblock the reader so <-done can receive
	return <-done
}
