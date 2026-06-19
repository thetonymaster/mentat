package driver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestHTTPDriverHappyPath(t *testing.T) {
	var gotMethod, gotScenario, gotBaggage, gotBody, gotContentType, gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotScenario = r.Header.Get("X-Scenario")
		gotBaggage = r.Header.Get("baggage")
		gotContentType = r.Header.Get("Content-Type")
		gotTraceparent = r.Header.Get("traceparent")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"confirmed"}`))
	}))
	defer srv.Close()

	spec := core.RunSpec{
		Target:  "checkout",
		Adapter: "http",
		Command: []string{"--scenario", "happy"},
		Input:   "request-body",
		HTTP: core.HTTPSpec{
			URL:     srv.URL,
			Method:  http.MethodPost,
			Headers: map[string]string{"Content-Type": "application/json"},
		},
		RunID: "run-abc",
		Tags:  map[string]string{"test.run.id": "run-abc"},
	}

	res, err := NewHTTP().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotScenario != "happy" {
		t.Errorf("X-Scenario = %q, want happy", gotScenario)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody != "request-body" {
		t.Errorf("body = %q, want request-body", gotBody)
	}
	if !strings.Contains(gotBaggage, "test.run.id=run-abc") {
		t.Errorf("baggage %q missing test.run.id=run-abc", gotBaggage)
	}
	if !strings.Contains(gotBaggage, "test.scenario=happy") {
		t.Errorf("baggage %q missing test.scenario=happy", gotBaggage)
	}
	if res.Output.Status != http.StatusCreated {
		t.Errorf("Status = %d, want 201", res.Output.Status)
	}
	if res.Output.Answer != `{"status":"confirmed"}` {
		t.Errorf("Answer = %q", res.Output.Answer)
	}
	if string(res.Output.Body) != `{"status":"confirmed"}` {
		t.Errorf("Body = %q", res.Output.Body)
	}
	if res.RunID != "run-abc" {
		t.Errorf("RunID = %q, want run-abc", res.RunID)
	}
	if gotTraceparent != "" {
		t.Errorf("driver must not inject traceparent, got %q", gotTraceparent)
	}
}

func TestHTTPDriverNon2xxIsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"status":"declined"}`))
	}))
	defer srv.Close()

	spec := core.RunSpec{
		Target:  "checkout",
		Adapter: "http",
		Command: []string{"--scenario", "payment_decline"},
		HTTP:    core.HTTPSpec{URL: srv.URL, Method: http.MethodPost},
		RunID:   "run-402",
		Tags:    map[string]string{"test.run.id": "run-402"},
	}

	res, err := NewHTTP().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("non-2xx must NOT be an error, got: %v", err)
	}
	if res.Output.Status != http.StatusPaymentRequired {
		t.Errorf("Status = %d, want 402", res.Output.Status)
	}
	if string(res.Output.Body) != `{"status":"declined"}` {
		t.Errorf("Body = %q, want {\"status\":\"declined\"}", res.Output.Body)
	}
	if res.Output.Answer != `{"status":"declined"}` {
		t.Errorf("Answer = %q, want {\"status\":\"declined\"}", res.Output.Answer)
	}
}

func TestHTTPDriverErrors(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close() // connection will be refused

	tests := []struct {
		name    string
		spec    core.RunSpec
		wantSub string
	}{
		{
			name:    "empty URL is an error",
			spec:    core.RunSpec{Target: "checkout", HTTP: core.HTTPSpec{Method: "POST"}},
			wantSub: "empty URL",
		},
		{
			name:    "empty method is an error",
			spec:    core.RunSpec{Target: "checkout", HTTP: core.HTTPSpec{URL: "http://x"}},
			wantSub: "empty method",
		},
		{
			name:    "transport failure is a wrapped error",
			spec:    core.RunSpec{Target: "checkout", RunID: "run-x", HTTP: core.HTTPSpec{URL: closedURL, Method: "POST"}},
			wantSub: "run-x",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHTTP().Run(context.Background(), tt.spec)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}
