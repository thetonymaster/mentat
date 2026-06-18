package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
)

const otlpTraceJSON = `{
  "resourceSpans": [{
    "resource": { "attributes": [
      { "key": "test.run.id", "value": { "stringValue": "abc123" } }
    ]},
    "scopeSpans": [{ "spans": [
      {
        "traceId": "aa", "spanId": "01", "name": "invoke_agent researchbot",
        "startTimeUnixNano": "1000", "endTimeUnixNano": "4000",
        "attributes": [
          { "key": "gen_ai.operation.name", "value": { "stringValue": "invoke_agent" } },
          { "key": "gen_ai.usage.input_tokens", "value": { "intValue": "1200" } }
        ]
      },
      {
        "traceId": "aa", "spanId": "02", "parentSpanId": "01", "name": "execute_tool search",
        "startTimeUnixNano": "2000", "endTimeUnixNano": "2500",
        "attributes": [
          { "key": "gen_ai.operation.name", "value": { "stringValue": "execute_tool" } },
          { "key": "gen_ai.tool.name", "value": { "stringValue": "search" } }
        ]
      }
    ]}]
  }]
}`

func TestTempoGetByIDParsesForestAndMergesResourceAttrs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(otlpTraceJSON))
	}))
	defer srv.Close()

	tr, err := NewTempo(srv.URL, srv.Client()).GetByID(context.Background(), "aa")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(tr.Spans) != 2 || len(tr.Roots) != 1 {
		t.Fatalf("forest wrong: spans=%d roots=%d", len(tr.Spans), len(tr.Roots))
	}
	if tr.Roots[0].Attr("test.run.id") != "abc123" {
		t.Fatalf("resource attr not merged onto span: %v", tr.Roots[0].Attrs)
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if len(tools) != 1 || tools[0].Attr(genai.ToolName) != "search" {
		t.Fatalf("tool span wrong: %v", tools)
	}
	if n, _ := tr.Roots[0].AttrInt(genai.InTokens); n != 1200 {
		t.Fatalf("input tokens = %d", n)
	}
}

func TestTempoQueryBuildsTraceQLAndReturnsRefs(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`{"traces":[{"traceID":"aa"},{"traceID":"bb"}]}`))
	}))
	defer srv.Close()

	refs, err := NewTempo(srv.URL, srv.Client()).Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "abc123"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(refs) != 2 || refs[0].TraceID != "aa" {
		t.Fatalf("refs = %v", refs)
	}
	if !strings.Contains(gotQuery, `.test.run.id = "abc123"`) {
		t.Fatalf("traceql = %q", gotQuery)
	}
}

func TestTempoCaps(t *testing.T) {
	caps := NewTempo("http://localhost", nil).Caps()
	if !caps.StructuralQuery {
		t.Fatal("Tempo must report StructuralQuery=true")
	}
}

func TestTempoErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		action  func(t *Tempo) error
		wantSub string
	}{
		{
			name: "GetByID_non200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			action: func(tp *Tempo) error {
				_, err := tp.GetByID(context.Background(), "missing")
				return err
			},
			wantSub: "status 404",
		},
		{
			name: "GetByID_malformed_JSON_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("not-json"))
			},
			action: func(tp *Tempo) error {
				_, err := tp.GetByID(context.Background(), "bad")
				return err
			},
			wantSub: "parse trace",
		},
		{
			name: "Query_non200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			action: func(tp *Tempo) error {
				_, err := tp.Query(context.Background(), core.TraceQuery{Tag: "k", Value: "v"})
				return err
			},
			wantSub: "status 500",
		},
		{
			name: "Query_malformed_JSON_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{{{"))
			},
			action: func(tp *Tempo) error {
				_, err := tp.Query(context.Background(), core.TraceQuery{Tag: "k", Value: "v"})
				return err
			},
			wantSub: "parse search",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			err := tt.action(NewTempo(srv.URL, srv.Client()))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}

// TestTempoMalformedEndpointReturnsError proves get() does not panic when the
// endpoint URL is so malformed that http.NewRequestWithContext returns an error.
func TestTempoMalformedEndpointReturnsError(t *testing.T) {
	// A URL with a control character (0x7f) is rejected by the net/http request
	// builder, giving us a request-construction error without needing a server.
	bad := "http://\x7f/bad"
	tp := NewTempo(bad, http.DefaultClient)
	_, err := tp.GetByID(context.Background(), "trace1")
	if err == nil {
		t.Fatal("expected error from malformed endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "build request") {
		t.Fatalf("error %q does not contain 'build request'", err.Error())
	}
}

// TestTempoGetByIDNonNumericNanoReturnsError proves that a span carrying a
// non-numeric startTimeUnixNano makes GetByID return a descriptive error
// instead of silently substituting epoch-0.
const otlpTraceInvalidNano = `{
  "resourceSpans": [{
    "resource": { "attributes": [] },
    "scopeSpans": [{ "spans": [
      {
        "traceId": "bb", "spanId": "a1", "name": "bad-time",
        "startTimeUnixNano": "not-a-number", "endTimeUnixNano": "2000",
        "attributes": []
      }
    ]}]
  }]
}`

func TestTempoGetByIDNonNumericNanoReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(otlpTraceInvalidNano))
	}))
	defer srv.Close()

	_, err := NewTempo(srv.URL, srv.Client()).GetByID(context.Background(), "bb")
	if err == nil {
		t.Fatal("expected error for non-numeric nano, got nil")
	}
	if !strings.Contains(err.Error(), "startTimeUnixNano") {
		t.Fatalf("error %q does not contain 'startTimeUnixNano'", err.Error())
	}
	if !strings.Contains(err.Error(), "not-a-number") {
		t.Fatalf("error %q does not contain the bad value 'not-a-number'", err.Error())
	}
}

// TestTempoGetByIDMultiRootForest proves the forest invariant: two spans with
// no parentSpanId both become roots (Trace.Roots length == 2).
const otlpTwoRoots = `{
  "resourceSpans": [{
    "resource": { "attributes": [] },
    "scopeSpans": [{ "spans": [
      {
        "traceId": "cc", "spanId": "r1", "name": "root-one",
        "startTimeUnixNano": "1000", "endTimeUnixNano": "2000",
        "attributes": []
      },
      {
        "traceId": "cc", "spanId": "r2", "name": "root-two",
        "startTimeUnixNano": "3000", "endTimeUnixNano": "4000",
        "attributes": []
      }
    ]}]
  }]
}`

func TestTempoGetByIDMultiRootForest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(otlpTwoRoots))
	}))
	defer srv.Close()

	tr, err := NewTempo(srv.URL, srv.Client()).GetByID(context.Background(), "cc")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(tr.Roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tr.Roots))
	}
}

// otlpTraceBatches is the Tempo-specific envelope: top-level key is "batches"
// instead of "resourceSpans". Same inner shape — this is the real Tempo response.
const otlpTraceBatches = `{
  "batches": [{
    "resource": { "attributes": [
      { "key": "test.run.id", "value": { "stringValue": "abc123" } }
    ]},
    "scopeSpans": [{ "spans": [
      {
        "traceId": "aa", "spanId": "01", "name": "invoke_agent researchbot",
        "startTimeUnixNano": "1000", "endTimeUnixNano": "4000",
        "attributes": [
          { "key": "gen_ai.operation.name", "value": { "stringValue": "invoke_agent" } },
          { "key": "gen_ai.usage.input_tokens", "value": { "intValue": "1200" } }
        ]
      },
      {
        "traceId": "aa", "spanId": "02", "parentSpanId": "01", "name": "execute_tool search",
        "startTimeUnixNano": "2000", "endTimeUnixNano": "2500",
        "attributes": [
          { "key": "gen_ai.operation.name", "value": { "stringValue": "execute_tool" } },
          { "key": "gen_ai.tool.name", "value": { "stringValue": "search" } }
        ]
      }
    ]}]
  }]
}`

// TestTempoGetByIDBatchesEnvelope reproduces the live-Tempo bug: Tempo returns
// spans under "batches" (not "resourceSpans"). GetByID must parse both envelopes.
func TestTempoGetByIDBatchesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(otlpTraceBatches))
	}))
	defer srv.Close()

	tr, err := NewTempo(srv.URL, srv.Client()).GetByID(context.Background(), "aa")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(tr.Spans) != 2 || len(tr.Roots) != 1 {
		t.Fatalf("forest wrong: spans=%d roots=%d", len(tr.Spans), len(tr.Roots))
	}
	if tr.Roots[0].Attr("test.run.id") != "abc123" {
		t.Fatalf("resource attr not merged onto span: %v", tr.Roots[0].Attrs)
	}
	tools := tr.ByOp(genai.OpExecuteTool)
	if len(tools) != 1 || tools[0].Attr(genai.ToolName) != "search" {
		t.Fatalf("tool span wrong: %v", tools)
	}
	if n, _ := tr.Roots[0].AttrInt(genai.InTokens); n != 1200 {
		t.Fatalf("input tokens = %d", n)
	}
}

// otlpTraceAllValueTypes exercises the double/bool branches of valStr.
const otlpTraceAllValueTypes = `{
  "resourceSpans": [{
    "resource": { "attributes": [] },
    "scopeSpans": [{ "spans": [
      {
        "traceId": "zz", "spanId": "99", "name": "test",
        "startTimeUnixNano": "0", "endTimeUnixNano": "0",
        "attributes": [
          { "key": "cost", "value": { "doubleValue": 0.5 } },
          { "key": "flag", "value": { "boolValue": true } },
          { "key": "empty", "value": {} }
        ]
      }
    ]}]
  }]
}`

func TestTempoValStrBranches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(otlpTraceAllValueTypes))
	}))
	defer srv.Close()

	tr, err := NewTempo(srv.URL, srv.Client()).GetByID(context.Background(), "zz")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(tr.Spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tr.Spans))
	}
	sp := tr.Spans[0]
	if sp.Attr("cost") != "0.5" {
		t.Fatalf("double attr = %q", sp.Attr("cost"))
	}
	if sp.Attr("flag") != "true" {
		t.Fatalf("bool attr = %q", sp.Attr("flag"))
	}
	if sp.Attr("empty") != "" {
		t.Fatalf("empty attr = %q", sp.Attr("empty"))
	}
}
