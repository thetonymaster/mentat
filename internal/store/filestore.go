package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// fixture mirrors Plan 1's tracelab capture format.
type fixture struct {
	RunScenario string `json:"runScenario"`
	Spans       []struct {
		Name        string            `json:"name"`
		ParentIndex int               `json:"parentIndex"`
		Attrs       map[string]string `json:"attrs"`
		Status      string            `json:"status"`
	} `json:"spans"`
}

// LoadFixture parses a captured fixture into a Trace forest.
// Parentage is by index; we store the parent span's Name as a synthetic ParentID.
// Span names are NOT globally unique within a fixture (happy.json repeats
// "chat claude-x"), so this is safe only because every child's parentIndex points
// at the unique root span. Deeper nesting would require index-based synthetic IDs.
func LoadFixture(data []byte) (*trace.Trace, error) {
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, fmt.Errorf("parse fixture: %w", err)
	}
	tr := &trace.Trace{}
	spans := make([]*trace.Span, len(fx.Spans))
	for i, fs := range fx.Spans {
		spans[i] = &trace.Span{Name: fs.Name, Status: fs.Status, Attrs: fs.Attrs}
	}
	for i, fs := range fx.Spans {
		if fs.ParentIndex >= 0 && fs.ParentIndex < len(spans) {
			spans[i].ParentID = spans[fs.ParentIndex].Name
		} else {
			tr.Roots = append(tr.Roots, spans[i])
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
