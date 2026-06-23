package expectations

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const goodPattern = `name: fanout-summarize
description: planner fans out then summarizes
clauses:
  - exists: "gen_ai.tool.name=search"
    count: ">=2"
  - child: "gen_ai.tool.name=summarize"
    of: "gen_ai.operation.name=chat"
  - fanout:
      parent: "gen_ai.operation.name=chat"
      child: "gen_ai.operation.name=execute_tool"
      count: ">=3"
`

func TestLoadGood(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "fanout.yaml", goodPattern)
	pats, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	clauses, ok := pats.Get("fanout-summarize")
	if !ok {
		t.Fatalf("pattern fanout-summarize not loaded; got %v", pats)
	}
	if len(clauses) != 3 {
		t.Errorf("got %d clauses, want 3", len(clauses))
	}
	if _, ok := pats.Get("nope"); ok {
		t.Errorf("Get(nope) = true, want false")
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, dir string }{
		{"empty string", ""},
		{"missing dir", filepath.Join(t.TempDir(), "does-not-exist")},
		{"empty dir", t.TempDir()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pats, err := Load(tt.dir)
			if err != nil {
				t.Fatalf("Load(%q): %v", tt.dir, err)
			}
			if len(pats) != 0 {
				t.Errorf("Load(%q) = %v, want empty", tt.dir, pats)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"malformed yaml", map[string]string{"a.yaml": "name: x\nclauses: [oops"}},
		{"unknown clause key", map[string]string{"a.yaml": "name: x\nclauses:\n  - exits: \"a=b\"\n"}},
		{"missing name", map[string]string{"a.yaml": "clauses:\n  - exists: \"a=b\"\n"}},
		{"empty clauses", map[string]string{"a.yaml": "name: x\nclauses: []\n"}},
		{"bad clause", map[string]string{"a.yaml": "name: x\nclauses:\n  - child: \"a=b\"\n"}}, // child without of
		{"duplicate name", map[string]string{
			"a.yaml": "name: dup\nclauses:\n  - exists: \"a=b\"\n",
			"b.yaml": "name: dup\nclauses:\n  - exists: \"c=d\"\n",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for n, b := range tt.files {
				write(t, dir, n, b)
			}
			if _, err := Load(dir); err == nil {
				t.Fatalf("Load() = nil error, want error")
			}
		})
	}
}
