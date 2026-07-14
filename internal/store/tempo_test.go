package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// otlpBodyWithSpanFields wraps a single span whose extra JSON fields (status
// block, kind) are injected verbatim, so a table row can vary only those.
func otlpBodyWithSpanFields(fields string) string {
	return `{
  "resourceSpans": [{
    "resource": { "attributes": [] },
    "scopeSpans": [{ "spans": [
      {
        "traceId": "tt", "spanId": "s1", "name": "span-one",
        "startTimeUnixNano": "1000", "endTimeUnixNano": "2000",
        "attributes": []` + fields + `
      }
    ]}]
  }]
}`
}

// TestTempoGetByIDNormalizesStatusAndKind pins the A1 fix: GetByID must map OTLP
// status/kind spellings onto the canonical vocabulary at decode time (never a raw
// passthrough), and hard-error on an unknown status naming the span and value.
func TestTempoGetByIDNormalizesStatusAndKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		fields     string
		wantStatus string
		wantKind   string
		wantErr    bool
		wantSubs   []string
	}{
		{
			name:       "STATUS_CODE_ERROR normalizes to Error",
			fields:     `, "status": { "code": "STATUS_CODE_ERROR" }`,
			wantStatus: trace.StatusError,
			wantKind:   trace.KindUnspecified,
		},
		{
			name:       "STATUS_CODE_OK normalizes to Ok",
			fields:     `, "status": { "code": "STATUS_CODE_OK" }`,
			wantStatus: trace.StatusOk,
		},
		{
			name:       "STATUS_CODE_UNSET normalizes to Unset",
			fields:     `, "status": { "code": "STATUS_CODE_UNSET" }`,
			wantStatus: trace.StatusUnset,
		},
		{
			name:       "omitted status defaults to Unset",
			fields:     ``,
			wantStatus: trace.StatusUnset,
		},
		{
			name:       "SPAN_KIND_SERVER decoded",
			fields:     `, "kind": "SPAN_KIND_SERVER"`,
			wantStatus: trace.StatusUnset,
			wantKind:   trace.KindServer,
		},
		{
			name:     "unknown status spelling errors naming span and value",
			fields:   `, "status": { "code": "STATUS_CODE_BANANA" }`,
			wantErr:  true,
			wantSubs: []string{"s1", "STATUS_CODE_BANANA"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(otlpBodyWithSpanFields(tt.fields)))
			}))
			defer srv.Close()

			tr, err := NewTempo(srv.URL, srv.Client(), 0).GetByID(context.Background(), "tt")
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

// TestTempoFetchPayloadReturnsExactBody pins the feature-004 raw-payload seam
// (FR-002): FetchPayload must return the EXACT /api/traces/{id} response body —
// byte-for-byte, no parsing, no re-encoding — because those bytes are the
// stability poll's change-detection signal and later the input to DecodePayload
// (the hashed bytes and the decoded bytes must be the same fetch).
func TestTempoFetchPayloadReturnsExactBody(t *testing.T) {
	t.Parallel()
	// Deliberately NOT canonical JSON: leading/trailing whitespace and key order
	// must survive verbatim — a raw accessor that parses/re-encodes would lose them.
	const body = "  {\"batches\": [],  \"zzz\": 1}\n\n"

	tests := []struct {
		name       string
		status     int
		wantErr    bool
		wantErrSub string
	}{
		{name: "200 returns exact bytes", status: http.StatusOK},
		{name: "non-200 is a hard error", status: http.StatusInternalServerError, wantErr: true, wantErrSub: "status 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			got, err := NewTempo(srv.URL, srv.Client(), 0).FetchPayload(context.Background(), "tt")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if string(got) != body {
				t.Fatalf("payload not the exact response body:\ngot  %q\nwant %q", got, body)
			}
		})
	}
}

// TestTempoDecodePayloadDecodesFetchedBytes pins the decode half of the seam
// split: DecodePayload(id, payload) must decode payload bytes previously
// returned by FetchPayload — same forest building and resource-attr merge
// (the C5 copy) as the composed GetByID path — and hard-error on bytes it
// cannot decode, naming the trace id.
func TestTempoDecodePayloadDecodesFetchedBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		payload    string
		wantErr    bool
		wantErrSub string
		check      func(t *testing.T, tr *trace.Trace)
	}{
		{
			name:    "otlp resourceSpans envelope decodes with resource attrs merged",
			payload: otlpTraceJSON,
			check: func(t *testing.T, tr *trace.Trace) {
				if len(tr.Spans) != 2 || len(tr.Roots) != 1 {
					t.Fatalf("forest wrong: spans=%d roots=%d", len(tr.Spans), len(tr.Roots))
				}
				if tr.Roots[0].Attr("test.run.id") != "abc123" {
					t.Fatalf("resource attr not merged onto span: %v", tr.Roots[0].Attrs)
				}
			},
		},
		{
			name:       "malformed bytes are a hard error naming the id",
			payload:    "not json",
			wantErr:    true,
			wantErrSub: "parse trace deadbeef",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tp := NewTempo("http://unused.invalid", http.DefaultClient, 0)
			tr, err := tp.DecodePayload("deadbeef", []byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			tt.check(t, tr)
		})
	}
}

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

	tr, err := NewTempo(srv.URL, srv.Client(), 0).GetByID(context.Background(), "aa")
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

	refs, err := NewTempo(srv.URL, srv.Client(), 0).Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "abc123"})
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

// TestTempoQuerySendsLimitParam pins A4: every search request carries an explicit
// &limit=<N>. A non-positive configured limit falls back to 100 at query time
// (belt-and-suspenders so a bare-constructed store still bounds its page).
func TestTempoQuerySendsLimitParam(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		limit     int
		wantLimit string
	}{
		{name: "explicit 100", limit: 100, wantLimit: "100"},
		{name: "explicit 50", limit: 50, wantLimit: "50"},
		{name: "zero defaults to 100", limit: 0, wantLimit: "100"},
		{name: "negative defaults to 100", limit: -5, wantLimit: "100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var gotLimit string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotLimit = r.URL.Query().Get("limit")
				_, _ = w.Write([]byte(`{"traces":[{"traceID":"aa"}]}`))
			}))
			defer srv.Close()

			_, err := NewTempo(srv.URL, srv.Client(), tt.limit).Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "abc123"})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if gotLimit != tt.wantLimit {
				t.Fatalf("limit param = %q, want %q", gotLimit, tt.wantLimit)
			}
		})
	}
}

// tempoSearchBody renders a Tempo /api/search response with n trace refs.
func tempoSearchBody(n int) string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, `{"traceID":"t`+strconv.Itoa(i)+`"}`)
	}
	return `{"traces":[` + strings.Join(ids, ",") + `]}`
}

// TestTempoQueryTruncationGuard pins A4/R3: a search response returning exactly
// `limit` traces is possibly-truncated, so Query hard-errors (naming the tag/value,
// the count, and the poll.searchLimit knob) instead of silently dropping evidence.
// A response below the limit returns its refs normally.
func TestTempoQueryTruncationGuard(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		limit    int
		nTraces  int
		wantErr  bool
		wantRefs int
		wantSubs []string
	}{
		{
			name:    "exactly limit traces returns truncation error",
			limit:   3,
			nTraces: 3,
			wantErr: true,
			wantSubs: []string{
				"test.run.id", `"abc123"`, "returned 3 traces", "== limit",
				"poll.searchLimit",
			},
		},
		{
			name:     "fewer than limit returns refs normally",
			limit:    3,
			nTraces:  2,
			wantRefs: 2,
		},
		{
			name:     "zero traces returns no refs and no error",
			limit:    3,
			nTraces:  0,
			wantRefs: 0,
		},
		{
			name:    "default limit (100) full page errors",
			limit:   0,
			nTraces: 100,
			wantErr: true,
			wantSubs: []string{
				"returned 100 traces", "== limit", "poll.searchLimit",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := tempoSearchBody(tt.nTraces)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			refs, err := NewTempo(srv.URL, srv.Client(), tt.limit).Query(context.Background(), core.TraceQuery{Tag: "test.run.id", Value: "abc123"})
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
			if len(refs) != tt.wantRefs {
				t.Fatalf("refs = %d, want %d", len(refs), tt.wantRefs)
			}
		})
	}
}

func TestTempoCaps(t *testing.T) {
	caps := NewTempo("http://localhost", nil, 0).Caps()
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
			err := tt.action(NewTempo(srv.URL, srv.Client(), 0))
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
	tp := NewTempo(bad, http.DefaultClient, 0)
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

	_, err := NewTempo(srv.URL, srv.Client(), 0).GetByID(context.Background(), "bb")
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

	tr, err := NewTempo(srv.URL, srv.Client(), 0).GetByID(context.Background(), "cc")
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

	tr, err := NewTempo(srv.URL, srv.Client(), 0).GetByID(context.Background(), "aa")
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

	tr, err := NewTempo(srv.URL, srv.Client(), 0).GetByID(context.Background(), "zz")
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
