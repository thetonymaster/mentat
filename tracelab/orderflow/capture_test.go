package orderflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureIsDeterministicAndCarriesServiceName(t *testing.T) {
	ctx := context.Background()
	a, err := Capture(ctx, "happy")
	if err != nil {
		t.Fatalf("capture 1: %v", err)
	}
	b, err := Capture(ctx, "happy")
	if err != nil {
		t.Fatalf("capture 2: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("capture not deterministic:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}

	var fx struct {
		RunScenario string `json:"runScenario"`
		Spans       []struct {
			Attrs map[string]string `json:"attrs"`
		} `json:"spans"`
	}
	if err := json.Unmarshal(a, &fx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fx.RunScenario != "happy" {
		t.Errorf("runScenario = %q, want happy", fx.RunScenario)
	}
	if len(fx.Spans) == 0 {
		t.Fatal("no spans captured")
	}
	// Every span must carry service.name (merged from its resource).
	for i, s := range fx.Spans {
		if s.Attrs["service.name"] == "" {
			t.Errorf("span[%d] missing service.name attr: %v", i, s.Attrs)
		}
	}
	// The first span (start-ordered) is the gateway server span.
	if got := fx.Spans[0].Attrs["service.name"]; got != ServiceGateway {
		t.Errorf("first span service.name = %q, want gateway", got)
	}
}

func TestWriteFixturesCreatesAllScenarios(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFixtures(dir); err != nil {
		t.Fatalf("WriteFixtures: %v", err)
	}
	for _, name := range Scenarios() {
		path := filepath.Join(dir, name+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing fixture file %q: %v", path, err)
			continue
		}
		// Verify it is valid JSON with our schema.
		var fx struct {
			RunScenario string `json:"runScenario"`
			Spans       []struct {
				Name string `json:"name"`
			} `json:"spans"`
		}
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Errorf("fixture %q: invalid JSON: %v", name, err)
			continue
		}
		if fx.RunScenario != name {
			t.Errorf("fixture %q: runScenario = %q, want %q", name, fx.RunScenario, name)
		}
		if len(fx.Spans) == 0 {
			t.Errorf("fixture %q: no spans", name)
		}
		// File must end with newline (clean diffs).
		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Errorf("fixture %q: missing trailing newline", name)
		}
	}
}

func TestWriteFixturesMkdirError(t *testing.T) {
	// Use a path where we cannot create a directory (a file blocks it).
	f, err := os.CreateTemp("", "capture-test-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	// f.Name() is a regular file; using it as the target dir must fail MkdirAll.
	err = WriteFixtures(filepath.Join(f.Name(), "subdir"))
	if err == nil {
		t.Fatal("WriteFixtures: expected error when dir cannot be created, got nil")
	}
}
