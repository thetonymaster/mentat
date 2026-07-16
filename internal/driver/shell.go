package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

type shell struct{ logger *slog.Logger }

// NewShell returns the shell driver adapter. Options are applied over a silent
// (discard-handler) logger default so the seam narrates nothing unless a caller
// opts in via WithLogger; the variadic keeps existing NewShell() call sites
// compiling.
func NewShell(opts ...Option) core.Driver {
	logger := resolveOptions(opts).logger
	return shell{logger: logger}
}

func (s shell) Run(ctx context.Context, spec core.RunSpec) (core.RunResult, error) {
	if len(spec.Command) == 0 {
		return core.RunResult{}, fmt.Errorf("shell: empty command for target %q", spec.Target)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)

	// Base env = inherited, plus explicit spec.Env, plus injected correlation.
	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	// Merge Mentat's correlation tags over any ambient OTEL_RESOURCE_ATTRIBUTES the
	// SUT already carries (FR-006): the developer's resource attributes survive and
	// Mentat wins key collisions so test.run.id correlation stays intact. A malformed
	// ambient value is a hard error naming the run — never a silent drop (Constitution IV).
	ambient := os.Getenv("OTEL_RESOURCE_ATTRIBUTES")
	ra, err := mergeResourceAttrs(ambient, spec.Tags)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("shell: merge OTEL_RESOURCE_ATTRIBUTES for run %q: %w", spec.RunID, err)
	}
	if ra != "" {
		env = append(env, "OTEL_RESOURCE_ATTRIBUTES="+ra)
	}
	cmd.Env = env

	// Narrate the Mentat-SET environment only (Debug): the spec.Env entries plus
	// the merged OTEL_RESOURCE_ATTRIBUTES (which deliberately folds in the SUT's
	// ambient resource attrs). No OTHER inherited os.Environ() value is logged, so
	// ambient secrets in the runner's environment cannot leak into narration. The
	// attr slice is built only when Debug is enabled (free at Info/discard). Keys
	// are sorted for deterministic output.
	if s.logger.Enabled(ctx, slog.LevelDebug) {
		attrs := make([]any, 0, 2*len(spec.Env)+4)
		attrs = append(attrs, "run_id", spec.RunID)
		keys := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			attrs = append(attrs, k, spec.Env[k])
		}
		if ra != "" {
			attrs = append(attrs, "OTEL_RESOURCE_ATTRIBUTES", ra)
		}
		s.logger.DebugContext(ctx, "drive.env", attrs...)
	}

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
	start := time.Now()
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

	answer, err := core.ExtractAnswer(stdout.String(), spec.Extract)
	if err != nil {
		// An unresolvable marker/pattern is a hard run failure naming the offending
		// marker/pattern, never a silent empty answer (Constitution IV). Same shape
		// as the exec-failure returns above.
		return core.RunResult{}, fmt.Errorf("shell: extract answer for run %q: %w", spec.RunID, err)
	}
	out := core.Output{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
		Answer:   answer,
	}
	// Normal-completion narration (Debug): the exit code is known and the run is
	// not a cancellation (those return early above, before this point).
	s.logger.DebugContext(ctx, "drive.done", "run_id", spec.RunID, "exit_code", exit, "elapsed", time.Since(start))
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

// encodeResourceAttrs renders a resource-attribute map as the
// OTEL_RESOURCE_ATTRIBUTES value (k=v,k=v) with sorted keys for determinism and
// each key/value percent-encoded via otelEncode. Empty map -> "".
func encodeResourceAttrs(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, otelEncode(k)+"="+otelEncode(m[k]))
	}
	return strings.Join(parts, ",")
}

// mergeResourceAttrs parses the ambient OTEL_RESOURCE_ATTRIBUTES value (k=v,k=v,
// percent-encoded per the OTel resource spec), overlays the Mentat tags (Mentat
// wins key collisions, incl. test.run.id — correlation integrity is the product),
// and re-encodes in sorted order. A non-empty ambient segment without a '='
// delimiter is a hard error naming the offending value (Constitution IV — never a
// silent drop). Returns ("", nil) only when both ambient and tags are empty.
func mergeResourceAttrs(ambient string, tags map[string]string) (string, error) {
	merged := make(map[string]string, len(tags))
	for _, seg := range strings.Split(ambient, ",") {
		if seg == "" {
			continue // stray/trailing comma is benign
		}
		kv := strings.SplitN(seg, "=", 2)
		if len(kv) != 2 {
			return "", fmt.Errorf("shell: malformed OTEL_RESOURCE_ATTRIBUTES segment %q (want k=v)", seg)
		}
		merged[otelDecode(kv[0])] = otelDecode(kv[1])
	}
	// Mentat's correlation tags overlay the ambient set — Mentat wins collisions.
	for k, v := range tags {
		merged[k] = v
	}
	return encodeResourceAttrs(merged), nil
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

// otelDecode is the exact inverse of otelEncode. Ordering is load-bearing: because
// otelEncode encodes '%'->'%25' FIRST, otelDecode must decode '%25'->'%' LAST, so a
// literal "%2C" that was encoded to "%252C" round-trips back to "%2C" rather than a
// comma. Used to parse an ambient OTEL_RESOURCE_ATTRIBUTES value the SUT already has.
func otelDecode(s string) string {
	s = strings.ReplaceAll(s, "%2C", ",")
	s = strings.ReplaceAll(s, "%3D", "=")
	s = strings.ReplaceAll(s, "%25", "%") // last: reverses the '%'->'%25' done first in otelEncode
	return s
}
