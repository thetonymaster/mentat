package orderflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// volatileAttrs lists OTel HTTP semconv attributes whose values depend on
// ephemeral OS state (port numbers, peer addresses) and therefore must not
// appear in golden fixtures.
var volatileAttrs = map[string]bool{
	"network.peer.port":    true,
	"server.port":          true,
	"network.peer.address": true,
	"client.address":       true,
}

// fixtureSpan / fixture mirror the schema the Mentat store loads
// (internal/store/filestore.go). Volatile IDs and timestamps are dropped;
// parentage is by final index; service.name (a resource attr) is merged into
// attrs so sequence(service)/budgets can read it.
type fixtureSpan struct {
	Name        string            `json:"name"`
	ParentIndex int               `json:"parentIndex"`
	Attrs       map[string]string `json:"attrs"`
	Status      string            `json:"status"`
}

type fixture struct {
	RunScenario string        `json:"runScenario"`
	Spans       []fixtureSpan `json:"spans"`
}

// resourceServiceName extracts service.name from a span's resource.
func resourceServiceName(res *resource.Resource) string {
	for _, kv := range res.Attributes() {
		if kv.Key == semconv.ServiceNameKey {
			return kv.Value.AsString()
		}
	}
	return ""
}

// Capture drives one scenario through an ephemeral in-process system into an
// in-memory exporter and renders a normalized, deterministic span-forest JSON.
// Spans are ordered by start time so the fixture order encodes service-call
// order — sequence(service) relies on this because fixtures carry no timestamps.
func Capture(ctx context.Context, scenario string) ([]byte, error) {
	exp := tracetest.NewInMemoryExporter()
	sys, topo, err := StartInProcess(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("capture %q: start: %w", scenario, err)
	}
	if _, _, err := sys.Drive(ctx, topo, "capture-"+scenario, scenario); err != nil {
		_ = sys.Shutdown(ctx)
		return nil, fmt.Errorf("capture %q: drive: %w", scenario, err)
	}
	stubs, err := stableSnapshots(ctx, exp)
	if err != nil {
		_ = sys.Shutdown(ctx)
		return nil, fmt.Errorf("capture %q: wait spans: %w", scenario, err)
	}
	if err := sys.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("capture %q: shutdown: %w", scenario, err)
	}
	if len(stubs) == 0 {
		return nil, fmt.Errorf("capture %q: no spans exported", scenario)
	}

	sort.SliceStable(stubs, func(i, j int) bool {
		ti, tj := stubs[i].StartTime, stubs[j].StartTime
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		// Tiebreakers must be stable across runs, so they use span content
		// (service.name, name, status) — never SpanID, which is regenerated
		// per run and would reorder fixtures on a start-time collision.
		if si, sj := resourceServiceName(stubs[i].Resource), resourceServiceName(stubs[j].Resource); si != sj {
			return si < sj
		}
		if stubs[i].Name != stubs[j].Name {
			return stubs[i].Name < stubs[j].Name
		}
		return stubs[i].Status.Code.String() < stubs[j].Status.Code.String()
	})

	idx := make(map[string]int, len(stubs))
	for i, s := range stubs {
		idx[s.SpanContext.SpanID().String()] = i
	}

	out := fixture{RunScenario: scenario, Spans: make([]fixtureSpan, 0, len(stubs))}
	for _, s := range stubs {
		attrs := make(map[string]string, len(s.Attributes)+1)
		for _, kv := range s.Attributes {
			if !volatileAttrs[string(kv.Key)] {
				attrs[string(kv.Key)] = kv.Value.String()
			}
		}
		if sn := resourceServiceName(s.Resource); sn != "" {
			attrs["service.name"] = sn
		}
		parent := -1
		if s.Parent.IsValid() {
			if pi, ok := idx[s.Parent.SpanID().String()]; ok {
				parent = pi
			}
		}
		out.Spans = append(out.Spans, fixtureSpan{
			Name:        s.Name,
			ParentIndex: parent,
			Attrs:       attrs,
			Status:      s.Status.Code.String(),
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// stableSnapshots polls until the exported span count is stable, since otelhttp
// server spans end on their own goroutine after the handler returns. It honors
// ctx so a canceled caller stops waiting immediately instead of sleeping ~2s.
func stableSnapshots(ctx context.Context, exp *tracetest.InMemoryExporter) ([]tracetest.SpanStub, error) {
	last, stable := -1, 0
	for i := 0; i < 200; i++ { // up to ~2s
		n := len(exp.GetSpans())
		if n > 0 && n == last {
			if stable++; stable >= 3 {
				break
			}
		} else {
			stable = 0
		}
		last = n
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return exp.GetSpans(), nil
}

// WriteFixtures captures every scenario into dir/<scenario>.json with a trailing
// newline for clean diffs.
func WriteFixtures(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write fixtures: mkdir %q: %w", dir, err)
	}
	for _, name := range Scenarios() {
		data, err := Capture(context.Background(), name)
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
