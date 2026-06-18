package orderflow

import (
	"encoding/json"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// slowDelay is the fixed latency the `slow` scenario injects, chosen to breach a
// sub-second SLO budget deterministically.
const slowDelay = 900 * time.Millisecond

// gatewayPlan is the ordered downstream services the gateway calls for a
// scenario, plus the gateway's own response.
type gatewayPlan struct {
	calls  []string
	status int
	body   map[string]string
}

func planFor(scenario string) gatewayPlan {
	switch scenario {
	case "happy", "slow":
		return gatewayPlan{[]string{ServiceAuth, ServiceInventory, ServicePayment, ServiceNotify}, http.StatusCreated, map[string]string{"status": "confirmed"}}
	case "payment_decline":
		return gatewayPlan{[]string{ServiceAuth, ServiceInventory, ServicePayment}, http.StatusPaymentRequired, map[string]string{"status": "declined"}}
	case "inventory_out":
		return gatewayPlan{[]string{ServiceAuth, ServiceInventory}, http.StatusConflict, map[string]string{"status": "out_of_stock"}}
	case "legacy_path":
		return gatewayPlan{[]string{ServiceAuth, ServiceLegacy, ServiceInventory, ServicePayment, ServiceNotify}, http.StatusCreated, map[string]string{"status": "confirmed"}}
	case "reorder":
		return gatewayPlan{[]string{ServiceAuth, ServicePayment, ServiceInventory, ServiceNotify}, http.StatusCreated, map[string]string{"status": "confirmed"}}
	default:
		return gatewayPlan{nil, http.StatusBadRequest, map[string]string{"status": "unknown_scenario"}}
	}
}

// ExpectedResult is the gateway's deterministic response per scenario — the
// boundary Output the framework's result comparator will assert against.
func ExpectedResult(scenario string) (int, []byte) {
	p := planFor(scenario)
	body, _ := json.Marshal(p.body)
	return p.status, body
}

func gatewayHandler(client *http.Client, topo Topology) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scenario := r.Header.Get(HeaderScenario)
		p := planFor(scenario)
		if scenario == "slow" {
			time.Sleep(slowDelay)
		}
		for _, svc := range p.calls {
			status, _, err := callDownstream(r.Context(), client, topo, svc, scenario)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			if status >= http.StatusBadRequest {
				break // short-circuit: a failed downstream stops the flow
			}
		}
		writeJSON(w, p.status, p.body)
	}
}

func leafHandler(service string, tr oteltrace.Tracer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scenario := r.Header.Get(HeaderScenario)
		switch {
		case service == ServicePayment && scenario == "payment_decline":
			// Explicit child error span so the error is independent of how
			// otelhttp maps the 402 status onto the server span.
			_, span := tr.Start(r.Context(), "payment.declined")
			span.SetStatus(codes.Error, "payment declined")
			span.End()
			writeJSON(w, http.StatusPaymentRequired, map[string]string{service: "declined"})
		case service == ServiceInventory && scenario == "inventory_out":
			writeJSON(w, http.StatusConflict, map[string]string{service: "out"})
		default:
			writeJSON(w, http.StatusOK, map[string]string{service: "ok"})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
