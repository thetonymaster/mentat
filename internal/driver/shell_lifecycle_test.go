package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// Feature 003 (US1) driver lifecycle tests. They drive the TestHelperProcess
// re-exec modes (helper_test.go) through NewShell() to prove the shell driver
// bounds and reaps a SUT process tree: a hung tree is killed at the context
// deadline, a descendant holding the output pipe does not hang Wait, and a
// SIGTERM-ignoring SUT is escalated to SIGKILL.

type runOutcome struct {
	res     core.RunResult
	err     error
	elapsed time.Duration
}

// runWithTimeout runs the shell driver in a goroutine and returns its outcome, or
// reports timedOut=true if it does not return within hardBound. The bound is a test
// safety net: a lifecycle bug (unbounded wait) surfaces as a fast timedOut failure
// instead of hanging the suite until the go-test deadline.
func runWithTimeout(t *testing.T, ctx context.Context, spec core.RunSpec, hardBound time.Duration) (runOutcome, bool) {
	t.Helper()
	done := make(chan runOutcome, 1)
	start := time.Now()
	go func() {
		res, err := NewShell().Run(ctx, spec)
		done <- runOutcome{res: res, err: err, elapsed: time.Since(start)}
	}()
	select {
	case o := <-done:
		return o, false
	case <-time.After(hardBound):
		return runOutcome{}, true
	}
}

// processAlive reports whether pid still exists (signal 0 probes existence). A
// just-killed but not-yet-reaped zombie may briefly still report alive, so callers
// poll via eventually.
func processAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// groupAlive reports whether any process remains in the process group pgid.
func groupAlive(pgid int) bool {
	return !errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

// eventually polls cond until it is true or timeout elapses.
func eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitPIDFile polls until the helper's PID file is non-empty, then parses the
// space-separated PIDs (self first, then any child).
func waitPIDFile(t *testing.T, path string, timeout time.Duration) []int {
	t.Helper()
	var data []byte
	if !eventually(timeout, func() bool {
		b, err := os.ReadFile(path)
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			return false
		}
		data = b
		return true
	}) {
		t.Fatalf("pid file %q never became readable", path)
	}
	fields := strings.Fields(string(data))
	pids := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("parse pid %q from %q: %v", f, path, err)
		}
		pids[i] = n
	}
	return pids
}

func TestShellNeverExitsKillsWholeGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	grace := 400 * time.Millisecond
	ctxTimeout := 400 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	spec := helperSpec(helperNeverExits, grace, pidFile)
	o, timedOut := runWithTimeout(t, ctx, spec, ctxTimeout+grace+3*time.Second)
	if timedOut {
		t.Fatal("Run did not return: a never-exiting SUT tree was not bounded")
	}
	if o.err == nil {
		t.Fatal("expected a cancellation error for a never-exiting SUT, got nil")
	}

	pids := waitPIDFile(t, pidFile, 2*time.Second)
	if len(pids) < 2 {
		t.Fatalf("expected self+child PIDs, got %v", pids)
	}
	pgid, child := pids[0], pids[1]
	// SC-001 / FR-002: no member of the SUT process group survives the run.
	if !eventually(grace+3*time.Second, func() bool { return !groupAlive(pgid) }) {
		t.Fatalf("process group %d survived; a member outlived the run", pgid)
	}
	if !eventually(2*time.Second, func() bool { return !processAlive(child) }) {
		t.Fatalf("child %d survived the group kill", child)
	}
}

func TestShellGrandchildPipeReturnsWithinWaitDelay(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	grace := 300 * time.Millisecond

	spec := helperSpec(helperGrandchildPipe, grace, pidFile)
	// No context cancellation: the direct child exits 0 but a grandchild holds the
	// stdout pipe open. Without WaitDelay the runner blocks until the grandchild
	// exits (leafSleep, 30s); the hard bound is far under that.
	o, timedOut := runWithTimeout(t, context.Background(), spec, 8*time.Second)
	if timedOut {
		t.Fatal("Run blocked on a pipe held by a surviving descendant (missing WaitDelay)")
	}
	if o.err != nil {
		t.Fatalf("normal exit expected, got error: %v", o.err)
	}
	if o.elapsed >= leafSleep {
		t.Fatalf("Run took %v; it waited on the held pipe instead of finalizing at WaitDelay", o.elapsed)
	}
	// FR-003: captured output up to finalization is preserved.
	if got := o.res.Output.Answer; got != "answer" {
		t.Fatalf("captured Answer = %q, want %q (output not preserved through WaitDelay)", got, "answer")
	}
	// FR-002: even on a normal exit the whole tree is reaped.
	pids := waitPIDFile(t, pidFile, 2*time.Second)
	gc := pids[0]
	if !eventually(grace+3*time.Second, func() bool { return !processAlive(gc) }) {
		t.Fatalf("grandchild %d outlived the run", gc)
	}
}

func TestShellIgnoresSIGTERMEscalatesToKill(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	ctxTimeout := 300 * time.Millisecond
	grace := 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	spec := helperSpec(helperIgnoresSIGTERM, grace, pidFile)
	o, timedOut := runWithTimeout(t, ctx, spec, ctxTimeout+grace+3*time.Second)
	if timedOut {
		t.Fatal("Run did not return: a SIGTERM-ignoring SUT was never escalated to SIGKILL")
	}
	if o.err == nil {
		t.Fatal("expected a cancellation error, got nil")
	}
	// Polite-first: the SUT ignores SIGTERM, so the run must wait ~grace for the
	// forceful SIGKILL. An immediate-SIGKILL driver returns before this lower bound.
	if floor := ctxTimeout + grace/2; o.elapsed < floor {
		t.Fatalf("Run returned in %v; expected >= ~%v (SIGTERM must precede a graced SIGKILL)", o.elapsed, floor)
	}
	pids := waitPIDFile(t, pidFile, 2*time.Second)
	if !eventually(2*time.Second, func() bool { return !processAlive(pids[0]) }) {
		t.Fatalf("process %d survived; escalation to SIGKILL failed", pids[0])
	}
}
