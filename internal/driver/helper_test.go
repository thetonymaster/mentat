package driver

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// Helper-process scaffolding (T002) — the Go stdlib re-exec pattern used across
// the standard library (os/exec's own tests). A driver test that needs a
// controllable subprocess re-execs THIS test binary with
// `-test.run=^TestHelperProcess$` and GO_WANT_HELPER_PROCESS=1 in the child env;
// the child lands in TestHelperProcess below and behaves per HELPER_MODE. Because
// each mode ends by blocking forever or calling os.Exit, the Go test framework
// never prints its summary line, so the parent captures only what the mode wrote.
//
// This file is infrastructure only: it asserts no driver behaviour. The lifecycle
// driver tests (T006) build a core.RunSpec that targets these modes and drive it
// through NewShell().
//
// Metadata (PIDs) is written to STDERR so a mode's STDOUT stays a clean payload —
// the grandchild-holds-pipe mode relies on that: its stdout carries the preserved
// "answer" and is held open by a descendant, while its stderr carries the
// grandchild PID and closes when the direct child exits.
const (
	helperEnvGate = "GO_WANT_HELPER_PROCESS"
	helperEnvMode = "HELPER_MODE"
	// helperEnvPIDFile, when set, names a file the subprocess writes its PIDs to
	// (self first, then any child). A test uses it to learn the process group even
	// on a killed/timed-out run, where the driver returns no captured output.
	helperEnvPIDFile = "HELPER_PID_FILE"
)

// Helper mode names. Selected by the HELPER_MODE env var in the child.
const (
	// helperNeverExits blocks forever after spawning one child that also sleeps.
	// Both processes share the process group the driver puts the direct child in,
	// so a process-group kill must reap the whole tree. The child+self PIDs are
	// printed to stderr so a test can assert neither survives.
	helperNeverExits = "never-exits"
	// helperGrandchildPipe writes an "answer" to stdout, spawns a grandchild that
	// inherits (and holds open) that stdout pipe, then exits 0. Without a bounded
	// WaitDelay the runner would block on the held pipe forever; with it, Wait
	// returns after the delay with the already-captured "answer" preserved. The
	// grandchild PID is printed to stderr so a test can reap it.
	helperGrandchildPipe = "grandchild-holds-pipe"
	// helperIgnoresSIGTERM installs a SIGTERM handler that swallows the signal,
	// then blocks forever. Only an (uncatchable) SIGKILL escalation stops it.
	helperIgnoresSIGTERM = "ignores-sigterm"
	// helperLeafSleep is the plain child/grandchild: it sleeps a bounded time then
	// exits. Bounded so a broken kill path self-cleans instead of leaking forever.
	helperLeafSleep = "leaf-sleep"
)

// leafSleep is how long a leaf child/grandchild sleeps. It must far outlast any
// test's kill-grace (so the process is still alive when a group kill fires, and
// still holds the pipe past a WaitDelay) yet stay bounded so a broken lifecycle
// path does not leak a process indefinitely.
const leafSleep = 30 * time.Second

// TestHelperProcess is not a real test: when GO_WANT_HELPER_PROCESS=1 it acts as
// the re-exec'd subprocess for the mode in HELPER_MODE. When the gate is unset
// (an ordinary `go test` run) it returns immediately.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvGate) != "1" {
		return
	}
	// From here on this process IS the helper subprocess. It never returns to the
	// test framework — every branch blocks forever (killed by the driver) or exits.
	switch mode := os.Getenv(helperEnvMode); mode {
	case helperLeafSleep:
		time.Sleep(leafSleep)
		os.Exit(0)

	case helperNeverExits:
		child := spawnHelperChild(helperLeafSleep, nil)
		recordPIDs(os.Getpid(), child.Process.Pid)
		fmt.Fprintf(os.Stderr, "self-pid=%d child-pid=%d\n", os.Getpid(), child.Process.Pid)
		blockUntilKilled() // the driver must kill the whole process group

	case helperGrandchildPipe:
		// A grandchild inherits (and holds open) our stdout pipe, then we exit 0.
		gc := spawnHelperChild(helperLeafSleep, os.Stdout)
		recordPIDs(gc.Process.Pid)
		fmt.Fprintf(os.Stderr, "grandchild-pid=%d\n", gc.Process.Pid)
		_, _ = fmt.Fprintln(os.Stdout, "answer") // captured payload; must survive finalization
		os.Exit(0)

	case helperIgnoresSIGTERM:
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		go func() {
			for range ch { // swallow every SIGTERM; only SIGKILL can stop us
			}
		}()
		recordPIDs(os.Getpid())
		fmt.Fprintf(os.Stderr, "self-pid=%d\n", os.Getpid())
		blockUntilKilled() // the driver must escalate to SIGKILL

	default:
		fmt.Fprintf(os.Stderr, "helper: unknown HELPER_MODE %q\n", mode)
		os.Exit(2)
	}
}

// blockUntilKilled parks the helper effectively forever, waiting for the driver to
// terminate it. A long time.Sleep is used deliberately rather than select{}: an
// empty select trips Go's deadlock detector once this is the only live goroutine,
// crashing the helper (exit 2) before the driver can exercise its kill path. A
// pending timer keeps the runtime from declaring deadlock; the driver kills the
// process long before the hour elapses.
func blockUntilKilled() {
	time.Sleep(time.Hour)
	os.Exit(0)
}

// recordPIDs writes the given PIDs (space-separated, self first) to the file named
// by HELPER_PID_FILE, when set. It lets a parent test learn the subprocess and its
// process group even on a killed/timed-out run where no output is captured.
func recordPIDs(pids ...int) {
	path := os.Getenv(helperEnvPIDFile)
	if path == "" {
		return
	}
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	data := []byte(strings.Join(parts, " "))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "helper: write pid file: %v\n", err)
		os.Exit(1)
	}
}

// helperSpec builds a RunSpec that re-execs THIS test binary in the given helper
// mode, carrying the kill grace and (optional) PID-file path. Driver tests drive it
// through NewShell() to exercise the process-group/WaitDelay lifecycle paths (T006).
func helperSpec(mode string, grace time.Duration, pidFile string) core.RunSpec {
	env := map[string]string{helperEnvGate: "1", helperEnvMode: mode}
	if pidFile != "" {
		env[helperEnvPIDFile] = pidFile
	}
	return core.RunSpec{
		Target:    "helper-" + mode,
		Command:   []string{os.Args[0], "-test.run=^TestHelperProcess$"},
		Env:       env,
		RunID:     "helper-" + mode,
		KillGrace: grace,
	}
}

// spawnHelperChild starts another copy of this test binary in the given mode. When
// stdout is non-nil the child inherits it (used so a grandchild holds the parent's
// stdout pipe open). The child inherits the caller's process group by default, so a
// process-group kill of the caller reaps it too.
func spawnHelperChild(mode string, stdout *os.File) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnvGate+"=1", helperEnvMode+"="+mode)
	cmd.Stdout = stdout // nil → discarded; os.Stdout → inherit and hold the pipe
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "helper: start %s child: %v\n", mode, err)
		os.Exit(1)
	}
	return cmd
}
