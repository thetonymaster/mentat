package driver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestShellCapturesAnswerAndInjectsRunIDEnv(t *testing.T) {
	// The script echoes OTEL_RESOURCE_ATTRIBUTES so we can assert injection,
	// then prints the "answer" on its own line.
	spec := core.RunSpec{
		Command: []string{"sh", "-c", `printf '%s\n' "$OTEL_RESOURCE_ATTRIBUTES"; printf 'the answer\n'`},
		Tags:    map[string]string{"test.run.id": "abc123"},
		RunID:   "abc123",
	}
	res, err := NewShell().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RunID != "abc123" {
		t.Fatalf("RunID = %q", res.RunID)
	}
	if !strings.Contains(res.Output.Stdout, "test.run.id=abc123") {
		t.Fatalf("OTEL_RESOURCE_ATTRIBUTES not injected; stdout=%q", res.Output.Stdout)
	}
	if res.Output.Answer != "test.run.id=abc123\nthe answer" {
		t.Fatalf("Answer = %q", res.Output.Answer)
	}
}

func TestShellAdditionalBranches(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T)
		ctx         func(t *testing.T) context.Context
		spec        core.RunSpec
		wantErr     bool
		wantErrSub  string
		wantErrIs   error
		wantExit    int
		checkOutput func(t *testing.T, res core.RunResult)
	}{
		{
			name: "empty_command_returns_descriptive_error",
			spec: core.RunSpec{
				Target:  "my-target",
				Command: []string{},
				RunID:   "run-empty",
			},
			wantErr:    true,
			wantErrSub: "my-target",
		},
		{
			name: "nonzero_exit_no_error_exit_code_captured",
			spec: core.RunSpec{
				Command: []string{"sh", "-c", "exit 3"},
				RunID:   "run-exit3",
			},
			wantErr:  false,
			wantExit: 3,
		},
		{
			name: "nonexistent_binary_returns_wrapped_error",
			spec: core.RunSpec{
				Command: []string{"definitely-not-a-real-binary-xyz"},
				RunID:   "run-nobin",
			},
			wantErr:    true,
			wantErrSub: "definitely-not-a-real-binary-xyz",
		},
		{
			name: "context_deadline_kills_process_returns_error",
			// The process must START and then be killed by the deadline so we hit
			// the ExitError+ctx.Err() branch (a pre-cancelled context would fail at
			// Start and hit the other branch). 'sleep 5' far outlasts the 30ms
			// deadline, so the deadline always fires first regardless of host load,
			// while the test still completes in tens of ms.
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			spec: core.RunSpec{
				Command: []string{"sleep", "5"},
				RunID:   "run-cancel",
			},
			wantErr:    true,
			wantErrSub: "canceled",
			wantErrIs:  context.DeadlineExceeded,
		},
		{
			name: "multiple_tags_sorted_otel_attrs",
			spec: core.RunSpec{
				Command: []string{"sh", "-c", `printf '%s\n' "$OTEL_RESOURCE_ATTRIBUTES"`},
				Tags: map[string]string{
					"zzz.key":     "zzz",
					"aaa.key":     "aaa",
					"test.run.id": "sorted-run",
				},
				RunID: "sorted-run",
			},
			wantErr: false,
			checkOutput: func(t *testing.T, res core.RunResult) {
				t.Helper()
				got := strings.TrimSpace(res.Output.Stdout)
				// Keys must be in sorted order: aaa.key < test.run.id < zzz.key
				wantAttrs := "aaa.key=aaa,test.run.id=sorted-run,zzz.key=zzz"
				if got != wantAttrs {
					t.Fatalf("OTEL_RESOURCE_ATTRIBUTES = %q, want %q", got, wantAttrs)
				}
			},
		},
		{
			name: "otel_reserved_chars_percent_encoded",
			// A tag value containing the OTel-reserved delimiters ',' and '='
			// must be percent-encoded, otherwise the produced
			// OTEL_RESOURCE_ATTRIBUTES is malformed and a spec-compliant SDK
			// discards the WHOLE variable, silently breaking correlation.
			spec: core.RunSpec{
				Command: []string{"sh", "-c", `printf '%s' "$OTEL_RESOURCE_ATTRIBUTES"`},
				Tags: map[string]string{
					"test.run.id": "abc-123",
					"label":       "a,b=c",
				},
				RunID: "abc-123",
			},
			wantErr: false,
			checkOutput: func(t *testing.T, res core.RunResult) {
				t.Helper()
				got := res.Output.Stdout
				if !strings.Contains(got, "label=a%2Cb%3Dc") {
					t.Fatalf("encoded label missing; OTEL_RESOURCE_ATTRIBUTES=%q", got)
				}
				if !strings.Contains(got, "test.run.id=abc-123") {
					t.Fatalf("unaffected test.run.id missing; OTEL_RESOURCE_ATTRIBUTES=%q", got)
				}
				if strings.Contains(got, "a,b=c") {
					t.Fatalf("raw reserved chars leaked unencoded; OTEL_RESOURCE_ATTRIBUTES=%q", got)
				}
			},
		},
		{
			name: "empty_tags_no_otel_attr_injected",
			// Guarantee hermeticity: if the host exports OTEL_RESOURCE_ATTRIBUTES,
			// clear it so os.Environ() passes an empty value. The bash expansion
			// ${VAR:-DEFAULT} treats an empty value the same as unset, so the
			// child still prints NOT_SET — proving the driver injects nothing when
			// spec.Tags is empty. t.Setenv restores the original value via t.Cleanup.
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
			},
			spec: core.RunSpec{
				Command: []string{"sh", "-c", `printf '%s\n' "${OTEL_RESOURCE_ATTRIBUTES:-NOT_SET}"`},
				Tags:    map[string]string{},
				RunID:   "run-notags",
			},
			wantErr: false,
			checkOutput: func(t *testing.T, res core.RunResult) {
				t.Helper()
				got := strings.TrimSpace(res.Output.Stdout)
				if got != "NOT_SET" {
					t.Fatalf("Expected OTEL_RESOURCE_ATTRIBUTES not to be set; stdout=%q", res.Output.Stdout)
				}
			},
		},
		{
			name: "spec_env_injected_into_child",
			// Covers the spec.Env branch: extra variables from spec.Env must
			// appear in the child process environment, appended after os.Environ().
			spec: core.RunSpec{
				Command: []string{"sh", "-c", `printf '%s\n' "$MY_VAR"`},
				Env:     map[string]string{"MY_VAR": "my_value"},
				RunID:   "run-specenv",
			},
			wantErr: false,
			checkOutput: func(t *testing.T, res core.RunResult) {
				t.Helper()
				got := strings.TrimSpace(res.Output.Stdout)
				if got != "my_value" {
					t.Fatalf("MY_VAR = %q, want %q", got, "my_value")
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}
			ctx := context.Background()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}
			res, err := NewShell().Run(ctx, tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.wantErrSub != "" && !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("error %q is not %v", err.Error(), tt.wantErrIs)
				}
				return
			}
			if tt.wantExit != 0 && res.Output.ExitCode != tt.wantExit {
				t.Fatalf("ExitCode = %d, want %d", res.Output.ExitCode, tt.wantExit)
			}
			if tt.checkOutput != nil {
				tt.checkOutput(t, res)
			}
		})
	}
}

// TestShellInjectsNoTraceparent is the shell-driver complement to the http
// driver's no-traceparent assertion (http_test.go): correlation rides
// OTEL_RESOURCE_ATTRIBUTES (a resource attribute), never a propagated
// traceparent, so the SUT's own first span roots the trace (spec §5). The host
// may export TRACEPARENT; t.Setenv clears it so ${VAR:-NONE} proves the driver
// injected nothing.
func TestShellInjectsNoTraceparent(t *testing.T) {
	t.Setenv("TRACEPARENT", "")
	spec := core.RunSpec{
		Command: []string{"sh", "-c", `printf '%s\n' "${TRACEPARENT:-NONE}"`},
		Tags:    map[string]string{"test.run.id": "run-tp"},
		RunID:   "run-tp",
	}
	res, err := NewShell().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(res.Output.Stdout); got != "NONE" {
		t.Fatalf("shell driver must not inject traceparent; TRACEPARENT=%q", got)
	}
}
