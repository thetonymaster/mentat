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
	status, body := ExpectedResult("happy")
	if status != 201 {
		t.Errorf("status = %d, want 201", status)
	}
	if string(body) != `{"status":"confirmed"}` {
		t.Errorf("body = %s, want confirmed JSON", body)
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
	// Happy path: all downstream stubs return 200; expect gateway 201.
	t.Run("happy path all 200 -> 201", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer stub.Close()

		topo := Topology{
			ServiceAuth:      stub.URL,
			ServiceInventory: stub.URL,
			ServicePayment:   stub.URL,
			ServiceNotify:    stub.URL,
		}

		h := gatewayHandler(&http.Client{}, topo)
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(HeaderScenario, "happy")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("status = %d, want 201", rec.Code)
		}
	})

	// Short-circuit: payment returns 402, gateway still returns 402 (its own plan status).
	t.Run("payment_decline stub 402 -> gateway 402 short-circuit", func(t *testing.T) {
		// Stub returns 402 for payment (simulates the downstream result).
		stub402 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPaymentRequired)
		}))
		defer stub402.Close()

		stub200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer stub200.Close()

		topo := Topology{
			ServiceAuth:      stub200.URL,
			ServiceInventory: stub200.URL,
			ServicePayment:   stub402.URL,
			// ServiceNotify intentionally absent; short-circuit should prevent calling it.
		}

		h := gatewayHandler(&http.Client{}, topo)
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(HeaderScenario, "payment_decline")
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		// The gateway's plan status for payment_decline is 402.
		if rec.Code != http.StatusPaymentRequired {
			t.Errorf("status = %d, want 402", rec.Code)
		}
	})

	// Error path: topology missing a required service -> 502.
	t.Run("missing topology entry -> 502", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer stub.Close()

		// auth is missing from topology — callDownstream will return an error.
		topo := Topology{
			ServiceInventory: stub.URL,
			ServicePayment:   stub.URL,
			ServiceNotify:    stub.URL,
		}

		h := gatewayHandler(&http.Client{}, topo)
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(HeaderScenario, "happy") // auth is first call
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})
}
