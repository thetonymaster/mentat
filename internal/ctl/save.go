package ctl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

type fixtureSpan struct {
	Name        string            `json:"name"`
	Op          string            `json:"op"`
	ParentIndex int               `json:"parentIndex"`
	Attrs       map[string]string `json:"attrs"`
	Status      string            `json:"status"`
}

type fixtureDoc struct {
	RunScenario string        `json:"runScenario"`
	Spans       []fixtureSpan `json:"spans"`
}

// WriteFixture serializes a live trace forest into the Plan-1 fixture schema so it
// can feed L1 unit tests via store.LoadFixture. Parentage is by index; roots first.
func WriteFixture(tr *trace.Trace, path string) error {
	// Build ordered slice: roots first, then non-roots in Spans order.
	rootSet := make(map[*trace.Span]bool, len(tr.Roots))
	for _, r := range tr.Roots {
		rootSet[r] = true
	}
	ordered := make([]*trace.Span, 0, len(tr.Spans))
	ordered = append(ordered, tr.Roots...)
	for _, s := range tr.Spans {
		if !rootSet[s] {
			ordered = append(ordered, s)
		}
	}

	// Map span ID → index for parent resolution.
	idx := make(map[string]int, len(ordered))
	for i, s := range ordered {
		idx[s.ID] = i
	}

	out := fixtureDoc{RunScenario: tr.RunID}
	for _, s := range ordered {
		parent := -1
		if s.ParentID != "" {
			if pi, ok := idx[s.ParentID]; ok {
				parent = pi
			}
		}
		out.Spans = append(out.Spans, fixtureSpan{
			Name:        s.Name,
			Op:          s.Attr(genai.Op),
			ParentIndex: parent,
			Attrs:       s.Attrs,
			Status:      s.Status,
		})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("ctl: marshal fixture: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ctl: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("ctl: write fixture %s: %w", path, err)
	}
	return nil
}
