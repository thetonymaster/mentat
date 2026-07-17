package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// DecodePayload decodes the supplied payload bytes — previously returned by
// FetchPayload for the same id — into a Trace forest. It decodes THESE bytes,
// never the current store state: the correlator hashes the fetched payload and
// decodes the same bytes, so if the stored forest mutates between fetch and
// decode, the decode must still return the snapshot the hash described.
// An unknown id remains a hard error, never a silent nil.
//
// In the stored forest Roots entries alias the same *Span objects present in
// Spans, so json.Marshal duplicates root spans in the payload and a plain
// unmarshal yields Roots pointing at distinct objects. The aliasing is rebuilt
// by span ID; a root whose ID is absent from Spans is a hard error
// (constitution IV — no silent fallback).
func (s *InMemStore) DecodePayload(id string, payload []byte) (*trace.Trace, error) {
	if _, ok := s.byRunID[id]; !ok {
		return nil, fmt.Errorf("inmem store: no trace %q", id)
	}
	var tr trace.Trace
	if err := json.Unmarshal(payload, &tr); err != nil {
		return nil, fmt.Errorf("inmem store: decode payload for trace %q: %w", id, err)
	}
	byID := make(map[string]*trace.Span, len(tr.Spans))
	for _, sp := range tr.Spans {
		byID[sp.ID] = sp
	}
	for i, root := range tr.Roots {
		sp, ok := byID[root.ID]
		if !ok {
			return nil, fmt.Errorf("inmem store: decode payload for trace %q: root span %q (id %q) not present in spans", id, root.Name, root.ID)
		}
		tr.Roots[i] = sp
	}
	return &tr, nil
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

// FileStore is a directory-backed TraceStore for OFFLINE replay (US5). It serves
// captured fixtures — the LoadFixture / ctl.WriteFixture format — from a directory,
// keyed by each fixture's recorded `runScenario` field (the run id a saved run
// carries). It closes the write-only fixture loop: a saved run replays with no
// Tempo, no Docker, no network — just local file reads.
//
// It targets the PINNED replay path (`mentatctl agent replay <id>` / `--last`),
// which SUPPLIES the run id rather than injecting a fresh one, so Query(run id)
// matches the fixture's recorded runScenario. The live `mentat run` path injects a
// FRESH run id per run, which no recorded fixture carries; its Query then returns
// the descriptive not-found error below (a loud miss, never a wrong trace).
type FileStore struct {
	dir string
	// byRunID maps a recorded runScenario to its fixture entries. More than one
	// entry for an id is an ambiguity surfaced (naming both files) only when that id
	// is looked up — never silently resolved to an arbitrary sample (constitution IV).
	byRunID map[string][]fileEntry
}

type fileEntry struct {
	path    string
	payload []byte
}

// NewFileStore scans dir for *.json fixtures, validates each via LoadFixture, and
// indexes them by recorded runScenario. A dir that cannot be read, a fixture that
// fails to parse, or a fixture with no runScenario is a hard error at construction
// (constitution IV — a corrupt replay corpus fails loudly at build, not with a
// wrong verdict later). Two fixtures sharing a runScenario are NOT rejected here —
// the ambiguity is surfaced only when that id is queried (naming both files).
func NewFileStore(dir string) (*FileStore, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("file store: read dir %q: %w", dir, err)
	}
	byRunID := map[string][]fileEntry{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("file store: read fixture %q: %w", path, err)
		}
		// Validate the fixture (parentage, cycles, canonical vocabulary) so a broken
		// fixture fails at build, not at replay.
		if _, err := LoadFixture(payload); err != nil {
			return nil, fmt.Errorf("file store: parse fixture %q: %w", path, err)
		}
		var fx fixture
		if err := json.Unmarshal(payload, &fx); err != nil {
			return nil, fmt.Errorf("file store: read runScenario from %q: %w", path, err)
		}
		if strings.TrimSpace(fx.RunScenario) == "" {
			return nil, fmt.Errorf("file store: fixture %q has no runScenario (cannot key it by run id)", path)
		}
		byRunID[fx.RunScenario] = append(byRunID[fx.RunScenario], fileEntry{path: path, payload: payload})
	}
	return &FileStore{dir: dir, byRunID: byRunID}, nil
}

// lookup resolves a run id to its single fixture entry. Zero matches → a
// descriptive not-found error naming BOTH the dir and the id (unlike InMemStore's
// (nil,nil): a replay against a file store is a deliberate "this exact id lives
// here", so its absence is an error, not an empty result). More than one match →
// an ambiguity error naming every candidate file (constitution IV — never guess
// which sample the author meant).
func (s *FileStore) lookup(id string) (fileEntry, error) {
	ents := s.byRunID[id]
	switch len(ents) {
	case 0:
		return fileEntry{}, fmt.Errorf("file store: no fixture for run %q in dir %q", id, s.dir)
	case 1:
		return ents[0], nil
	default:
		paths := make([]string, len(ents))
		for i, e := range ents {
			paths[i] = e.path
		}
		return fileEntry{}, fmt.Errorf("file store: run %q is ambiguous in dir %q: %s", id, s.dir, strings.Join(paths, ", "))
	}
}

// Query resolves a test.run.id tag query against the recorded fixtures. A hit
// returns exactly one ref (TraceID == the run id); absent/ambiguous is the hard
// error lookup names. Only test.run.id is supported — the file store is a
// tag-first replay store, not a structural query engine (Caps.StructuralQuery=false).
func (s *FileStore) Query(_ context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	if q.Tag != "test.run.id" {
		return nil, fmt.Errorf("file store: only test.run.id queries supported, got %q", q.Tag)
	}
	if _, err := s.lookup(q.Value); err != nil {
		return nil, err
	}
	return []core.TraceRef{{TraceID: q.Value}}, nil
}

// FetchPayload returns the recorded fixture bytes for id — the change-detection
// signal of the stability poll. The bytes are the file's own content (deterministic
// across fetches), so repeated fetches are byte-identical. Unknown/ambiguous id is
// the hard error lookup names, never (nil, nil).
func (s *FileStore) FetchPayload(_ context.Context, id string) ([]byte, error) {
	ent, err := s.lookup(id)
	if err != nil {
		return nil, err
	}
	return ent.payload, nil
}

// DecodePayload decodes payload bytes previously returned by FetchPayload — the
// raw fixture bytes — back into a Trace forest via LoadFixture (feature-002
// canonical status/kind vocabulary). It decodes THESE bytes (the fetch/decode
// contract): the id names the run for the error message only. Malformed bytes are
// a hard, wrapped error (constitution IV).
func (s *FileStore) DecodePayload(id string, payload []byte) (*trace.Trace, error) {
	tr, err := LoadFixture(payload)
	if err != nil {
		return nil, fmt.Errorf("file store: decode payload for run %q: %w", id, err)
	}
	return tr, nil
}

// GetByID loads the fixture recorded for id and returns its Trace forest (feature-002
// canonical vocabulary via LoadFixture). Unknown/ambiguous id is the same hard error
// as Query. Mirrors InMemStore.GetByID for symmetric L1-store use; the TraceStore
// interface itself resolves via FetchPayload + DecodePayload, which this composes.
func (s *FileStore) GetByID(_ context.Context, id string) (*trace.Trace, error) {
	ent, err := s.lookup(id)
	if err != nil {
		return nil, err
	}
	tr, err := LoadFixture(ent.payload)
	if err != nil {
		return nil, fmt.Errorf("file store: load fixture %q: %w", ent.path, err)
	}
	return tr, nil
}

func (s *FileStore) Caps() core.StoreCaps { return core.StoreCaps{StructuralQuery: false} }
