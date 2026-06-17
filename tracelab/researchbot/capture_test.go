package researchbot

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestCaptureFixture(t *testing.T) {
	tests := []struct {
		name         string
		scenario     string
		wantRootName string
		wantToolName string // if non-empty, assert an execute_tool span with this AttrToolName
		wantChildren int    // number of non-root spans; must each have ParentIndex == 0
	}{
		{
			name:         "happy scenario deterministic and correct parentage",
			scenario:     "happy",
			wantRootName: "invoke_agent researchbot",
			wantToolName: "search",
			// happy.yaml: 3 chats + 3 tools = 6 children
			wantChildren: 6,
		},
		{
			name:         "wrong_order scenario correct parentage",
			scenario:     "wrong_order",
			wantRootName: "invoke_agent researchbot",
			wantToolName: "",
			wantChildren: -1, // do not assert count; just assert parentage
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p, err := Scenario(tt.scenario)
			if err != nil {
				t.Fatalf("scenario %q: %v", tt.scenario, err)
			}

			ctx := context.Background()

			// --- Determinism: two calls must yield byte-identical output ---
			a, err := CaptureFixture(ctx, p)
			if err != nil {
				t.Fatalf("first CaptureFixture: %v", err)
			}
			b, err := CaptureFixture(ctx, p)
			if err != nil {
				t.Fatalf("second CaptureFixture: %v", err)
			}
			if string(a) != string(b) {
				t.Fatal("CaptureFixture is not deterministic: two calls produced different output")
			}

			// --- Parse output ---
			var fx struct {
				RunScenario string `json:"runScenario"`
				Spans       []struct {
					Name        string            `json:"name"`
					Op          string            `json:"op"`
					ParentIndex int               `json:"parentIndex"`
					Attrs       map[string]string `json:"attrs"`
					Status      string            `json:"status"`
				} `json:"spans"`
			}
			if err := json.Unmarshal(a, &fx); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}

			if len(fx.Spans) == 0 {
				t.Fatal("fixture has no spans")
			}

			// --- Root is at index 0 with correct name and parentIndex == -1 ---
			root := fx.Spans[0]
			if root.Name != tt.wantRootName {
				t.Fatalf("spans[0].Name = %q, want %q", root.Name, tt.wantRootName)
			}
			if root.ParentIndex != -1 {
				t.Fatalf("spans[0].ParentIndex = %d, want -1 (root must have no parent)", root.ParentIndex)
			}
			if root.Op != OpInvokeAgent {
				t.Fatalf("spans[0].Op = %q, want %q", root.Op, OpInvokeAgent)
			}

			// --- Parentage: EVERY non-root span must have ParentIndex == 0 ---
			// This is the core assertion that catches the brief's bug: the buggy
			// swap moves root to index 0 without recomputing parentIndex, so
			// children still point at the root's OLD export index (last slot).
			for i, s := range fx.Spans[1:] {
				if s.ParentIndex != 0 {
					t.Fatalf("spans[%d] (%q) has ParentIndex=%d, want 0 (all children must point to root at index 0)",
						i+1, s.Name, s.ParentIndex)
				}
			}

			// --- Child count (when specified) ---
			if tt.wantChildren >= 0 {
				got := len(fx.Spans) - 1 // exclude root
				if got != tt.wantChildren {
					t.Fatalf("want %d child spans, got %d", tt.wantChildren, got)
				}
			}

			// --- execute_tool span with expected tool name exists ---
			if tt.wantToolName != "" {
				found := false
				for _, s := range fx.Spans {
					if s.Op == OpExecuteTool && s.Attrs[AttrToolName] == tt.wantToolName {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected an execute_tool span with %s=%q", AttrToolName, tt.wantToolName)
				}
			}
		})
	}
}

func TestWriteFixtures(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFixtures(dir); err != nil {
		t.Fatalf("WriteFixtures: %v", err)
	}

	// All scenario names must produce a file.
	names := ScenarioNames()
	if len(names) == 0 {
		t.Fatal("ScenarioNames returned empty list")
	}
	for _, name := range names {
		path := dir + "/" + name + ".json"
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing fixture for %q: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("fixture %q is empty", name)
		}
		// Validate JSON structure.
		var fx struct {
			RunScenario string        `json:"runScenario"`
			Spans       []interface{} `json:"spans"`
		}
		if err := json.Unmarshal(data, &fx); err != nil {
			t.Fatalf("fixture %q invalid JSON: %v", name, err)
		}
		if fx.RunScenario != name {
			t.Fatalf("fixture %q: runScenario=%q, want %q", name, fx.RunScenario, name)
		}
	}
}

func TestCaptureFixtureNilPlanReturnsError(t *testing.T) {
	ctx := context.Background()
	_, err := CaptureFixture(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil plan, got nil")
	}
}
