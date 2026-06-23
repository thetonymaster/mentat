package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
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
	if _, err := Build(cfg, nil, nil); err == nil {
		t.Fatalf("Build() = nil error, want error for malformed pattern")
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
