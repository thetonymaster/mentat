package store

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// TestLoadFixtureNormalizesStatusAndKind pins R1/R2 at the fixture boundary: the
// loader must accept canonical and OTLP status spellings, default omitted
// status/kind, decode the optional kind field, and hard-error on an unknown
// spelling naming the span and the offending value.
func TestLoadFixtureNormalizesStatusAndKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		data       string
		wantStatus string
		wantKind   string
		wantErr    bool
		wantSubs   []string
	}{
		{
			name:       "canonical status Error loads",
			data:       `{"spans":[{"name":"root","parentIndex":-1,"status":"Error"}]}`,
			wantStatus: trace.StatusError,
			wantKind:   trace.KindUnspecified,
		},
		{
			name:       "OTLP spelling STATUS_CODE_ERROR loads to Error",
			data:       `{"spans":[{"name":"root","parentIndex":-1,"status":"STATUS_CODE_ERROR"}]}`,
			wantStatus: trace.StatusError,
		},
		{
			name:       "omitted status defaults to Unset",
			data:       `{"spans":[{"name":"root","parentIndex":-1}]}`,
			wantStatus: trace.StatusUnset,
		},
		{
			name:       "optional kind SPAN_KIND_SERVER loads",
			data:       `{"spans":[{"name":"root","parentIndex":-1,"kind":"SPAN_KIND_SERVER"}]}`,
			wantStatus: trace.StatusUnset,
			wantKind:   trace.KindServer,
		},
		{
			name:     "unknown status spelling errors naming span and value",
			data:     `{"spans":[{"name":"checkout","parentIndex":-1,"status":"STATUS_CODE_BANANA"}]}`,
			wantErr:  true,
			wantSubs: []string{"checkout", "STATUS_CODE_BANANA"},
		},
		{
			name:     "unknown kind errors naming span and value",
			data:     `{"spans":[{"name":"checkout","parentIndex":-1,"kind":"SPAN_KIND_BANANA"}]}`,
			wantErr:  true,
			wantSubs: []string{"checkout", "SPAN_KIND_BANANA"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, err := LoadFixture([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range tt.wantSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				return
			}
			if len(tr.Spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(tr.Spans))
			}
			if tr.Spans[0].Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", tr.Spans[0].Status, tt.wantStatus)
			}
			if tr.Spans[0].Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", tr.Spans[0].Kind, tt.wantKind)
			}
		})
	}
}

// TestLoadFixtureRejectsMalformedParentIndex pins A7: parentIndex is validated so
// a typo'd, self-referential, or omitted index is a hard load error naming the
// span index/name (and the offending value where one exists), never a silent
// root or a silent child-of-span-0. parentIndex is REQUIRED on every span; -1 is
// the only root marker. After parentage is assigned the loader walks each span's
// parentIndex chain and rejects any chain that fails to terminate at a -1 root
// (a cycle), which would otherwise yield a rootless non-forest.
func TestLoadFixtureRejectsMalformedParentIndex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		data      string
		wantErr   bool
		wantSubs  []string
		wantRoots []string          // expected root span names (valid case)
		wantPar   map[string]string // span name -> expected ParentID (valid case)
	}{
		{
			name:      "valid root and parent load",
			data:      `{"spans":[{"name":"root","parentIndex":-1},{"name":"child","parentIndex":0}]}`,
			wantRoots: []string{"root"},
			wantPar:   map[string]string{"child": "root"},
		},
		{
			name:      "valid depth>1 parentage loads (root -> child -> grandchild)",
			data:      `{"spans":[{"name":"root","parentIndex":-1},{"name":"child","parentIndex":0},{"name":"grandchild","parentIndex":1}]}`,
			wantRoots: []string{"root"},
			wantPar:   map[string]string{"child": "root", "grandchild": "child"},
		},
		{
			name:     "out-of-range parentIndex errors naming span and value",
			data:     `{"spans":[{"name":"root","parentIndex":-1},{"name":"checkout","parentIndex":99}]}`,
			wantErr:  true,
			wantSubs: []string{"span 1", "checkout", "99"},
		},
		{
			name:     "below-range parentIndex (< -1) errors naming span and value",
			data:     `{"spans":[{"name":"root","parentIndex":-1},{"name":"checkout","parentIndex":-2}]}`,
			wantErr:  true,
			wantSubs: []string{"span 1", "checkout", "-2"},
		},
		{
			name:     "self-parent parentIndex errors",
			data:     `{"spans":[{"name":"root","parentIndex":-1},{"name":"loop","parentIndex":1}]}`,
			wantErr:  true,
			wantSubs: []string{"span 1", "loop", "itself"},
		},
		{
			name:     "omitted parentIndex on span 0 errors as required",
			data:     `{"spans":[{"name":"orphan"}]}`,
			wantErr:  true,
			wantSubs: []string{"span 0", "orphan", "required"},
		},
		{
			name:     "omitted parentIndex on child span errors as required (not silent child-of-0)",
			data:     `{"spans":[{"name":"root","parentIndex":-1},{"name":"child"}]}`,
			wantErr:  true,
			wantSubs: []string{"span 1", "child", "required"},
		},
		{
			name:     "indirect two-node cycle errors",
			data:     `{"spans":[{"name":"a","parentIndex":1},{"name":"b","parentIndex":0}]}`,
			wantErr:  true,
			wantSubs: []string{"span 0", "a", "cycle"},
		},
		{
			name:     "three-node cycle errors",
			data:     `{"spans":[{"name":"a","parentIndex":1},{"name":"b","parentIndex":2},{"name":"c","parentIndex":0}]}`,
			wantErr:  true,
			wantSubs: []string{"cycle"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, err := LoadFixture([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range tt.wantSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				return
			}
			if len(tr.Roots) != len(tt.wantRoots) {
				t.Fatalf("roots len = %d, want %d (%v)", len(tr.Roots), len(tt.wantRoots), tt.wantRoots)
			}
			for i, name := range tt.wantRoots {
				if tr.Roots[i].Name != name {
					t.Fatalf("root %d = %q, want %q", i, tr.Roots[i].Name, name)
				}
			}
			for _, sp := range tr.Spans {
				if want, ok := tt.wantPar[sp.Name]; ok && sp.ParentID != want {
					t.Fatalf("span %q ParentID = %q, want %q", sp.Name, sp.ParentID, want)
				}
			}
		})
	}
}

func TestLoadFixtureBuildsForestFromPlan1Golden(t *testing.T) {
	data, err := os.ReadFile("../../testdata/traces/researchbot/happy.json")
	if err != nil {
		t.Fatalf("read fixture (run Plan 1 capture first): %v", err)
	}
	tr, err := LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(tr.Roots) != 1 || tr.Roots[0].Name != "invoke_agent researchbot" {
		t.Fatalf("root wrong: %+v", tr.Roots)
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if len(tools) < 3 {
		t.Fatalf("want >=3 tool spans, got %d", len(tools))
	}
}

func TestInMemStoreResolvesByRunID(t *testing.T) {
	data, err := os.ReadFile("../../testdata/traces/researchbot/happy.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tr, err := LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	tr.RunID = "r1"
	st := NewInMemStore(map[string]*trace.Trace{"r1": tr})
	refs, err := st.Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "r1"})
	if err != nil || len(refs) != 1 {
		t.Fatalf("Query: refs=%v err=%v", refs, err)
	}
}

func TestInMemStoreGetByID(t *testing.T) {
	data, err := os.ReadFile("../../testdata/traces/researchbot/happy.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tr, err := LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	tr.RunID = "run-abc"
	st := NewInMemStore(map[string]*trace.Trace{"run-abc": tr})

	tests := []struct {
		name    string
		id      string
		wantNil bool
		wantErr bool
	}{
		{name: "known id returns trace", id: "run-abc", wantNil: false, wantErr: false},
		{name: "unknown id returns error", id: "missing", wantNil: true, wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := st.GetByID(context.Background(), tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if (got == nil) != tt.wantNil {
				t.Fatalf("got=%v wantNil=%v", got, tt.wantNil)
			}
		})
	}
}

// inmemSpan builds a span with a multi-key Attrs map. Multiple keys matter: Go
// map iteration order is randomized, so these tests prove the canonical
// serialization sorts keys instead of leaking iteration order into the bytes.
func inmemSpan(name, status string, extra map[string]string) *trace.Span {
	attrs := map[string]string{
		"service.name":          "researchbot",
		"test.run.id":           "run-1",
		"gen_ai.operation.name": "invoke_agent",
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return &trace.Span{Name: name, Status: status, Attrs: attrs}
}

func inmemForest(extra map[string]string) *trace.Trace {
	root := inmemSpan("invoke_agent researchbot", trace.StatusOk, extra)
	child := inmemSpan("execute_tool search", trace.StatusOk, extra)
	child.ParentID = root.Name
	return &trace.Trace{RunID: "run-1", Roots: []*trace.Span{root}, Spans: []*trace.Span{root, child}}
}

// TestInMemStoreFetchPayloadDeterministicCanonicalSerialization pins the
// feature-004 hermetic payload definition (spec Assumptions, research R1): a
// store with no wire payload derives its payload as a deterministic canonical
// serialization of the stored forest — content-identical ⇒ byte-identical
// (across repeated fetches AND across independently-constructed stores), any
// content change ⇒ different bytes, unknown id ⇒ hard error.
func TestInMemStoreFetchPayloadDeterministicCanonicalSerialization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		a, b      *trace.Trace // payloads compared between two independent stores
		wantEqual bool
	}{
		{
			name:      "content-identical forests serialize byte-identically",
			a:         inmemForest(nil),
			b:         inmemForest(nil),
			wantEqual: true,
		},
		{
			name:      "one changed attr value changes the bytes",
			a:         inmemForest(nil),
			b:         inmemForest(map[string]string{"gen_ai.operation.name": "execute_tool"}),
			wantEqual: false,
		},
		{
			name: "an added span changes the bytes",
			a:    inmemForest(nil),
			b: func() *trace.Trace {
				tr := inmemForest(nil)
				tr.Spans = append(tr.Spans, inmemSpan("extra", trace.StatusOk, nil))
				return tr
			}(),
			wantEqual: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stA := NewInMemStore(map[string]*trace.Trace{"run-1": tt.a})
			stB := NewInMemStore(map[string]*trace.Trace{"run-1": tt.b})

			pA, err := stA.FetchPayload(context.Background(), "run-1")
			if err != nil {
				t.Fatalf("FetchPayload A: %v", err)
			}
			pB, err := stB.FetchPayload(context.Background(), "run-1")
			if err != nil {
				t.Fatalf("FetchPayload B: %v", err)
			}
			if gotEqual := string(pA) == string(pB); gotEqual != tt.wantEqual {
				t.Fatalf("payload equality = %v, want %v\nA: %s\nB: %s", gotEqual, tt.wantEqual, pA, pB)
			}

			// Repeated fetches of the same store must be byte-identical (the
			// stability poll compares round over round).
			pA2, err := stA.FetchPayload(context.Background(), "run-1")
			if err != nil {
				t.Fatalf("FetchPayload A (2nd): %v", err)
			}
			if string(pA) != string(pA2) {
				t.Fatalf("repeated FetchPayload not byte-identical:\n1st: %s\n2nd: %s", pA, pA2)
			}
		})
	}

	t.Run("unknown id is a hard error", func(t *testing.T) {
		t.Parallel()
		st := NewInMemStore(map[string]*trace.Trace{"run-1": inmemForest(nil)})
		_, err := st.FetchPayload(context.Background(), "missing")
		if err == nil {
			t.Fatal("want error for unknown id, got nil")
		}
		if !strings.Contains(err.Error(), `"missing"`) {
			t.Fatalf("error does not name the id: %q", err.Error())
		}
	})
}

// inmemForestWithIDs builds a multi-root forest (invariant §2 — a run may span
// more than one root trace) with span IDs and UTC timestamps: the IDs are what
// DecodePayload uses to rebuild the Roots→Spans aliasing after the JSON
// round-trip, and time.Date(..., time.UTC) times survive RFC 3339 marshalling
// with .Equal semantics.
func inmemForestWithIDs() *trace.Trace {
	start := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	r1 := &trace.Span{
		ID: "s1", Name: "invoke_agent researchbot", Kind: trace.KindServer,
		Status: trace.StatusOk, Start: start, End: start.Add(2 * time.Second),
		Attrs: map[string]string{"test.run.id": "run-1", "gen_ai.operation.name": "invoke_agent"},
	}
	c1 := &trace.Span{
		ID: "s2", ParentID: "s1", Name: "execute_tool search",
		Status: trace.StatusOk, Start: start.Add(100 * time.Millisecond), End: start.Add(time.Second),
		Attrs: map[string]string{"gen_ai.operation.name": "execute_tool"},
	}
	r2 := &trace.Span{
		ID: "s3", Name: "invoke_agent subagent",
		Status: trace.StatusOk, Start: start.Add(time.Second), End: start.Add(3 * time.Second),
		Attrs: map[string]string{"gen_ai.operation.name": "invoke_agent"},
	}
	return &trace.Trace{RunID: "run-1", Roots: []*trace.Span{r1, r2}, Spans: []*trace.Span{r1, c1, r2}}
}

// assertSpanEqual compares span content field-by-field; times via .Equal so the
// JSON round-trip's location normalization cannot produce false mismatches.
func assertSpanEqual(t *testing.T, got, want *trace.Span) {
	t.Helper()
	if got.ID != want.ID || got.ParentID != want.ParentID || got.Name != want.Name ||
		got.Kind != want.Kind || got.Status != want.Status ||
		!got.Start.Equal(want.Start) || !got.End.Equal(want.End) ||
		!maps.Equal(got.Attrs, want.Attrs) {
		t.Fatalf("span %q mismatch:\n got: %+v\nwant: %+v", want.ID, got, want)
	}
}

// TestInMemStoreDecodePayloadDecodesSuppliedBytes pins the TraceStore contract
// (core.TraceStore.DecodePayload): decode THESE bytes — the correlator hashes
// the fetched payload and decodes the same bytes, so the decode must describe
// the snapshot the hash described, never the store's current state. Because
// json.Marshal duplicates root spans (Roots alias Spans in the stored forest),
// the decode must also rebuild that aliasing.
func TestInMemStoreDecodePayloadDecodesSuppliedBytes(t *testing.T) {
	t.Parallel()

	t.Run("round-trip decodes a content-equal snapshot, not the stored pointer", func(t *testing.T) {
		t.Parallel()
		forest := inmemForestWithIDs()
		st := NewInMemStore(map[string]*trace.Trace{"run-1": forest})
		payload, err := st.FetchPayload(context.Background(), "run-1")
		if err != nil {
			t.Fatalf("FetchPayload: %v", err)
		}
		got, err := st.DecodePayload("run-1", payload)
		if err != nil {
			t.Fatalf("DecodePayload: %v", err)
		}
		if got == forest {
			t.Fatal("decoded forest is the stored pointer; DecodePayload must decode the supplied bytes")
		}
		if got.RunID != forest.RunID {
			t.Fatalf("RunID = %q, want %q", got.RunID, forest.RunID)
		}
		if len(got.Spans) != len(forest.Spans) {
			t.Fatalf("spans len = %d, want %d", len(got.Spans), len(forest.Spans))
		}
		for i, want := range forest.Spans {
			assertSpanEqual(t, got.Spans[i], want)
		}
		if len(got.Roots) != 2 || got.Roots[0].ID != "s1" || got.Roots[1].ID != "s3" {
			t.Fatalf("roots = %+v, want IDs [s1 s3]", got.Roots)
		}
	})

	t.Run("decoded roots alias the decoded spans objects", func(t *testing.T) {
		t.Parallel()
		st := NewInMemStore(map[string]*trace.Trace{"run-1": inmemForestWithIDs()})
		payload, err := st.FetchPayload(context.Background(), "run-1")
		if err != nil {
			t.Fatalf("FetchPayload: %v", err)
		}
		got, err := st.DecodePayload("run-1", payload)
		if err != nil {
			t.Fatalf("DecodePayload: %v", err)
		}
		for i, root := range got.Roots {
			var match *trace.Span
			for _, sp := range got.Spans {
				if sp.ID == root.ID {
					match = sp
					break
				}
			}
			if match == nil {
				t.Fatalf("root %d (id %q) has no matching span in decoded Spans", i, root.ID)
			}
			if root != match {
				t.Fatalf("root %d (id %q) is a distinct object from its Spans entry: %p != %p", i, root.ID, root, match)
			}
		}
	})

	t.Run("decodes the fetched snapshot even after the store mutates", func(t *testing.T) {
		t.Parallel()
		forest := inmemForestWithIDs()
		st := NewInMemStore(map[string]*trace.Trace{"run-1": forest})
		payload, err := st.FetchPayload(context.Background(), "run-1")
		if err != nil {
			t.Fatalf("FetchPayload: %v", err)
		}
		// Mutate the stored forest AFTER the fetch: the hash the correlator
		// computed describes the payload, so the decode must too.
		forest.Spans = append(forest.Spans, &trace.Span{ID: "s4", Name: "late arrival"})
		got, err := st.DecodePayload("run-1", payload)
		if err != nil {
			t.Fatalf("DecodePayload: %v", err)
		}
		if len(got.Spans) != 3 {
			t.Fatalf("spans len = %d, want 3 (the snapshot); decode leaked post-fetch store state", len(got.Spans))
		}
	})
}

// TestInMemStoreDecodePayloadErrors pins the hard-error paths (constitution IV
// — no silent fallback): unknown id, malformed payload bytes (wrapped, naming
// the trace id), and a payload whose root span id is absent from Spans (the
// aliasing rebuild must not guess).
func TestInMemStoreDecodePayloadErrors(t *testing.T) {
	t.Parallel()
	st := NewInMemStore(map[string]*trace.Trace{"run-1": inmemForestWithIDs()})
	valid, err := st.FetchPayload(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("FetchPayload: %v", err)
	}
	orphanRoot, err := json.Marshal(&trace.Trace{
		RunID: "run-1",
		Roots: []*trace.Span{{ID: "ghost", Name: "orphan root"}},
		Spans: []*trace.Span{{ID: "s1", Name: "real span"}},
	})
	if err != nil {
		t.Fatalf("marshal orphan-root payload: %v", err)
	}

	tests := []struct {
		name        string
		id          string
		payload     []byte
		wantSubs    []string
		wantJSONErr bool
	}{
		{
			name:     "unknown id is a hard error",
			id:       "missing",
			payload:  valid,
			wantSubs: []string{`"missing"`},
		},
		{
			name:        "malformed payload errors naming the trace id and wraps the json error",
			id:          "run-1",
			payload:     []byte("{not json"),
			wantSubs:    []string{`"run-1"`, "decode payload"},
			wantJSONErr: true,
		},
		{
			name:     "root id absent from spans is a hard error",
			id:       "run-1",
			payload:  orphanRoot,
			wantSubs: []string{`"run-1"`, "ghost"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := st.DecodePayload(tt.id, tt.payload)
			if err == nil {
				t.Fatalf("want error, got nil (trace %+v)", got)
			}
			if got != nil {
				t.Fatalf("want nil trace on error, got %+v", got)
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q does not contain %q", err.Error(), sub)
				}
			}
			if tt.wantJSONErr {
				var syn *json.SyntaxError
				if !errors.As(err, &syn) {
					t.Fatalf("error %q does not wrap *json.SyntaxError", err.Error())
				}
			}
		})
	}
}

func TestInMemStoreQueryErrorPaths(t *testing.T) {
	data, err := os.ReadFile("../../testdata/traces/researchbot/happy.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tr, err := LoadFixture(data)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	tr.RunID = "r2"
	st := NewInMemStore(map[string]*trace.Trace{"r2": tr})

	tests := []struct {
		name    string
		query   core.TraceQuery
		wantLen int
		wantErr bool
	}{
		{
			name:    "wrong tag returns error",
			query:   core.TraceQuery{Tag: "some.other.tag", Value: "r2"},
			wantLen: 0,
			wantErr: true,
		},
		{
			name:    "no match returns nil slice no error",
			query:   core.TraceQuery{Tag: "test.run.id", Value: "does-not-exist"},
			wantLen: 0,
			wantErr: false,
		},
		{
			name:    "match returns one ref",
			query:   core.TraceQuery{Tag: "test.run.id", Value: "r2"},
			wantLen: 1,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			refs, err := st.Query(context.Background(), tt.query)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if len(refs) != tt.wantLen {
				t.Fatalf("refs len=%d want=%d", len(refs), tt.wantLen)
			}
		})
	}
}

func TestInMemStoreCaps(t *testing.T) {
	st := NewInMemStore(nil)
	caps := st.Caps()
	if caps.StructuralQuery {
		t.Fatalf("InMemStore should not report StructuralQuery capability")
	}
}

// writeFixture writes a LoadFixture-format fixture into dir and returns its path.
func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", p, err)
	}
	return p
}

// TestFileStoreQuery pins the directory-backed store's tag query (US5, R5): a
// fixture keyed by its recorded `runScenario` resolves to one ref; an absent id is
// a hard not-found error naming BOTH the dir and the id (unlike InMemStore's
// (nil,nil) — a replay against a file store is a deliberate "this exact id lives
// here", so its absence is loud); two fixtures sharing a runScenario are an
// ambiguity error naming both files (constitution IV — never guess which sample);
// a non-run-id tag is rejected.
func TestFileStoreQuery(t *testing.T) {
	t.Parallel()

	single := t.TempDir()
	writeFixture(t, single, "run-a.json", `{"runScenario":"run-a","spans":[{"name":"invoke_agent researchbot","parentIndex":-1,"status":"Ok","attrs":{"gen_ai.operation.name":"invoke_agent"}}]}`)

	dup := t.TempDir()
	dupA := writeFixture(t, dup, "a.json", `{"runScenario":"dup","spans":[{"name":"root","parentIndex":-1}]}`)
	dupB := writeFixture(t, dup, "b.json", `{"runScenario":"dup","spans":[{"name":"root","parentIndex":-1}]}`)

	tests := []struct {
		name     string
		dir      string
		query    core.TraceQuery
		wantLen  int
		wantErr  bool
		wantSubs []string
	}{
		{
			name:    "hit returns one ref",
			dir:     single,
			query:   core.TraceQuery{Tag: "test.run.id", Value: "run-a"},
			wantLen: 1,
		},
		{
			name:     "absent errors naming dir and id",
			dir:      single,
			query:    core.TraceQuery{Tag: "test.run.id", Value: "ghost"},
			wantErr:  true,
			wantSubs: []string{single, "ghost"},
		},
		{
			name:     "ambiguous errors naming both files",
			dir:      dup,
			query:    core.TraceQuery{Tag: "test.run.id", Value: "dup"},
			wantErr:  true,
			wantSubs: []string{dupA, dupB},
		},
		{
			name:     "wrong tag errors",
			dir:      single,
			query:    core.TraceQuery{Tag: "some.other.tag", Value: "run-a"},
			wantErr:  true,
			wantSubs: []string{"test.run.id"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs, err := NewFileStore(tt.dir)
			if err != nil {
				t.Fatalf("NewFileStore: %v", err)
			}
			refs, err := fs.Query(context.Background(), tt.query)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range tt.wantSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				return
			}
			if len(refs) != tt.wantLen {
				t.Fatalf("refs len=%d want=%d", len(refs), tt.wantLen)
			}
			if len(refs) == 1 && refs[0].TraceID != tt.query.Value {
				t.Fatalf("ref TraceID=%q want %q", refs[0].TraceID, tt.query.Value)
			}
		})
	}
}

// TestFileStoreFetchAndDecodeRoundTrip pins the store seam pair: FetchPayload
// returns the recorded fixture bytes and DecodePayload decodes exactly those
// bytes back into a forest via LoadFixture. A multi-root fixture (invariant §2 —
// a run may span more than one root trace) round-trips as a two-root forest.
// FetchPayload of an unknown id is a hard error naming dir+id; DecodePayload of
// malformed bytes is a hard, wrapped error.
func TestFileStoreFetchAndDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "multi.json", `{"runScenario":"multi","spans":[`+
		`{"name":"invoke_agent researchbot","parentIndex":-1,"status":"Ok","attrs":{"gen_ai.operation.name":"invoke_agent"}},`+
		`{"name":"execute_tool search","parentIndex":0,"status":"Ok","attrs":{"gen_ai.operation.name":"execute_tool","gen_ai.tool.name":"search"}},`+
		`{"name":"invoke_agent subagent","parentIndex":-1,"status":"Ok","attrs":{"gen_ai.operation.name":"invoke_agent"}}]}`)
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	payload, err := fs.FetchPayload(context.Background(), "multi")
	if err != nil {
		t.Fatalf("FetchPayload: %v", err)
	}
	tr, err := fs.DecodePayload("multi", payload)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if len(tr.Roots) != 2 {
		t.Fatalf("roots=%d want 2 (multi-root forest)", len(tr.Roots))
	}
	if len(tr.Spans) != 3 {
		t.Fatalf("spans=%d want 3", len(tr.Spans))
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if len(tools) != 1 || tools[0].Attr(genai.ToolName) != "search" {
		t.Fatalf("tool spans wrong: %+v", tools)
	}

	if _, err := fs.FetchPayload(context.Background(), "ghost"); err == nil {
		t.Fatal("FetchPayload(ghost): want error")
	} else {
		for _, sub := range []string{dir, "ghost"} {
			if !strings.Contains(err.Error(), sub) {
				t.Fatalf("error %q does not contain %q", err.Error(), sub)
			}
		}
	}
	if _, err := fs.DecodePayload("multi", []byte("{not json")); err == nil {
		t.Fatal("DecodePayload(bad bytes): want error")
	}
}

// TestFileStoreGetByIDCanonicalVocabulary pins that GetByID loads a fixture by id
// through LoadFixture, applying the feature-002 canonical status/kind vocabulary:
// an OTLP status spelling normalizes to the canonical value. Unknown id errors.
func TestFileStoreGetByIDCanonicalVocabulary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "err.json", `{"runScenario":"errrun","spans":[{"name":"checkout","parentIndex":-1,"status":"STATUS_CODE_ERROR","kind":"SPAN_KIND_SERVER"}]}`)
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	tr, err := fs.GetByID(context.Background(), "errrun")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(tr.Spans) != 1 {
		t.Fatalf("spans=%d want 1", len(tr.Spans))
	}
	if tr.Spans[0].Status != trace.StatusError {
		t.Fatalf("status=%q want %q", tr.Spans[0].Status, trace.StatusError)
	}
	if tr.Spans[0].Kind != trace.KindServer {
		t.Fatalf("kind=%q want %q", tr.Spans[0].Kind, trace.KindServer)
	}
	if _, err := fs.GetByID(context.Background(), "missing"); err == nil {
		t.Fatal("GetByID(missing): want error")
	}
}

// TestFileStoreRejectsMultiRun pins the @runs(N>1) hard error (research R5,
// constitution IV): a recorded fixture is ONE deterministic sample per run id, so
// N independent samples cannot be fabricated from it. n<=1 is allowed; n>1 is a
// hard error carrying the R5 intent (one recorded sample / live store) and the dir.
func TestFileStoreRejectsMultiRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "r.json", `{"runScenario":"r","spans":[{"name":"root","parentIndex":-1}]}`)
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	tests := []struct {
		name    string
		n       int
		wantErr bool
	}{
		{name: "single run allowed", n: 1, wantErr: false},
		{name: "zero is not multi-run", n: 0, wantErr: false},
		{name: "two runs rejected", n: 2, wantErr: true},
		{name: "five runs rejected", n: 5, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := fs.RejectMultiRun(tt.n)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range []string{"one recorded sample", "live store", dir} {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("R5 error %q does not contain %q", err.Error(), sub)
					}
				}
			}
		})
	}
}

// TestFileStoreConstructionErrors pins the loud-failure boundary (constitution IV):
// a missing dir, a malformed fixture, or a fixture with no runScenario is a hard
// error at construction (naming the file where one is at fault); an empty dir is a
// valid empty store whose queries not-found.
func TestFileStoreConstructionErrors(t *testing.T) {
	t.Parallel()

	t.Run("nonexistent dir errors", func(t *testing.T) {
		t.Parallel()
		if _, err := NewFileStore(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
			t.Fatal("want error for missing dir")
		}
	})
	t.Run("malformed fixture errors naming file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := writeFixture(t, dir, "bad.json", `{not json`)
		_, err := NewFileStore(dir)
		if err == nil {
			t.Fatal("want error for malformed fixture")
		}
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("error %q does not name file %q", err.Error(), p)
		}
	})
	t.Run("empty runScenario errors naming file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := writeFixture(t, dir, "noid.json", `{"spans":[{"name":"root","parentIndex":-1}]}`)
		_, err := NewFileStore(dir)
		if err == nil {
			t.Fatal("want error for empty runScenario")
		}
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("error %q does not name file %q", err.Error(), p)
		}
	})
	t.Run("empty dir is a valid empty store", func(t *testing.T) {
		t.Parallel()
		fs, err := NewFileStore(t.TempDir())
		if err != nil {
			t.Fatalf("empty dir should be valid: %v", err)
		}
		if _, err := fs.Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "x"}); err == nil {
			t.Fatal("query on empty store should not-found")
		}
	})
}

func TestFileStoreCaps(t *testing.T) {
	t.Parallel()
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if fs.Caps().StructuralQuery {
		t.Fatal("FileStore should not report StructuralQuery capability")
	}
}

func TestLoadFixtureMalformedJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{name: "nil data returns error", data: nil, wantErr: true},
		{name: "garbage JSON returns error", data: []byte(`{not valid json`), wantErr: true},
		{name: "empty JSON object gives empty trace", data: []byte(`{}`), wantErr: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tr, err := LoadFixture(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && tr == nil {
				t.Fatalf("expected non-nil trace on success")
			}
		})
	}
}
