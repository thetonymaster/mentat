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
				// Point at an existing directory so os.WriteFile returns EISDIR
				// regardless of privilege level (avoids false-pass under root).
				outDir := filepath.Join(dir, "outdir")
				if err := os.MkdirAll(outDir, 0o755); err != nil {
					t.Fatalf("setup outdir: %v", err)
				}
				return outDir
			},
			wantErr: true,
			errSub:  "ctl:",
		},
		{
			// orphan is a non-root span (not in Roots) with a ParentID absent from the
			// forest — this is a genuine dangling reference and must still error.
			name: "dangling parent id on non-root span returns error containing references missing parent id",
			tr: func() *trace.Trace {
				root := &trace.Span{ID: "root-1", Name: "invoke_agent researchbot",
					Attrs: map[string]string{genai.Op: genai.OpInvokeAgent}}
				orphan := &trace.Span{ID: "orphan-2", ParentID: "nonexistent-id", Name: "execute_tool search",
					Attrs: map[string]string{genai.Op: genai.OpExecuteTool}}
				return &trace.Trace{
					RunID: "dangling-run",
					Roots: []*trace.Span{root},
					// orphan is in Spans but NOT in Roots — it is a non-root span.
					Spans: []*trace.Span{root, orphan},
				}
			},
			pathFn: func(dir string) string {
				return filepath.Join(dir, "dangling.json")
			},
			wantErr: true,
			errSub:  "references missing parent id",
		},
		{
			// A root span (present in tr.Roots) may retain a non-empty ParentID from a
			// different trace (cross-trace parent, Trace-is-a-forest invariant).  The
			// Tempo store does exactly this: it marks a span as a root when its parent
			// lives in a different trace but keeps the original ParentID.
			// WriteFixture must serialize such a root with parentIndex = -1, not error.
			name: "root span with cross-trace ParentID serializes as parentIndex -1 without error",
			tr: func() *trace.Trace {
				// crossRoot has a non-empty ParentID that points to a span in another
				// trace (absent from this forest).  The Tempo store would still put it
				// in tr.Roots because byID[sp.ParentID] == nil.
				crossRoot := &trace.Span{
					ID:       "cross-root-span",
					ParentID: "parent-in-other-trace",
					Name:     "invoke_agent sub-agent",
					Attrs:    map[string]string{genai.Op: genai.OpInvokeAgent},
				}
				child := &trace.Span{
					ID:       "child-of-cross-root",
					ParentID: "cross-root-span",
					Name:     "execute_tool search",
					Attrs:    map[string]string{genai.Op: genai.OpExecuteTool},
				}
				return &trace.Trace{
					RunID: "cross-trace-run",
					// crossRoot IS in Roots — it is a root span.
					Roots: []*trace.Span{crossRoot},
					Spans: []*trace.Span{crossRoot, child},
				}
			},
			pathFn: func(dir string) string {
				return filepath.Join(dir, "cross_root.json")
			},
			wantErr: false,
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

			case "root span with cross-trace ParentID serializes as parentIndex -1 without error":
				// Unmarshal raw JSON to verify the cross-trace root has parentIndex = -1.
				var doc struct {
					Spans []struct {
						Name        string `json:"name"`
						ParentIndex int    `json:"parentIndex"`
					} `json:"spans"`
				}
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatalf("unmarshal cross-root fixture: %v", err)
				}
				// WriteFixture emits: [0]=crossRoot (root), [1]=child
				if len(doc.Spans) != 2 {
					t.Fatalf("expected 2 spans, got %d", len(doc.Spans))
				}
				crossRoot := doc.Spans[0]
				child := doc.Spans[1]
				if crossRoot.ParentIndex != -1 {
					t.Fatalf("cross-trace root parentIndex: got %d, want -1", crossRoot.ParentIndex)
				}
				if child.ParentIndex != 0 {
					t.Fatalf("child of cross-trace root parentIndex: got %d, want 0", child.ParentIndex)
				}
			}
		})
	}
}
