package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// fixture mirrors Plan 1's tracelab capture format.
//
// ParentIndex is *int so an omitted field is distinguishable from index 0. It is
// required on every span: nil (omitted) is a hard error rather than a silent
// child-of-span-0, and -1 is the only root marker.
type fixture struct {
	RunScenario string `json:"runScenario"`
	Spans       []struct {
		Name        string            `json:"name"`
		ParentIndex *int              `json:"parentIndex"`
		Attrs       map[string]string `json:"attrs"`
		Status      string            `json:"status"`
		Kind        string            `json:"kind"`
	} `json:"spans"`
}

// LoadFixture parses a captured fixture into a Trace forest.
// Parentage is by index; we store the parent span's Name as a synthetic ParentID.
// parentIndex is required on every span (nil => hard error), -1 marks a root, and
// any other value must be an in-range, non-self index. Fixtures may nest deeper
// than one level (e.g. orderflow/payment_decline.json parents a span at index 3),
// so after assigning parents we walk each span's parentIndex chain and reject any
// chain that fails to terminate at a -1 root (a cycle), which would otherwise
// yield a rootless non-forest. Span names are NOT globally unique within a
// fixture (happy.json repeats "chat claude-x"), so the name-based ParentID stays
// meaningful only because parentIndex — used here for validation — is unambiguous.
func LoadFixture(data []byte) (*trace.Trace, error) {
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	tr := &trace.Trace{}
	spans := make([]*trace.Span, len(fx.Spans))
	for i, fs := range fx.Spans {
		status, err := trace.NormalizeStatus(fs.Status)
		if err != nil {
			return nil, fmt.Errorf("filestore: span %d (%q): %w", i, fs.Name, err)
		}
		kind, err := trace.NormalizeKind(fs.Kind)
		if err != nil {
			return nil, fmt.Errorf("filestore: span %d (%q): %w", i, fs.Name, err)
		}
		spans[i] = &trace.Span{Name: fs.Name, Kind: kind, Status: status, Attrs: fs.Attrs}
	}
	for i, fs := range fx.Spans {
		if fs.ParentIndex == nil {
			// An omitted parentIndex must never silently attach the span to
			// span 0; -1 is the explicit root marker.
			return nil, fmt.Errorf("filestore: span %d (%q): parentIndex is required (use -1 for root)", i, fs.Name)
		}
		pi := *fs.ParentIndex
		switch {
		case pi == -1:
			// -1 is the only root marker.
			tr.Roots = append(tr.Roots, spans[i])
		case pi == i:
			return nil, fmt.Errorf("filestore: span %d (%q): parentIndex %d points to itself (use -1 for root)", i, fs.Name, pi)
		case pi < -1 || pi >= len(spans):
			// -1 is handled above, so only < -1 and >= len(spans) reach here.
			return nil, fmt.Errorf("filestore: span %d (%q): parentIndex %d out of range [0,%d) (use -1 for root)", i, fs.Name, pi, len(spans))
		default:
			spans[i].ParentID = spans[pi].Name
		}
	}
	// Reachability: every parentIndex chain must terminate at a -1 root. Each
	// ParentIndex here is non-nil and either -1 or a valid in-range non-self
	// index (validated above), so the walk is bounded and index-safe. Revisiting
	// an index means the chain loops and never reaches a root — a cycle.
	for i := range fx.Spans {
		visited := map[int]bool{i: true}
		for j := *fx.Spans[i].ParentIndex; j != -1; j = *fx.Spans[j].ParentIndex {
			if visited[j] {
				return nil, fmt.Errorf("filestore: span %d (%q): parentIndex chain does not terminate at a root (cycle detected)", i, fx.Spans[i].Name)
			}
			visited[j] = true
		}
	}
	tr.Spans = spans
	return tr, nil
}

// InMemStore serves preloaded traces by run id; for L1 unit tests, zero infra.
type InMemStore struct{ byRunID map[string]*trace.Trace }

func NewInMemStore(byRunID map[string]*trace.Trace) *InMemStore {
	return &InMemStore{byRunID: byRunID}
}

func (s *InMemStore) GetByID(_ context.Context, id string) (*trace.Trace, error) {
	if tr, ok := s.byRunID[id]; ok {
		return tr, nil
	}
	return nil, fmt.Errorf("inmem store: no trace %q", id)
}

// FetchPayload returns a deterministic canonical serialization of the stored
// forest — the hermetic definition of the feature-004 change-detection payload
// (spec Assumptions): content-identical forests yield byte-identical payloads.
// encoding/json guarantees the determinism: struct fields encode in declaration
// order and map keys (span Attrs) are sorted, so Go map iteration order never
// leaks into the bytes.
func (s *InMemStore) FetchPayload(_ context.Context, id string) ([]byte, error) {
	tr, ok := s.byRunID[id]
	if !ok {
		return nil, fmt.Errorf("inmem store: no trace %q", id)
	}
	payload, err := json.Marshal(tr)
	if err != nil {
		return nil, fmt.Errorf("inmem store: canonical serialization of trace %q: %w", id, err)
	}
	return payload, nil
}

// DecodePayload returns the stored forest the payload canonically serializes.
// The store keeps decoded forests (there is no wire format to parse), so the
// lookup IS the decode; an unknown id is a hard error, never a silent nil.
func (s *InMemStore) DecodePayload(id string, _ []byte) (*trace.Trace, error) {
	tr, ok := s.byRunID[id]
	if !ok {
		return nil, fmt.Errorf("inmem store: no trace %q", id)
	}
	return tr, nil
}

func (s *InMemStore) Query(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	if q.Tag != "test.run.id" {
		return nil, fmt.Errorf("inmem store: only test.run.id queries supported, got %q", q.Tag)
	}
	if _, ok := s.byRunID[q.Value]; ok {
		return []core.TraceRef{{TraceID: q.Value}}, nil
	}
	return nil, nil
}

func (s *InMemStore) Caps() core.StoreCaps { return core.StoreCaps{StructuralQuery: false} }
