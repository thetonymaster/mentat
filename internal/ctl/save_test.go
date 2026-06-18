package ctl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/store"
	"github.com/thetonymaster/mentat/internal/trace"
)

// grandchildForest builds a 3-level tree: root → child → grandchild.
// IDs are numeric strings so parent resolution is deterministic.
func grandchildForest() *trace.Trace {
	root := &trace.Span{ID: "r", Name: "root-span",
		Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
	child := &trace.Span{ID: "c", ParentID: "r", Name: "child-span",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool}}
	grandchild := &trace.Span{ID: "g", ParentID: "c", Name: "grandchild-span",
		Attrs: map[string]string{genai.Op: genai.OpExecuteTool}}
	return &trace.Trace{
		RunID: "gc-run",
		Roots: []*trace.Span{root},
		Spans: []*trace.Span{root, child, grandchild},
	}
}

func TestWriteFixture(t *testing.T) {
	tests := []struct {
		name    string
		tr      func() *trace.Trace
		pathFn  func(dir string) string
		wantErr bool
		errSub  string
	}{
		{
			name: "round-trip via LoadFixture: 1 root + 2 execute_tool children",
			tr:   sampleForest,
			pathFn: func(dir string) string {
				return filepath.Join(dir, "happy.json")
			},
		},
		{
			name: "grandchild parentIndex points at child not root",
			tr:   grandchildForest,
			pathFn: func(dir string) string {
				return filepath.Join(dir, "gc.json")
			},
		},
		{
			name: "nil trace returns error with ctl: prefix",
			tr:   func() *trace.Trace { return nil },
			pathFn: func(dir string) string {
				return filepath.Join(dir, "nil.json")
			},
			wantErr: true,
			errSub:  "ctl:",
		},
		{
			name: "unwritable path returns error with ctl: prefix",
			tr:   sampleForest,
			pathFn: func(dir string) string {
				// Use a path under a read-only directory.
				ro := filepath.Join(dir, "readonly")
				if err := os.MkdirAll(ro, 0o555); err != nil {
					t.Fatalf("setup read-only dir: %v", err)
				}
				return filepath.Join(ro, "subdir", "out.json")
			},
			wantErr: true,
			errSub:  "ctl:",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tt.pathFn(dir)
			err := WriteFixture(tt.tr(), path)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("WriteFixture: expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("WriteFixture error %q does not contain %q", err.Error(), tt.errSub)
				}
				return
			}

			if err != nil {
				t.Fatalf("WriteFixture: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}

			switch tt.name {
			case "round-trip via LoadFixture: 1 root + 2 execute_tool children":
				loaded, lerr := store.LoadFixture(data)
				if lerr != nil {
					t.Fatalf("LoadFixture: %v", lerr)
				}
				if len(loaded.Roots) != 1 || loaded.Roots[0].Name != "invoke_agent researchbot" {
					t.Fatalf("round-trip root wrong: %+v", loaded.Roots)
				}
				if got := len(loaded.ByOp(genai.OpExecuteTool)); got != 2 {
					t.Fatalf("round-trip tool count: got %d, want 2", got)
				}

			case "grandchild parentIndex points at child not root":
				// Unmarshal raw JSON to verify parentIndex by position.
				var doc struct {
					Spans []struct {
						Name        string `json:"name"`
						ParentIndex int    `json:"parentIndex"`
					} `json:"spans"`
				}
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatalf("unmarshal grandchild fixture: %v", err)
				}
				// WriteFixture emits: [0]=root, [1]=child, [2]=grandchild
				// (roots first, then Spans in order, skipping already-emitted roots).
				if len(doc.Spans) != 3 {
					t.Fatalf("expected 3 spans, got %d", len(doc.Spans))
				}
				root := doc.Spans[0]
				child := doc.Spans[1]
				gc := doc.Spans[2]
				if root.ParentIndex != -1 {
					t.Fatalf("root parentIndex: got %d, want -1", root.ParentIndex)
				}
				if child.ParentIndex != 0 {
					t.Fatalf("child parentIndex: got %d, want 0 (root)", child.ParentIndex)
				}
				if gc.ParentIndex != 1 {
					t.Fatalf("grandchild parentIndex: got %d, want 1 (child), not 0 (root)", gc.ParentIndex)
				}
			}
		})
	}
}
