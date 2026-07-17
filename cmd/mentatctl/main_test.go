package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/engine"
)

// TestNewLoggerLevels mirrors cmd/mentat's assertion on the shared logger helper
// so both binaries are proven to map -v/-vv identically (FR-001, D6 anti-drift):
// no flags is a silent discard handler (SC-005), -v emits Info but suppresses
// Debug, -vv emits both, and -vv wins when both flags are set.
func TestNewLoggerLevels(t *testing.T) {
	t.Parallel()
	const (
		infoProbe  = "probe-info-msg"
		debugProbe = "probe-debug-msg"
	)
	tests := []struct {
		name      string
		verbose   bool
		debug     bool
		wantInfo  bool
		wantDebug bool
		wantEmpty bool
	}{
		{name: "no flags is silent discard", verbose: false, debug: false, wantEmpty: true},
		{name: "-v emits info suppresses debug", verbose: true, debug: false, wantInfo: true, wantDebug: false},
		{name: "-vv emits info and debug", verbose: false, debug: true, wantInfo: true, wantDebug: true},
		{name: "both set debug wins", verbose: true, debug: true, wantInfo: true, wantDebug: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := engine.NewLogger(&buf, tt.verbose, tt.debug)
			logger.Info(infoProbe)
			logger.Debug(debugProbe)
			out := buf.String()
			if tt.wantEmpty {
				if len(out) != 0 {
					t.Fatalf("silent default wrote %d bytes, want 0: %q", len(out), out)
				}
				return
			}
			if got := strings.Contains(out, infoProbe); got != tt.wantInfo {
				t.Fatalf("info present=%v want %v (out=%q)", got, tt.wantInfo, out)
			}
			if got := strings.Contains(out, debugProbe); got != tt.wantDebug {
				t.Fatalf("debug present=%v want %v (out=%q)", got, tt.wantDebug, out)
			}
		})
	}
}

func TestCheckDomainVerb(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		sub     string
		wantErr string // empty means nil error expected
	}{
		{name: "unknown verb errors before deps", domain: "agent", sub: "bogus", wantErr: "unknown subcommand"},
		{name: "service+tools errors", domain: "service", sub: "tools", wantErr: "only valid for the agent domain"},
		{name: "agent+services errors", domain: "agent", sub: "services", wantErr: "only valid for the service domain"},
		{name: "agent+tools ok", domain: "agent", sub: "tools", wantErr: ""},
		{name: "service+services ok", domain: "service", sub: "services", wantErr: ""},
		{name: "agent+run ok", domain: "agent", sub: "run", wantErr: ""},
		{name: "service+diff ok", domain: "service", sub: "diff", wantErr: ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := checkDomainVerb(tt.domain, tt.sub)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q missing substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestResolvePrompt pins the --prompt-file / stdin / mutual-exclusion contract
// (US7): --prompt-file wins nothing — it is an error to set both; `-` reads
// stdin; a missing file names its path (no silent fallback).
func TestResolvePrompt(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(file, []byte("prompt from file\n"), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
	tests := []struct {
		name       string
		prompt     string
		promptFile string
		stdin      string
		want       string
		wantErr    string
	}{
		{name: "no prompt-file returns the prompt flag", prompt: "inline", want: "inline"},
		{name: "prompt-file reads the file", promptFile: file, want: "prompt from file"},
		{name: "dash reads stdin", promptFile: "-", stdin: "from stdin\n", want: "from stdin"},
		{name: "both flags set is an error", prompt: "inline", promptFile: file, wantErr: "mutually exclusive"},
		{name: "missing file names the path", promptFile: filepath.Join(dir, "nope.txt"), wantErr: "nope.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePrompt(tt.prompt, tt.promptFile, strings.NewReader(tt.stdin))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err=%v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolvePrompt = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTimeoutErr proves --timeout surfaces a descriptive error naming the
// duration on deadline, and is transparent otherwise (no timeout / non-deadline
// error / nil run error).
func TestTimeoutErr(t *testing.T) {
	base := errors.New("boom")
	tests := []struct {
		name    string
		timeout time.Duration
		ctxErr  error
		runErr  error
		wantNil bool
		wantSub string
	}{
		{name: "nil run error stays nil", timeout: time.Second, ctxErr: context.DeadlineExceeded, runErr: nil, wantNil: true},
		{name: "deadline exceeded names the duration", timeout: 5 * time.Second, ctxErr: context.DeadlineExceeded, runErr: base, wantSub: "timed out after 5s"},
		{name: "no timeout passes error through", timeout: 0, ctxErr: nil, runErr: base, wantSub: "boom"},
		{name: "non-deadline error passes through", timeout: time.Second, ctxErr: context.Canceled, runErr: base, wantSub: "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := timeoutErr(tt.timeout, tt.ctxErr, tt.runErr)
			if tt.wantNil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err=%v, want containing %q", err, tt.wantSub)
			}
		})
	}
}

// TestBindRunFlags proves the three US7 flags are registered and parse into the
// run flag set (plumbing), alongside a pre-existing flag as a control.
func TestBindRunFlags(t *testing.T) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	f := bindRunFlags(fs)
	args := []string{"--prompt-file", "p.txt", "--timeout", "2s", "-o", "out.txt", "--target", "bot"}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *f.promptFile != "p.txt" {
		t.Fatalf("promptFile = %q, want %q", *f.promptFile, "p.txt")
	}
	if *f.output != "out.txt" {
		t.Fatalf("output = %q, want %q", *f.output, "out.txt")
	}
	if *f.timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want %v", *f.timeout, 2*time.Second)
	}
	if *f.target != "bot" {
		t.Fatalf("target = %q, want %q", *f.target, "bot")
	}
}

// TestCheckFlags proves the post-parse validation rejects a flag the selected
// verb does not read (Constitution IV: no silently-ignored flag, naming flag +
// verb) and rejects a negative --timeout, while supported flags, the universal
// flags (config/-v/-vv), and a zero/positive timeout pass.
func TestCheckFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sub     string
		args    []string
		timeout time.Duration
		wantErr string // empty => nil expected
	}{
		// supported / universal flags pass, per verb
		{name: "run supports timeout", sub: "run", args: []string{"--timeout", "5s"}, timeout: 5 * time.Second},
		{name: "run supports prompt-file", sub: "run", args: []string{"--prompt-file", "p.txt"}},
		{name: "run supports o", sub: "run", args: []string{"-o", "out.txt"}},
		{name: "run supports scenario save quiet json target", sub: "run", args: []string{"--scenario", "s", "--save", "f", "--quiet", "--json", "--target", "b"}},
		{name: "trace supports last", sub: "trace", args: []string{"--last"}},
		{name: "tools supports last", sub: "tools", args: []string{"--last"}},
		{name: "services supports last", sub: "services", args: []string{"--last"}},
		{name: "replay supports feature and last", sub: "replay", args: []string{"--feature", "f.feature", "--last"}},
		{name: "universal config allowed for diff", sub: "diff", args: []string{"--config", "x.yaml"}},
		{name: "universal verbose allowed for trace", sub: "trace", args: []string{"-v"}},
		{name: "universal debug allowed for tools", sub: "tools", args: []string{"-vv"}},
		{name: "no flags set is fine", sub: "diff", args: nil},

		// unsupported flags rejected, naming flag + verb
		{name: "trace rejects timeout", sub: "trace", args: []string{"--timeout", "5s"}, wantErr: `flag "--timeout" is not supported by the "trace" command`},
		{name: "replay rejects o", sub: "replay", args: []string{"-o", "out"}, wantErr: `flag "--o" is not supported by the "replay" command`},
		{name: "diff rejects prompt-file", sub: "diff", args: []string{"--prompt-file", "x"}, wantErr: `flag "--prompt-file" is not supported by the "diff" command`},
		{name: "run rejects last", sub: "run", args: []string{"--last"}, wantErr: `flag "--last" is not supported by the "run" command`},
		{name: "trace rejects feature", sub: "trace", args: []string{"--feature", "f"}, wantErr: `flag "--feature" is not supported by the "trace" command`},

		// negative timeout rejected (supported verb, bad value); zero/positive pass
		{name: "run rejects negative timeout", sub: "run", args: []string{"--timeout", "-1s"}, timeout: -time.Second, wantErr: "--timeout must be non-negative, got -1s"},
		{name: "run allows zero timeout", sub: "run", args: []string{"--timeout", "0s"}, timeout: 0},
		{name: "run allows positive timeout", sub: "run", args: []string{"--timeout", "3s"}, timeout: 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet(tt.sub, flag.ContinueOnError)
			bindRunFlags(fs)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse %v: %v", tt.args, err)
			}
			err := checkFlags(tt.sub, fs, tt.timeout)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("checkFlags(%q, %v) = %v, want nil", tt.sub, tt.args, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("checkFlags(%q, %v) = %v, want containing %q", tt.sub, tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestSplitDomainVerb(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantDomain string
		wantSub    string
		wantRest   []string
		wantErr    bool
	}{
		{name: "agent run", args: []string{"agent", "run", "--target", "x"}, wantDomain: "agent", wantSub: "run", wantRest: []string{"--target", "x"}},
		{name: "service services with id", args: []string{"service", "services", "id1"}, wantDomain: "service", wantSub: "services", wantRest: []string{"id1"}},
		{name: "service diff", args: []string{"service", "diff", "a", "b"}, wantDomain: "service", wantSub: "diff", wantRest: []string{"a", "b"}},
		{name: "unknown domain errors", args: []string{"bogus", "run"}, wantErr: true},
		{name: "missing verb errors", args: []string{"agent"}, wantErr: true},
		{name: "no args errors", args: []string{}, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			domain, sub, rest, err := splitDomainVerb(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if domain != tt.wantDomain || sub != tt.wantSub {
				t.Fatalf("got (%q,%q), want (%q,%q)", domain, sub, tt.wantDomain, tt.wantSub)
			}
			if len(rest) != len(tt.wantRest) {
				t.Fatalf("rest=%v want=%v", rest, tt.wantRest)
			}
			for i := range rest {
				if rest[i] != tt.wantRest[i] {
					t.Fatalf("rest[%d]=%q want %q", i, rest[i], tt.wantRest[i])
				}
			}
		})
	}
}
