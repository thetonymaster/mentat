package orderflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// ---------------------------------------------------------------------------
// Pure-function tests (from the brief)
// ---------------------------------------------------------------------------

func TestPlanForEncodesScenarioCallOrderAndStatus(t *testing.T) {
	tests := []struct {
		scenario   string
		wantCalls  []string
		wantStatus int
	}{
		{"happy", []string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, 201},
		{"slow", []string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, 201},
		{"payment_decline", []string{ServiceAuth, ServiceInventory, ServicePayment}, 402},
		{"inventory_out", []string{ServiceAuth, ServiceInventory}, 409},
		{"legacy_path", []string{ServiceAuth, ServiceLegacy, ServiceInventory, ServicePayment, ServiceNotify}, 201},
		{"reorder", []string{ServiceAuth, ServicePayment, ServiceInventory, ServiceNotify}, 201},
		{"bogus", nil, http.StatusBadRequest},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.scenario, func(t *testing.T) {
			p := planFor(tt.scenario)
			if !reflect.DeepEqual(p.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", p.calls, tt.wantCalls)
			}
			if p.status != tt.wantStatus {
				t.Errorf("status = %d, want %d", p.status, tt.wantStatus)
			}
		})
	}
}

func TestExpectedResultIsDeterministicJSON(t *testing.T) {
	tests := []struct {
		scenario   string
		wantStatus int
		wantBody   string
	}{
		{"happy", 201, `{"status":"confirmed"}`},
		{"payment_decline", 402, `{"status":"declined"}`},
		{"inventory_out", 409, `{"status":"out_of_stock"}`},
		{"unknown scenario falls through to 400", 400, `{"status":"unknown_scenario"}`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.scenario, func(t *testing.T) {
			status, body := ExpectedResult(tt.scenario)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if string(body) != tt.wantBody {
				t.Errorf("body = %s, want %s", body, tt.wantBody)
			}
		})
	}
}

// TestGatewayBodyMatchesExpectedResult asserts that the bytes the gateway writes
// over the wire are identical to the bytes ExpectedResult returns — the contract
// the result comparator relies on.
func TestGatewayBodyMatchesExpectedResult(t *testing.T) {
	tests := []struct {
		scenario string
	}{
		{"happy"},
		{"payment_decline"},
		{"inventory_out"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.scenario, func(t *testing.T) {
			stub200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer stub200.Close()
			stub402 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusPaymentRequired)
			}))
			defer stub402.Close()
			stub409 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
			}))
			defer stub409.Close()

			topo := Topology{
				ServiceAuth:      stub200.URL,
				ServiceInventory: stub409.URL,
				ServicePayment:   stub402.URL,
				ServiceNotify:    stub200.URL,
			}

			h := gatewayHandler(&http.Client{}, topo)
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set(HeaderScenario, tt.scenario)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			_, wantBody := ExpectedResult(tt.scenario)
			gotBody := rec.Body.Bytes()
			if string(gotBody) != string(wantBody) {
				t.Errorf("scenario %q: gateway body = %q, want %q", tt.scenario, gotBody, wantBody)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// leafHandler tests
// ---------------------------------------------------------------------------

func TestLeafHandler(t *testing.T) {
	tests := []struct {
		name        string
		service     string
		scenario    string
		wantStatus  int
		wantErrSpan bool // expect an Error-status child span
	}{
		{
			name:        "payment_decline -> 402 with error span",
			service:     ServicePayment,
			scenario:    "payment_decline",
			wantStatus:  http.StatusPaymentRequired,
			wantErrSpan: true,
		},
		{
			name:       "inventory_out -> 409",
			service:    ServiceInventory,
			scenario:   "inventory_out",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "default (auth happy) -> 200",
			service:    ServiceAuth,
			scenario:   "happy",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			exp := tracetest.NewInMemoryExporter()
			tp, err := NewTracerProvider(context.Background(), tt.service, exp)
			if err != nil {
				t.Fatalf("NewTracerProvider: %v", err)
			}
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

			tr := tp.Tracer(tt.service)
			h := leafHandler(tt.service, tr)

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set(HeaderScenario, tt.scenario)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantErrSpan {
				spans := waitForSpans(t, exp)
				foundErr := false
				for _, s := range spans {
					if s.Status().Code.String() == "Error" {
						foundErr = true
						break
					}
				}
				if !foundErr {
					t.Errorf("expected an Error-status span but found none in %d spans", len(spans))
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// gatewayHandler tests
// ---------------------------------------------------------------------------

func TestGatewayHandler(t *testing.T) {
	stub200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stub200.Close()
	stub402 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer stub402.Close()

	tests := []struct {
		name       string
		scenario   string
		topo       Topology
		wantStatus int
	}{
		{
			name:     "happy path all 200 -> 201",
			scenario: "happy",
			topo: Topology{
				ServiceAuth:      stub200.URL,
				ServiceInventory: stub200.URL,
				ServicePayment:   stub200.URL,
				ServiceNotify:    stub200.URL,
			},
			wantStatus: http.StatusCreated,
		},
		{
			// payment returns 402; gateway short-circuits and returns its own
			// plan status (402). ServiceNotify is intentionally absent to prove
			// the short-circuit prevents calling it.
			name:     "payment_decline stub 402 -> gateway 402 short-circuit",
			scenario: "payment_decline",
			topo: Topology{
				ServiceAuth:      stub200.URL,
				ServiceInventory: stub200.URL,
				ServicePayment:   stub402.URL,
			},
			wantStatus: http.StatusPaymentRequired,
		},
		{
			// auth is the first call and is missing from the topology, so
			// callDownstream errors and the gateway responds 502.
			name:     "missing topology entry -> 502",
			scenario: "happy",
			topo: Topology{
				ServiceInventory: stub200.URL,
				ServicePayment:   stub200.URL,
				ServiceNotify:    stub200.URL,
			},
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			h := gatewayHandler(&http.Client{}, tt.topo)
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set(HeaderScenario, tt.scenario)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
