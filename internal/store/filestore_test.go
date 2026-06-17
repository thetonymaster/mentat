package store

import (
	"context"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

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
