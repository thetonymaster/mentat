package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/thetonymaster/mentat/internal/core"
)

type shell struct{}

func NewShell() core.Driver { return shell{} }

func (shell) Run(ctx context.Context, spec core.RunSpec) (core.RunResult, error) {
	if len(spec.Command) == 0 {
		return core.RunResult{}, fmt.Errorf("shell: empty command for target %q", spec.Target)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)

	// Base env = inherited, plus explicit spec.Env, plus injected correlation.
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	if ra := resourceAttrs(spec.Tags); ra != "" {
		env = append(env, "OTEL_RESOURCE_ATTRIBUTES="+ra)
	}
	cmd.Env = env

	// Run the SUT in its own process group so lifecycle control reaches the whole
	// tree, not just the direct child (feature 003, FR-002/FR-003):
	//   - Setpgid: pgid == the child's pid; signalling -pgid reaches every descendant.
	//   - Cancel: on context cancellation, SIGTERM the group (polite first). The
	//     post-Wait SIGKILL below is the forceful escalation.
	//   - WaitDelay: bound Wait so a descendant holding the stdio pipes cannot hang
	//     the runner, and a signal-ignoring child is force-killed after the grace.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return signalGroup(cmd, syscall.SIGTERM) }
	if spec.KillGrace > 0 {
		cmd.WaitDelay = spec.KillGrace
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// Reap the whole process group on every exit path (normal exit, timeout, cancel).
	// Cancel/WaitDelay only guarantee the direct child; a SIGKILL to the group ensures
	// no descendant outlives the run beyond the grace period (FR-002). ESRCH (the group
	// is already empty) is the normal case and is swallowed by signalGroup.
	_ = signalGroup(cmd, syscall.SIGKILL)

	// ErrWaitDelay means the child exited successfully but a descendant kept a stdio
	// pipe open past WaitDelay; Go finalized the captured output and closed the pipes,
	// so the run completed normally — not a failure (FR-003).
	if errors.Is(runErr, exec.ErrWaitDelay) {
		runErr = nil
	}

	exit := 0
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		// A cancelled/expired context kills the process, which surfaces as an
		// ExitError. Don't mistake that for a normal non-zero exit — the run was
		// interrupted, so report the cancellation cause instead.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return core.RunResult{}, fmt.Errorf("shell: exec %v canceled for run %q: %w", spec.Command, spec.RunID, ctxErr)
		}
		exit = ee.ExitCode()
	} else if runErr != nil {
		return core.RunResult{}, fmt.Errorf("shell: exec %v: %w (stderr: %s)", spec.Command, runErr, stderr.String())
	}

	out := core.Output{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
		Answer:   core.ExtractAnswer(stdout.String()),
	}
	return core.RunResult{RunID: spec.RunID, Output: out}, nil
}

// signalGroup sends sig to the entire process group led by cmd's process (pgid ==
// the leader's pid, established via Setpgid). A negative pid targets the group, so
// the signal reaches every descendant that stayed in it. It is a no-op when the
// process never started, and treats ESRCH (the group has already exited) as success
// — that is the expected outcome on the reap path, not an error to surface.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// resourceAttrs renders spec.Tags as the OTEL_RESOURCE_ATTRIBUTES value
// (k=v,k=v) with sorted keys for determinism.
func resourceAttrs(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, otelEncode(k)+"="+otelEncode(tags[k]))
	}
	return strings.Join(parts, ",")
}

// otelEncode percent-encodes the OTEL_RESOURCE_ATTRIBUTES reserved delimiters
// (',' and '=') per the OpenTelemetry resource spec. '%' is encoded first so
// existing percent signs round-trip. Nothing else is encoded — a minimal
// spec-compliant decoder reverses exactly this set.
func otelEncode(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, ",", "%2C")
	s = strings.ReplaceAll(s, "=", "%3D")
	return s
}
