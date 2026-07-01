package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/registry"
)

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
// No t.Parallel(): Build mutates the registry's package-global maps; running the
// rows concurrently would data-race those writes.
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
			_, err := Build(cfg, nil, nil) // Build does not call st/cor; nil is safe
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
			if _, ok := registry.Matcher("semantic"); !ok {
				t.Errorf("registry.Matcher(%q) = false after Build, want it wired", "semantic")
			}
		})
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
