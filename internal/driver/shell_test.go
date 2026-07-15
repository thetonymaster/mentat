package driver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestShellCapturesAnswerAndInjectsRunIDEnv(t *testing.T) {
	// The script echoes OTEL_RESOURCE_ATTRIBUTES so we can assert injection,
	// then prints the "answer" on its own line. Clear any ambient
	// OTEL_RESOURCE_ATTRIBUTES on the host so the merge (feature 005 D4) yields
	// exactly Mentat's tags — the exact Answer assertion below depends on it.
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
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
			// Clear ambient OTEL_RESOURCE_ATTRIBUTES so the merge yields exactly
			// Mentat's sorted tags — this row asserts on the full value.
			setup: func(t *testing.T) {
				t.Helper()
				t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
			},
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

// --- Feature 005 (US3, D4): OTEL_RESOURCE_ATTRIBUTES ambient merge (FR-006) ---

// TestOtelDecodeRoundTrip pins otelDecode as the exact inverse of otelEncode.
// Because otelEncode encodes '%'->'%25' FIRST, otelDecode must decode '%25'->'%'
// LAST; the "%2C" literal case fails loudly if that ordering is wrong (a naive
// decode-percent-first would turn encoded "%252C" back into a comma).
func TestOtelDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{name: "plain", in: "service.name"},
		{name: "percent", in: "a%b"},
		{name: "comma", in: "a,b"},
		{name: "equals", in: "a=b"},
		{name: "all_reserved", in: "a%,=b"},
		{name: "literal_percent_2C", in: "%2C"},
		{name: "literal_percent_25", in: "%25"},
		{name: "empty", in: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enc := otelEncode(tt.in)
			got := otelDecode(enc)
			if got != tt.in {
				t.Fatalf("otelDecode(otelEncode(%q)) = %q, want %q (encoded=%q)", tt.in, got, tt.in, enc)
			}
		})
	}
}

// TestMergeResourceAttrs pins FR-006/SC-004: Mentat overlays its correlation tags
// onto the SUT's ambient OTEL_RESOURCE_ATTRIBUTES instead of clobbering it. Mentat
// wins key collisions (test.run.id correlation integrity), output is sorted-key
// deterministic, and a malformed ambient segment is a hard error naming the value
// (Constitution IV — never a silent drop).
func TestMergeResourceAttrs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ambient    string
		tags       map[string]string
		want       string
		wantErr    bool
		wantErrSub string
	}{
		{
			name:    "empty_ambient_and_tags_yields_empty",
			ambient: "",
			tags:    nil,
			want:    "",
		},
		{
			name:    "ambient_only_preserved_decoded_then_reencoded_sorted",
			ambient: "zzz=1,aaa=2",
			tags:    nil,
			want:    "aaa=2,zzz=1",
		},
		{
			name:    "mentat_only_matches_resourceAttrs",
			ambient: "",
			tags:    map[string]string{"test.run.id": "run-1", "aaa.key": "a"},
			want:    "aaa.key=a,test.run.id=run-1",
		},
		{
			name:    "both_no_collision_union_sorted",
			ambient: "service.name=checkout",
			tags:    map[string]string{"test.run.id": "run-9"},
			want:    "service.name=checkout,test.run.id=run-9",
		},
		{
			name:    "collision_mentat_wins_test_run_id",
			ambient: "test.run.id=ambient-run,service.name=checkout",
			tags:    map[string]string{"test.run.id": "run-9"},
			want:    "service.name=checkout,test.run.id=run-9",
		},
		{
			name:    "percent_encoded_ambient_roundtrips",
			ambient: "label=a%2Cb%3Dc",
			tags:    map[string]string{"test.run.id": "run-9"},
			want:    "label=a%2Cb%3Dc,test.run.id=run-9",
		},
		{
			name:    "trailing_comma_benign",
			ambient: "service.name=checkout,",
			tags:    nil,
			want:    "service.name=checkout",
		},
		{
			name:       "malformed_no_equals_errors_naming_value",
			ambient:    "broken-no-equals",
			tags:       map[string]string{"test.run.id": "run-9"},
			wantErr:    true,
			wantErrSub: "broken-no-equals",
		},
		{
			name:       "malformed_second_segment_errors_naming_value",
			ambient:    "a=b,broken",
			tags:       nil,
			wantErr:    true,
			wantErrSub: "broken",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := mergeResourceAttrs(tt.ambient, tt.tags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("mergeResourceAttrs(%q, %v) err = %v, wantErr = %v", tt.ambient, tt.tags, err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.wantErrSub != "" && !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not name offending value %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("mergeResourceAttrs(%q, %v) = %q, want %q", tt.ambient, tt.tags, got, tt.want)
			}
		})
	}
}

// TestShellMergesAmbientResourceAttrs is the end-to-end proof of FR-006/SC-004
// through Run: an ambient OTEL_RESOURCE_ATTRIBUTES the SUT already carries must
// survive, with Mentat's correlation tag overlaid (not clobbered). It sets the
// ambient value via t.Setenv (so os.Environ picks it up) and drives a child that
// prints its effective OTEL_RESOURCE_ATTRIBUTES. No t.Parallel: t.Setenv forbids it.
func TestShellMergesAmbientResourceAttrs(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "ambient.key=ambient.val")
	spec := core.RunSpec{
		Command: []string{"sh", "-c", `printf '%s' "$OTEL_RESOURCE_ATTRIBUTES"`},
		Tags:    map[string]string{"test.run.id": "run-9"},
		RunID:   "run-9",
	}
	res, err := NewShell().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.Output.Stdout
	if !strings.Contains(got, "ambient.key=ambient.val") {
		t.Fatalf("ambient OTEL_RESOURCE_ATTRIBUTES clobbered; child saw %q", got)
	}
	if !strings.Contains(got, "test.run.id=run-9") {
		t.Fatalf("Mentat's test.run.id missing after merge; child saw %q", got)
	}
	// Deterministic sorted-key merge: ambient.key < test.run.id.
	if want := "ambient.key=ambient.val,test.run.id=run-9"; got != want {
		t.Fatalf("merged OTEL_RESOURCE_ATTRIBUTES = %q, want %q", got, want)
	}
}

// TestShellMergeMalformedAmbientIsHardError pins Constitution IV: a malformed
// ambient OTEL_RESOURCE_ATTRIBUTES is a hard, named error from Run — never a silent
// drop that would strip the developer's resource attributes. No t.Parallel: t.Setenv.
func TestShellMergeMalformedAmbientIsHardError(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "broken-no-equals")
	spec := core.RunSpec{
		Command: []string{"sh", "-c", "echo hi"},
		Tags:    map[string]string{"test.run.id": "run-bad"},
		RunID:   "run-bad",
	}
	_, err := NewShell().Run(context.Background(), spec)
	if err == nil {
		t.Fatal("expected hard error on malformed ambient OTEL_RESOURCE_ATTRIBUTES, got nil")
	}
	for _, want := range []string{"run-bad", "broken-no-equals"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err.Error(), want)
		}
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

// --- Feature 005 (US1): structured slog narration ---

// debugBufLogger returns a *slog.Logger writing text records at Debug level into
// buf, so a narration test can assert on the emitted attribute tokens.
func debugBufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// findDriverLogLine returns the first line in out whose slog msg token equals
// msg, or "" if no record carries that message.
func findDriverLogLine(out, msg string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "msg="+msg) {
			return line
		}
	}
	return ""
}

// captureStdio redirects the process's real stdout+stderr for the duration of fn
// and returns everything written to them — proving the SC-005 silent-default
// contract: with no injected logger the driver must reach neither stream. The
// SUT subprocess writes into cmd's own buffers, never the real streams, so a
// clean run captures nothing.
func captureStdio(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = w, w
	defer func() {
		os.Stdout, os.Stderr = origOut, origErr
	}()
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	return <-done
}

// TestShellNarratesDriveEnvExcludesInheritedEnv pins the driver half of US1:
// drive.env (Debug) logs ONLY Mentat-set environment — the spec.Env entries and
// the computed OTEL_RESOURCE_ATTRIBUTES — never inherited os.Environ values, and
// drive.done (Debug) reports the exit code. No t.Parallel: t.Setenv forbids it.
func TestShellNarratesDriveEnvExcludesInheritedEnv(t *testing.T) {
	// An inherited secret the driver must never narrate.
	t.Setenv("SOME_INHERITED", "super-secret-value")

	var buf bytes.Buffer
	spec := core.RunSpec{
		Command: []string{"sh", "-c", "echo hi"},
		Env:     map[string]string{"MY_VAR": "my_value"},
		Tags:    map[string]string{"test.run.id": "run-env"},
		RunID:   "run-env",
	}
	res, err := NewShell(WithLogger(debugBufLogger(&buf))).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Output.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.Output.ExitCode)
	}
	out := buf.String()

	envLine := findDriverLogLine(out, "drive.env")
	if envLine == "" {
		t.Fatalf("no drive.env record; narration:\n%s", out)
	}
	for _, want := range []string{"MY_VAR=my_value", "OTEL_RESOURCE_ATTRIBUTES", "test.run.id=run-env", "run_id=run-env"} {
		if !strings.Contains(envLine, want) {
			t.Fatalf("drive.env record %q missing %q", envLine, want)
		}
	}
	// The inherited variable (key OR value) must never surface in ANY record.
	if strings.Contains(out, "SOME_INHERITED") || strings.Contains(out, "super-secret-value") {
		t.Fatalf("drive.env leaked inherited env; narration:\n%s", out)
	}

	doneLine := findDriverLogLine(out, "drive.done")
	if doneLine == "" {
		t.Fatalf("no drive.done record; narration:\n%s", out)
	}
	for _, want := range []string{"run_id=run-env", "exit_code=0"} {
		if !strings.Contains(doneLine, want) {
			t.Fatalf("drive.done record %q missing %q", doneLine, want)
		}
	}
}

// TestShellNarratesDriveDoneExitCode pins that drive.done carries the real exit
// code on the normal-completion path — including a non-zero exit (which is not a
// driver error, feature 003).
func TestShellNarratesDriveDoneExitCode(t *testing.T) {
	tests := []struct {
		name     string
		command  []string
		wantExit string
	}{
		{name: "zero_exit", command: []string{"sh", "-c", "echo hi"}, wantExit: "exit_code=0"},
		{name: "nonzero_exit", command: []string{"sh", "-c", "exit 3"}, wantExit: "exit_code=3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			spec := core.RunSpec{Command: tt.command, RunID: "run-exit"}
			if _, err := NewShell(WithLogger(debugBufLogger(&buf))).Run(context.Background(), spec); err != nil {
				t.Fatalf("Run: %v", err)
			}
			doneLine := findDriverLogLine(buf.String(), "drive.done")
			if doneLine == "" {
				t.Fatalf("no drive.done record; narration:\n%s", buf.String())
			}
			if !strings.Contains(doneLine, tt.wantExit) {
				t.Fatalf("drive.done record %q missing %q", doneLine, tt.wantExit)
			}
		})
	}
}

// TestShellSilentByDefaultEmitsZeroBytes pins SC-005 for the driver: with the
// default (discard) logger a successful Run writes nothing to the process's real
// stdout/stderr.
func TestShellSilentByDefaultEmitsZeroBytes(t *testing.T) {
	spec := core.RunSpec{
		Command: []string{"sh", "-c", "echo hi"},
		Env:     map[string]string{"MY_VAR": "my_value"},
		Tags:    map[string]string{"test.run.id": "run-silent"},
		RunID:   "run-silent",
	}
	out := captureStdio(t, func() {
		if _, err := NewShell().Run(context.Background(), spec); err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("silent default must emit zero bytes, got:\n%q", out)
	}
}
