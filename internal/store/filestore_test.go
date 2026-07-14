package store

import (
	"context"
	"os"
	"strings"
	"testing"

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

// TestInMemStoreDecodePayloadReturnsStoredForest pins the decode half for the
// hermetic store: the payload IS the canonical serialization of the stored
// forest, so decoding it yields exactly that stored forest; an unknown id is a
// hard error, never a silent nil.
func TestInMemStoreDecodePayloadReturnsStoredForest(t *testing.T) {
	t.Parallel()
	forest := inmemForest(nil)
	st := NewInMemStore(map[string]*trace.Trace{"run-1": forest})
	payload, err := st.FetchPayload(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("FetchPayload: %v", err)
	}

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "known id decodes to the stored forest", id: "run-1"},
		{name: "unknown id is a hard error", id: "missing", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := st.DecodePayload(tt.id, payload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), `"missing"`) {
					t.Fatalf("error does not name the id: %q", err.Error())
				}
				return
			}
			if got != forest {
				t.Fatalf("decoded forest is not the stored forest: got %p want %p", got, forest)
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
