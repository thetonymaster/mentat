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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

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
