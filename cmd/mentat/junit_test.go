package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Feature 006 (US3, E3) coexistence lock-in: `mentat run --junit <file>` must emit
// BOTH the godog `pretty` console (stdout) AND the collector-based JUnit file in ONE
// run — the JUnit flag must never silence the console. main.go already sets
// Format:"pretty" unconditionally and emits JUnit on a separate path (report.EmitReports,
// which carries the feature-003 interrupted marker), so these are GREEN-on-write
// CHARACTERIZATION tests: they do not drive a red→green cycle, they fence the fixed
// behaviour so a future switch to godog's native `pretty,junit:file` multi-format
// (which would regress the interrupted property) can't silently re-break E3.
//
// Hermetic like signal_test.go: the SUT is a local shell echo, and the trace store is
// an in-process httptest server mimicking Tempo's search+trace API — no Docker, no
// external network. The mock returns byte-identical responses every poll round so the
// correlator's stability gate resolves in two rounds.

// tempoOTLP is a minimal single-span OTLP trace (Tempo's /api/traces/{id} "batches"
// envelope). Static bytes → byte-identical across poll rounds → stable observation.
const tempoOTLP = `{"batches":[{"resource":{"attributes":[]},"scopeSpans":[{"spans":[` +
	`{"traceId":"aa","spanId":"bb","name":"root","startTimeUnixNano":"1",` +
	`"endTimeUnixNano":"2","attributes":[],"status":{"code":"STATUS_CODE_OK"}}]}]}]}`

// startTempoMock serves the two endpoints correlate.Resolve queries: /api/search
// returns one fixed trace ref, /api/traces/{id} returns tempoOTLP. Both responses are
// static, so every poll round observes identical bytes and resolution stabilises fast.
func startTempoMock(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/search"):
			_, _ = w.Write([]byte(`{"traces":[{"traceID":"trace-1"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/traces/"):
			_, _ = w.Write([]byte(tempoOTLP))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// writeJUnitFixture writes a workdir with a mentat.yaml pointing the tempo store at the
// mock URL and a shell target that echoes a known string, plus a one-scenario feature
// whose sole assertion reads the driver Output (no trace attribute needed). The scenario
// PASSES: the SUT exits 0, the mock resolves a non-empty trace, and "the result contains"
// matches the echoed stdout — so the run exits 0.
func writeJUnitFixture(t *testing.T, tempoURL string) string {
	t.Helper()
	workdir := t.TempDir()
	cfg := fmt.Sprintf(`tempo: { endpoint: %q }
poll: { interval: "20ms", stableFor: 1, timeout: "3s" }
run_timeout: 10s
kill_grace: 2s
targets:
  fast:
    adapter: shell
    command: ["sh", "-c", "echo hello-x-world"]
`, tempoURL)
	if err := os.WriteFile(filepath.Join(workdir, "mentat.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	feature := `Feature: junit
  Scenario: fast scenario
    Given the agent target "fast"
    When I run the agent with prompt "go"
    Then the result contains "x"
`
	if err := os.WriteFile(filepath.Join(workdir, "fast.feature"), []byte(feature), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	return workdir
}

// TestRunJUnitAndConsoleCoexist locks FR-003 / US3 AC1: one `mentat run --junit` writes
// a valid JUnit file AND leaves the godog pretty console on stdout — they coexist in the
// same run, the flag does not silence the console. Green-on-write characterization test.
func TestRunJUnitAndConsoleCoexist(t *testing.T) {
	t.Parallel()
	workdir := writeJUnitFixture(t, startTempoMock(t))
	junitPath := filepath.Join(workdir, "r.xml")

	cmd := exec.Command(mentatBin, "run", "--junit", junitPath, "fast.feature")
	cmd.Dir = workdir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run exited non-zero (%v); the passing scenario should exit 0\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// Surface 1: the JUnit file is present and is valid JUnit XML (a testsuite with a
	// testcase), emitted from the collector alongside the console.
	assertFileContains(t, junitPath, "<testsuite")
	assertFileContains(t, junitPath, "<testcase")

	// Surface 2: the pretty console still reached stdout in the SAME run — the scenario
	// name and the godog summary line prove the formatter ran and was not silenced.
	out := stdout.String()
	if out == "" {
		t.Fatalf("stdout is empty; --junit silenced the pretty console (E3 regression)\nstderr:\n%s", stderr.String())
	}
	for _, want := range []string{"fast scenario", "1 scenarios"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing pretty-console marker %q (E3 regression):\n%s", want, out)
		}
	}
}

// TestRunJUnitWriteFailureFailsRun locks the FR-003 reporter-failure contract: when the
// JUnit target cannot be written (parent dir absent), EmitReports fails, the process
// exits non-zero, and stderr names the write error — a report write failure is never
// swallowed. The scenario itself passes (mock resolves), so the non-zero exit comes
// solely from the failed report emission.
func TestRunJUnitWriteFailureFailsRun(t *testing.T) {
	t.Parallel()
	workdir := writeJUnitFixture(t, startTempoMock(t))
	// Parent directory does not exist, so the atomic temp+rename write cannot create
	// its temp file — a hard write failure, not a silent skip.
	badJunit := filepath.Join(workdir, "no-such-dir", "r.xml")

	cmd := exec.Command(mentatBin, "run", "--junit", badJunit, "fast.feature")
	cmd.Dir = workdir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected a non-zero exit from the junit write failure, got %v\nstderr:\n%s", err, stderr.String())
	}
	if ee.ExitCode() == 0 {
		t.Fatalf("exit code = 0, want non-zero after a junit write failure")
	}
	// stderr names the write failure (the report target and the OS error), so the
	// operator can see the run failed BECAUSE the junit could not be written.
	errOut := stderr.String()
	for _, want := range []string{"writing junit report", "no such file or directory"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr missing write-error marker %q:\n%s", want, errOut)
		}
	}
	// The file was not (partially) created, and the console still ran — the failure is
	// isolated to the report write.
	if _, statErr := os.Stat(badJunit); statErr == nil {
		t.Fatalf("junit file %q exists despite the write failure", badJunit)
	}
	if stdout.Len() == 0 {
		t.Fatalf("stdout is empty; the console should still run when only the junit write fails")
	}
}
