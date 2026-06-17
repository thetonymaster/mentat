package driver

import (
	"context"
	"strings"
	"testing"

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
		spec        core.RunSpec
		wantErr     bool
		wantErrSub  string
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
			res, err := NewShell().Run(context.Background(), tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.wantErrSub != "" && !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
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
