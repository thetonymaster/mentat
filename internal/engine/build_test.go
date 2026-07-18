package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
)

// extraStubDriver/Comparator/Judge are minimal seam stubs used to exercise the
// facade-funneled extra-registration path (WithExtraDriver/Comparator/Judge) and its
// collision detection at the composition root (spec 007 FR-002). They do no work —
// the collision check fires before any of them is ever invoked.
type extraStubDriver struct{}

func (extraStubDriver) Run(context.Context, core.RunSpec) (core.RunResult, error) {
	return core.RunResult{}, nil
}

type extraStubComparator struct{}

func (extraStubComparator) Name() string { return "extra-stub" }
func (extraStubComparator) Compare(context.Context, core.Evidence, core.Expectation) (core.Verdict, error) {
	return core.Verdict{Pass: true}, nil
}

type extraStubJudge struct{}

func (extraStubJudge) Judge(context.Context, core.JudgeRequest) (core.JudgeVerdict, error) {
	return core.JudgeVerdict{}, nil
}

// TestBuildAppliesExtraSeams pins the composition-root half of FR-002: facade-funneled
// driver/comparator/judge registrations land in the registry as first-class seams, and
// a name colliding with a built-in OR with an earlier extra fails loudly naming the
// seam and the conflicting name — never a silent last-wins overwrite (Constitution IV).
//
// No t.Parallel(): kept serial by convention (the seam registry is per-engine now).
func TestBuildAppliesExtraSeams(t *testing.T) {
	drv := extraStubDriver{}
	cmp := extraStubComparator{}
	jf := func(config.Config) (core.Judge, error) { return extraStubJudge{}, nil }

	tests := []struct {
		name       string
		opts       []Option
		wantErrSub []string // nil ⇒ Build succeeds
		wantDriver string   // non-empty ⇒ assert this driver name is registered after Build
	}{
		{name: "custom driver registers as first-class adapter", opts: []Option{WithExtraDriver("xdrv", drv)}, wantDriver: "xdrv"},
		{name: "driver collides with built-in", opts: []Option{WithExtraDriver("shell", drv)}, wantErrSub: []string{"WithDriver", "shell"}},
		{name: "driver collides with earlier extra", opts: []Option{WithExtraDriver("dup-d", drv), WithExtraDriver("dup-d", drv)}, wantErrSub: []string{"WithDriver", "dup-d"}},
		{name: "custom comparator registers", opts: []Option{WithExtraComparator("xcmp", cmp)}},
		{name: "comparator collides with built-in", opts: []Option{WithExtraComparator("result", cmp)}, wantErrSub: []string{"WithComparator", "result"}},
		{name: "comparator collides with earlier extra", opts: []Option{WithExtraComparator("dup-c", cmp), WithExtraComparator("dup-c", cmp)}, wantErrSub: []string{"WithComparator", "dup-c"}},
		{name: "custom judge registers", opts: []Option{WithExtraJudge("xjudge", jf)}},
		{name: "judge collides with built-in", opts: []Option{WithExtraJudge("claude", jf)}, wantErrSub: []string{"WithJudge", "claude"}},
		{name: "judge collides with earlier extra", opts: []Option{WithExtraJudge("dup-j", jf), WithExtraJudge("dup-j", jf)}, wantErrSub: []string{"WithJudge", "dup-j"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No targets ⇒ the adapter-validation loop is skipped, isolating the
			// extra-registration/collision behaviour under test.
			cfg := config.Config{OTLPEndpoint: "x"}
			eng, err := Build(cfg, nil, nil, tt.opts...)
			if len(tt.wantErrSub) == 0 {
				if err != nil {
					t.Fatalf("Build with extra seam: %v", err)
				}
				if tt.wantDriver != "" {
					if _, ok := eng.reg.Driver(tt.wantDriver); !ok {
						t.Fatalf("extra driver %q not registered after Build", tt.wantDriver)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("Build must reject %s, got nil error", tt.name)
			}
			for _, sub := range tt.wantErrSub {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("Build error = %q, want substring %q", err, sub)
				}
			}
		})
	}
}

// TestEngineAdapter proves the read-only Adapter accessor reports a configured
// target's adapter (used by the steps layer to reject request-body steps against a
// non-http target), and returns ("", false) for an unknown target so callers can
// fall through to their existing "no target set"/"unknown target" paths.
func TestEngineAdapter(t *testing.T) {
	cfg := config.Config{OTLPEndpoint: "x", Targets: map[string]config.Target{
		"agent": {Adapter: "shell"},
		"api":   {Adapter: "http"},
	}}
	eng, err := Build(cfg, nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tests := []struct {
		name   string
		target string
		wantAd string
		wantOK bool
	}{
		{name: "shell target", target: "agent", wantAd: "shell", wantOK: true},
		{name: "http target", target: "api", wantAd: "http", wantOK: true},
		{name: "unknown target", target: "ghost", wantAd: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAd, gotOK := eng.Adapter(tt.target)
			if gotAd != tt.wantAd || gotOK != tt.wantOK {
				t.Fatalf("Adapter(%q) = (%q, %v), want (%q, %v)", tt.target, gotAd, gotOK, tt.wantAd, tt.wantOK)
			}
		})
	}
}

func TestBuildLoadsShapePatterns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.yaml"),
		[]byte("name: p1\nclauses:\n  - exists: \"gen_ai.tool.name=search\"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := config.Config{OTLPEndpoint: "x", Expectations: dir}
	eng, err := Build(cfg, nil, nil) // Build does not call st/cor; nil is safe
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clauses, ok := eng.ShapePattern("p1")
	if !ok || len(clauses) != 1 {
		t.Fatalf("ShapePattern(p1) = (%v, %v), want 1 clause", clauses, ok)
	}
	if _, ok := eng.ShapePattern("missing"); ok {
		t.Errorf("ShapePattern(missing) = true, want false")
	}
}

func TestBuildNoExpectationsDir(t *testing.T) {
	cfg := config.Config{OTLPEndpoint: "x"} // Expectations == "" → zero patterns, no error
	eng, err := Build(cfg, nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := eng.ShapePattern("anything"); ok {
		t.Errorf("ShapePattern = true on empty engine, want false")
	}
}

func TestBuildRejectsMalformedPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"),
		[]byte("name: bad\nclauses:\n  - child: \"a=b\"\n"), 0o644); err != nil { // child without of
		t.Fatalf("write: %v", err)
	}
	cfg := config.Config{OTLPEndpoint: "x", Expectations: dir}
	_, err := Build(cfg, nil, nil)
	if err == nil {
		t.Fatalf("Build() = nil error, want error for malformed pattern")
	}
	// The load failure must be wrapped with context naming the directory,
	// not propagated raw (CLAUDE.md error-wrapping convention).
	if !strings.Contains(err.Error(), "load expectations from") || !strings.Contains(err.Error(), dir) {
		t.Errorf("Build() error = %q, want it wrapped with %q and dir %q", err, "load expectations from", dir)
	}
}

// TestBuildWiresSemanticJudge asserts the composition root resolves the judge
// backend and registers the "semantic" result matcher (US3-AC1/AC2/AC3, FR-005).
//
// No t.Parallel(): kept serial by convention (the seam registry is per-engine now,
// so a rebuild is independent — not a data-race requirement).
func TestBuildWiresSemanticJudge(t *testing.T) {
	tests := []struct {
		name            string
		backend         string
		votes           int
		wantErr         bool
		wantErrContains []string
	}{
		{
			name:            "unknown backend is a hard error",
			backend:         "definitely-not-a-backend",
			wantErr:         true,
			wantErrContains: []string{"unknown judge backend", "definitely-not-a-backend"},
		},
		{
			name:    "empty backend defaults to claude and wires semantic matcher",
			backend: "",
		},
		{
			name:    "explicit claude backend wires semantic matcher",
			backend: "claude",
		},
		{
			name:    "odd votes wires semantic matcher",
			backend: "claude",
			votes:   3,
		},
		{
			name:            "even votes is a hard error naming judge.votes and the value",
			backend:         "claude",
			votes:           4,
			wantErr:         true,
			wantErrContains: []string{"judge.votes", "4"},
		},
		{
			name:            "negative votes is a hard error naming judge.votes and the value",
			backend:         "claude",
			votes:           -1,
			wantErr:         true,
			wantErrContains: []string{"judge.votes", "-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{OTLPEndpoint: "x"}
			cfg.Judge.Backend = tt.backend
			cfg.Judge.Votes = tt.votes
			eng, err := Build(cfg, nil, nil) // Build does not call st/cor; nil is safe
			if (err != nil) != tt.wantErr {
				t.Fatalf("Build() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, want := range tt.wantErrContains {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("Build() err = %q, want substring %q", err, want)
					}
				}
				return
			}
			if _, ok := eng.reg.Matcher("semantic"); !ok {
				t.Errorf("eng.reg.Matcher(%q) = false after Build, want it wired", "semantic")
			}
		})
	}
}

// TestBuildRejectsUnregisteredAdapter proves D3/FR-005: adapter existence is
// validated at the composition root against the driver registry (the single
// runtime source of truth), not against a drift-prone load-time allowlist. A
// target whose adapter has no registered driver fails at Build (startup, before
// any scenario) with an error naming the target, the adapter, and the registered
// set. The built-in shell/http drivers Build registers must still Build cleanly.
//
// Substring (not exact) assertions: assert the "registered:" set CONTAINS the
// built-in drivers, tolerant of any future built-ins, without pinning the exact set.
//
// No t.Parallel(): kept serial by convention (the seam registry is per-engine now).
func TestBuildRejectsUnregisteredAdapter(t *testing.T) {
	tests := []struct {
		name            string
		targets         map[string]config.Target
		wantErr         bool
		wantErrContains []string
	}{
		{
			name:            "phantom adapter fails at build naming target, adapter, and registered set",
			targets:         map[string]config.Target{"svc": {Adapter: "telepathy"}},
			wantErr:         true,
			wantErrContains: []string{"svc", "telepathy", "registered:", "shell", "http"},
		},
		{
			name: "built-in shell and http adapters build cleanly",
			targets: map[string]config.Target{
				"agent": {Adapter: "shell"},
				"api":   {Adapter: "http"},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{OTLPEndpoint: "x", Targets: tt.targets}
			_, err := Build(cfg, nil, nil) // Build does not call st/cor; nil is safe
			if (err != nil) != tt.wantErr {
				t.Fatalf("Build() err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			for _, want := range tt.wantErrContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Build() err = %q, want substring %q", err, want)
				}
			}
		})
	}
}

// TestBuildRejectsUnregisteredAdapterDeterministicOrder pins the ordering half of
// D3: cfg.Targets is a map, so when more than one target has a phantom adapter the
// validation loop must surface the same error every run (sorted by target name),
// not whichever target Go's randomized map iteration visits first. Without the
// sort this test is flaky; with it, the alphabetically-first target always wins.
//
// No t.Parallel(): kept serial by convention (the seam registry is per-engine now).
func TestBuildRejectsUnregisteredAdapterDeterministicOrder(t *testing.T) {
	cfg := config.Config{OTLPEndpoint: "x", Targets: map[string]config.Target{
		"aaa": {Adapter: "aphantom"},
		"zzz": {Adapter: "zphantom"},
	}}
	// Run several times: a nondeterministic loop would eventually surface "zzz".
	for i := 0; i < 20; i++ {
		_, err := Build(cfg, nil, nil)
		if err == nil {
			t.Fatal("Build() err = nil, want phantom-adapter error")
		}
		if !strings.Contains(err.Error(), "aaa") || !strings.Contains(err.Error(), "aphantom") {
			t.Fatalf("Build() err = %q, want sorted-first target \"aaa\"/\"aphantom\"", err)
		}
		if strings.Contains(err.Error(), "zzz") || strings.Contains(err.Error(), "zphantom") {
			t.Fatalf("Build() err = %q, want sorted-first target only, not \"zzz\"", err)
		}
	}
}

func TestToPricing(t *testing.T) {
	t.Run("empty maps to nil", func(t *testing.T) {
		if got := toPricing(nil); got != nil {
			t.Fatalf("toPricing(nil) = %v, want nil", got)
		}
		if got := toPricing(config.Pricing{}); got != nil {
			t.Fatalf("toPricing(empty) = %v, want nil", got)
		}
	})
	t.Run("converts rates", func(t *testing.T) {
		in := config.Pricing{"m": {InputPerMTok: 3, OutputPerMTok: 15}}
		got := toPricing(in)
		r, ok := got["m"]
		if !ok {
			t.Fatalf("missing model m in %v", got)
		}
		if r.InputPerMTok != 3 || r.OutputPerMTok != 15 {
			t.Fatalf("rate = %+v, want {3 15}", r)
		}
	})
}
