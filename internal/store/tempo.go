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

type Tempo struct {
	endpoint string
	hc       *http.Client
}

func NewTempo(endpoint string, hc *http.Client) *Tempo {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Tempo{endpoint: strings.TrimRight(endpoint, "/"), hc: hc}
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

func (t *Tempo) GetByID(ctx context.Context, id string) (*trace.Trace, error) {
	body, err := t.get(ctx, t.endpoint+"/api/traces/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	var ot otlpTrace
	if err := json.Unmarshal(body, &ot); err != nil {
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
				sp := &trace.Span{
					ID:       s.SpanID,
					ParentID: s.ParentSpanID,
					Name:     s.Name,
					Start:    start,
					End:      end,
					Status:   s.Status.Code,
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
	// q.Value is constrained to [A-Za-z0-9._:-]+ by correlate.Inject, so it needs no TraceQL string escaping here.
	traceql := fmt.Sprintf(`{ .%s = "%s" }`, q.Tag, q.Value)
	u := t.endpoint + "/api/search?q=" + url.QueryEscape(traceql)
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
