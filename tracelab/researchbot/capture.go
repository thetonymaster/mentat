package researchbot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fixtureSpan struct {
	Name        string            `json:"name"`
	Op          string            `json:"op"`
	ParentIndex int               `json:"parentIndex"`
	Attrs       map[string]string `json:"attrs"`
	Status      string            `json:"status"`
}

type fixture struct {
	RunScenario string        `json:"runScenario"`
	Spans       []fixtureSpan `json:"spans"`
}

// CaptureFixture emits the plan to an in-memory exporter and renders a
// normalized, deterministic span-tree JSON. Volatile IDs and timestamps are
// dropped; parentage is expressed by final index; spans are ordered
// root-first then in export (start) order.
//
// Correctness note: Emit uses defer root.End(), so SimpleSpanProcessor
// exports children BEFORE the root. We therefore:
//  1. Collect all exported spans.
//  2. Separate the root (no valid parent span-id) from children.
//  3. Build the FINAL ordered slice: [root, child0, child1, …].
//  4. Build spanID → finalIndex from that ordered slice.
//  5. Compute each span's ParentIndex from that map.
//
// This is the only correct order for the computation. The alternative
// (build idxBySpanID first from export order, then swap root to 0 without
// recomputing) produces corrupt fixtures: children's ParentIndex still
// points at root's OLD export index (the last slot), which now holds a
// different child span.
func CaptureFixture(ctx context.Context, p *Plan) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("capture fixture: plan is nil")
	}

	exp := tracetest.NewInMemoryExporter()
	tp, err := NewTracerProvider(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("capture fixture: new tracer provider: %w", err)
	}
	if err := Emit(ctx, tp.Tracer("researchbot"), p); err != nil {
		return nil, fmt.Errorf("capture fixture: emit: %w", err)
	}
	// Collect spans BEFORE Shutdown: InMemoryExporter.Shutdown calls Reset(),
	// which clears all stored spans. SimpleSpanProcessor exports synchronously
	// on End(), so all spans are already present by this point.
	stubs := exp.GetSpans()
	if err := tp.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("capture fixture: shutdown: %w", err)
	}
	if len(stubs) == 0 {
		return nil, fmt.Errorf("capture fixture: no spans exported for scenario %q", p.Scenario)
	}

	// Step 1: find root span (parent span-id is invalid / zero).
	rootIdx := -1
	for i, s := range stubs {
		if !s.Parent.IsValid() {
			rootIdx = i
			break
		}
	}
	if rootIdx < 0 {
		return nil, fmt.Errorf("capture fixture: no root span found in %d exported spans for scenario %q",
			len(stubs), p.Scenario)
	}

	// Step 2: build final ordered slice — root first, then children in export order.
	ordered := make([]tracetest.SpanStub, 0, len(stubs))
	ordered = append(ordered, stubs[rootIdx])
	for i, s := range stubs {
		if i != rootIdx {
			ordered = append(ordered, s)
		}
	}

	// Step 3: spanID → finalIndex map, computed from the FINAL order.
	idxBySpanID := make(map[string]int, len(ordered))
	for i, s := range ordered {
		idxBySpanID[s.SpanContext.SpanID().String()] = i
	}

	// Step 4: build fixture spans using the final-index map for parentage.
	out := fixture{RunScenario: p.Scenario, Spans: make([]fixtureSpan, 0, len(ordered))}
	for _, s := range ordered {
		attrs := make(map[string]string, len(s.Attributes))
		for _, kv := range s.Attributes {
			attrs[string(kv.Key)] = kv.Value.String()
		}
		parent := -1
		if s.Parent.IsValid() {
			if pi, ok := idxBySpanID[s.Parent.SpanID().String()]; ok {
				parent = pi
			}
		}
		out.Spans = append(out.Spans, fixtureSpan{
			Name:        s.Name,
			Op:          attrs[AttrOp],
			ParentIndex: parent,
			Attrs:       attrs,
			Status:      s.Status.Code.String(),
		})
	}

	return json.MarshalIndent(out, "", "  ")
}

// WriteFixtures captures every scenario into dir/<scenario>.json.
// The file ends with a trailing newline for clean diffs.
func WriteFixtures(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write fixtures: mkdir %q: %w", dir, err)
	}
	for _, name := range ScenarioNames() {
		p, err := Scenario(name)
		if err != nil {
			return fmt.Errorf("write fixtures: scenario %q: %w", name, err)
		}
		data, err := CaptureFixture(context.Background(), p)
		if err != nil {
			return fmt.Errorf("write fixtures: capture %q: %w", name, err)
		}
		dest := filepath.Join(dir, name+".json")
		if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("write fixtures: write %q: %w", dest, err)
		}
	}
	return nil
}
