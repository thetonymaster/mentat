package kafkaecho

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/thetonymaster/mentat"
)

// Store is a toy mentat.TraceStore serving the forest the Driver published, keyed
// on the same engine-injected run id. It stands in for a real trace backend
// (Tempo): Query is the analogue of a `test.run.id` tag query, and
// FetchPayload/DecodePayload the analogue of fetching and parsing a stored trace.
type Store struct {
	bus *Bus
}

// NewStore returns a Store serving traces from bus (shared with a Driver).
func NewStore(bus *Bus) *Store { return &Store{bus: bus} }

// FetchPayload returns the deterministic wire bytes for a run id — a canonical
// JSON serialization of the stored forest. It MUST be byte-identical across calls
// for the same trace so the correlator's stability poll converges (json.Marshal
// sorts map keys, so a fixed forest yields fixed bytes). An absent id is a hard
// error, never (nil, nil) (Constitution IV).
func (s *Store) FetchPayload(_ context.Context, id string) ([]byte, error) {
	tr, ok := s.bus.Lookup(id)
	if !ok {
		return nil, fmt.Errorf("kafkaecho store: no trace for run %q", id)
	}
	payload, err := json.Marshal(tr)
	if err != nil {
		return nil, fmt.Errorf("kafkaecho store: marshal trace for run %q: %w", id, err)
	}
	return payload, nil
}

// DecodePayload parses bytes previously returned by FetchPayload for the same id
// back into a forest. It does not consult the bus: the hashed and decoded bytes are
// the same fetch, so there is no partial-evidence window.
func (s *Store) DecodePayload(id string, payload []byte) (*mentat.Trace, error) {
	var tr mentat.Trace
	if err := json.Unmarshal(payload, &tr); err != nil {
		return nil, fmt.Errorf("kafkaecho store: decode trace for run %q: %w", id, err)
	}
	return &tr, nil
}

// Query resolves a run id to at most one trace ref. Correlation is tag-first: the
// engine queries test.run.id=<runID>, and this store keys on that id. A missing
// trace returns an empty ref set (not an error): "not yet ingested" is a normal
// poll state the correlator retries, distinct from FetchPayload's hard miss.
func (s *Store) Query(_ context.Context, q mentat.TraceQuery) ([]mentat.TraceRef, error) {
	if _, ok := s.bus.Lookup(q.Value); !ok {
		return nil, nil
	}
	return []mentat.TraceRef{{TraceID: q.Value}}, nil
}

// Caps advertises no structural-query support: this store resolves by run id only.
func (s *Store) Caps() mentat.StoreCaps { return mentat.StoreCaps{} }
