package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Feature 003 (US2) signal test: interrupting `mentat run` must cancel the suite,
// still write every configured report with the interrupted marker, and exit 130; a
// second signal must force-quit. Hermetic — the SUT is a local sleep bounded by
// run_timeout, so no Tempo is contacted (drive times out before resolve).

var mentatBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mentat-signal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "signal test: temp dir: %v\n", err)
		os.Exit(1)
	}
	mentatBin = filepath.Join(dir, "mentat")
	build := exec.Command("go", "build", "-o", mentatBin, ".")
	if out, berr := build.CombinedOutput(); berr != nil {
		fmt.Fprintf(os.Stderr, "signal test: build: %v\n%s", berr, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// writeSignalFixture writes a temp working dir with a mentat.yaml whose only target
// ("slow") touches a ready file then sleeps far past the scenario, and a
// one-scenario feature driving it. runTimeout/killGrace set the lifecycle budget.
// When trapTerm is true the SUT ignores SIGTERM, so cancelling the drive takes the
// full kill grace — that widens the shutdown window enough to observe a second
// signal force-quit. The ready path doubles as a unique pgrep marker for cleanup.
func writeSignalFixture(t *testing.T, runTimeout, killGrace string, trapTerm bool) (workdir, readyPath string) {
	t.Helper()
	workdir = t.TempDir()
	readyPath = filepath.Join(workdir, "ready")
	body := fmt.Sprintf("touch %s; sleep 120", readyPath)
	if trapTerm {
		body = fmt.Sprintf("trap '' TERM INT; touch %s; sleep 120", readyPath)
	}
	cfg := fmt.Sprintf(`tempo: { endpoint: "http://127.0.0.1:1" }
otlpEndpoint: "http://127.0.0.1:1"
poll: { interval: "50ms", stableFor: 1, timeout: "1s" }
run_timeout: %s
kill_grace: %s
targets:
  slow:
    adapter: shell
    command: ["sh", "-c", "%s"]
`, runTimeout, killGrace, body)
	if err := os.WriteFile(filepath.Join(workdir, "mentat.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	feature := `Feature: signal
  Scenario: slow scenario
    Given the agent target "slow"
    When I run the agent with prompt "go"
    Then the result contains "x"
`
	if err := os.WriteFile(filepath.Join(workdir, "slow.feature"), []byte(feature), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	// Best-effort reap of any SUT tree a force-quit leaves behind.
	t.Cleanup(func() { killMarker(readyPath) })
	return workdir, readyPath
}

// killMarker SIGKILLs any process (and its group) whose command line matches marker.
func killMarker(marker string) {
	out, _ := exec.Command("pgrep", "-f", marker).Output()
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %q never appeared (SUT never started)", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("mentat did not exit within %v after the signal", timeout)
		return nil
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%q missing %q:\n%s", path, want, data)
	}
}

func TestSignalInterruptEmitsReportsAndExits130(t *testing.T) {
	workdir, readyPath := writeSignalFixture(t, "2s", "10s", false)
	jsonPath := filepath.Join(workdir, "r.json")
	junitPath := filepath.Join(workdir, "r.xml")

	cmd := exec.Command(mentatBin, "run",
		"--report-json", jsonPath, "--junit", junitPath, "slow.feature")
	cmd.Dir = workdir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mentat: %v", err)
	}

	waitForFile(t, readyPath, 10*time.Second) // the SUT is being driven
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	err := waitExit(t, cmd, 15*time.Second)
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected a non-zero exit, got %v", err)
	}
	if ee.ExitCode() != 130 {
		t.Fatalf("exit code = %d, want 130 (SIGTERM interrupt)", ee.ExitCode())
	}
	// Every configured report is still written, with the interrupted marker.
	assertFileContains(t, jsonPath, `"interrupted": true`)
	assertFileContains(t, junitPath, `name="interrupted" value="true"`)
}

func TestSignalSecondSignalForceQuits(t *testing.T) {
	// The SUT traps SIGTERM, so after the first signal the driver must wait the full
	// kill grace (5s) before SIGKILL — a graceful exit is therefore slow. That window
	// makes the SECOND signal's force-quit observable: the process exits fast and
	// signal-terminated, not via the graceful exit-130 path.
	workdir, readyPath := writeSignalFixture(t, "30s", "5s", true)
	cmd := exec.Command(mentatBin, "run", "slow.feature")
	cmd.Dir = workdir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mentat: %v", err)
	}
	waitForFile(t, readyPath, 10*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("first SIGTERM: %v", err)
	}
	time.Sleep(400 * time.Millisecond) // let the first signal cancel + restore default
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("second SIGTERM: %v", err)
	}

	start := time.Now()
	err := waitExit(t, cmd, 10*time.Second)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("second signal did not force-quit; wait took %v", elapsed)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected an ExitError from a signal-terminated process, got %v", err)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		t.Fatalf("expected the process to be signal-terminated by the second SIGTERM, got exit code %d", ee.ExitCode())
	}
}
