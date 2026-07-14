package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// defaultSearchLimit is the page size Tempo.Query sends when none is configured.
// It bounds the search result set so a full page can be treated as
// possibly-truncated (research R3, A4).
const defaultSearchLimit = 100

type Tempo struct {
	endpoint    string
	hc          *http.Client
	searchLimit int
}

// NewTempo builds a Tempo store. searchLimit is the /api/search page size; a
// non-positive value is treated as defaultSearchLimit at query time.
func NewTempo(endpoint string, hc *http.Client, searchLimit int) *Tempo {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Tempo{endpoint: strings.TrimRight(endpoint, "/"), hc: hc, searchLimit: searchLimit}
}

func (t *Tempo) Caps() core.StoreCaps { return core.StoreCaps{StructuralQuery: true} }

// --- OTLP JSON shapes (minimal subset) ---

type otlpValue struct {
	StringValue *string  `json:"stringValue"`
	IntValue    *string  `json:"intValue"` // proto JSON encodes int64 as string
	DoubleValue *float64 `json:"doubleValue"`
	BoolValue   *bool    `json:"boolValue"`
}

type otlpKV struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpSpan struct {
	TraceID           string   `json:"traceId"`
	SpanID            string   `json:"spanId"`
	ParentSpanID      string   `json:"parentSpanId"`
	Name              string   `json:"name"`
	StartTimeUnixNano string   `json:"startTimeUnixNano"`
	EndTimeUnixNano   string   `json:"endTimeUnixNano"`
	Attributes        []otlpKV `json:"attributes"`
	Kind              string   `json:"kind"`
	Status            struct {
		Code string `json:"code"`
	} `json:"status"`
}

// otlpResourceSpan is the shared inner shape used by both OTLP "resourceSpans"
// and the Tempo-specific "batches" top-level keys.
type otlpResourceSpan struct {
	Resource struct {
		Attributes []otlpKV `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []struct {
		Spans []otlpSpan `json:"spans"`
	} `json:"scopeSpans"`
}

type otlpTrace struct {
	// Standard OTLP JSON envelope.
	ResourceSpans []otlpResourceSpan `json:"resourceSpans"`
	// Tempo's /api/traces/{id} returns spans under "batches" instead.
	Batches []otlpResourceSpan `json:"batches"`
}

func valStr(v otlpValue) string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64)
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

func nanos(s string) (time.Time, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("tempo: invalid nanosecond timestamp %q: %w", s, err)
	}
	return time.Unix(0, n), nil
}

// FetchPayload returns the exact /api/traces/{id} response body — the raw
// payload whose bytes the stability poll compares round over round (feature
// 004, FR-002). It never parses or re-encodes: DecodePayload must decode these
// same bytes, so the hashed bytes and the decoded bytes are one fetch.
func (t *Tempo) FetchPayload(ctx context.Context, id string) ([]byte, error) {
	return t.get(ctx, t.endpoint+"/api/traces/"+url.PathEscape(id))
}

// GetByID fetches and decodes in one step: DecodePayload(FetchPayload(id)).
// The stability poll uses the split methods directly; this composition remains
// for one-shot callers and pins that both halves agree.
func (t *Tempo) GetByID(ctx context.Context, id string) (*trace.Trace, error) {
	body, err := t.FetchPayload(ctx, id)
	if err != nil {
		return nil, err
	}
	return t.DecodePayload(id, body)
}

// DecodePayload decodes payload bytes previously returned by FetchPayload for
// the same id into a Trace forest. Resource attributes are merged onto each
// span here — once per decode, not once per poll round (audit C5).
func (t *Tempo) DecodePayload(id string, payload []byte) (*trace.Trace, error) {
	var ot otlpTrace
	if err := json.Unmarshal(payload, &ot); err != nil {
		return nil, fmt.Errorf("tempo: parse trace %s: %w", id, err)
	}
	tr := &trace.Trace{}
	byID := map[string]*trace.Span{}
	allRS := append(ot.ResourceSpans, ot.Batches...)
	for _, rs := range allRS {
		resAttrs := map[string]string{}
		for _, kv := range rs.Resource.Attributes {
			resAttrs[kv.Key] = valStr(kv.Value)
		}
		for _, ss := range rs.ScopeSpans {
			for _, s := range ss.Spans {
				attrs := map[string]string{}
				for k, v := range resAttrs { // merge resource attrs onto span
					attrs[k] = v
				}
				for _, kv := range s.Attributes {
					attrs[kv.Key] = valStr(kv.Value)
				}
				start, err := nanos(s.StartTimeUnixNano)
				if err != nil {
					return nil, fmt.Errorf("tempo: trace %s span %s: invalid startTimeUnixNano %q: %w", id, s.SpanID, s.StartTimeUnixNano, err)
				}
				end, err := nanos(s.EndTimeUnixNano)
				if err != nil {
					return nil, fmt.Errorf("tempo: trace %s span %s: invalid endTimeUnixNano %q: %w", id, s.SpanID, s.EndTimeUnixNano, err)
				}
				status, err := trace.NormalizeStatus(s.Status.Code)
				if err != nil {
					return nil, fmt.Errorf("tempo: trace %s span %s: %w", id, s.SpanID, err)
				}
				kind, err := trace.NormalizeKind(s.Kind)
				if err != nil {
					return nil, fmt.Errorf("tempo: trace %s span %s: %w", id, s.SpanID, err)
				}
				sp := &trace.Span{
					ID:       s.SpanID,
					ParentID: s.ParentSpanID,
					Name:     s.Name,
					Kind:     kind,
					Start:    start,
					End:      end,
					Status:   status,
					Attrs:    attrs,
				}
				tr.Spans = append(tr.Spans, sp)
				byID[sp.ID] = sp
			}
		}
	}
	for _, sp := range tr.Spans {
		if sp.ParentID == "" || byID[sp.ParentID] == nil {
			tr.Roots = append(tr.Roots, sp)
		}
	}
	return tr, nil
}

func (t *Tempo) Query(ctx context.Context, q core.TraceQuery) ([]core.TraceRef, error) {
	limit := t.searchLimit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	// q.Value is constrained to [A-Za-z0-9._:-]+ by correlate.Inject, so it needs no TraceQL string escaping here.
	traceql := fmt.Sprintf(`{ .%s = "%s" }`, q.Tag, q.Value)
	u := t.endpoint + "/api/search?q=" + url.QueryEscape(traceql) + "&limit=" + strconv.Itoa(limit)
	body, err := t.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var res struct {
		Traces []struct {
			TraceID string `json:"traceID"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("tempo: parse search: %w", err)
	}
	// A full page may have been truncated by Tempo (paging support varies by
	// version), so a count equal to the limit is treated as possibly-incomplete and
	// hard-errored rather than silently dropping evidence (research R3, A4).
	if len(res.Traces) == limit {
		return nil, fmt.Errorf("tempo: search for %s=%q returned %d traces (== limit); result set may be truncated — raise poll.searchLimit", q.Tag, q.Value, len(res.Traces))
	}
	refs := make([]core.TraceRef, 0, len(res.Traces))
	for _, tr := range res.Traces {
		refs = append(refs, core.TraceRef{TraceID: tr.TraceID})
	}
	return refs, nil
}

func (t *Tempo) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tempo: build request %s: %w", u, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tempo: GET %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tempo: GET %s: status %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tempo: read %s: %w", u, err)
	}
	return body, nil
}
