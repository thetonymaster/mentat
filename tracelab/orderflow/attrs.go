// Package orderflow is a dual-mode microservices SUT for Mentat Phase 2.
package orderflow

// Service names. Each runs with its own TracerProvider / service.name.
const (
	ServiceGateway   = "gateway"
	ServiceAuth      = "auth"
	ServiceInventory = "inventory"
	ServicePayment   = "payment"
	ServiceNotify    = "notify"
	ServiceLegacy    = "legacy-pricing"
)

// HeaderScenario selects scenario behaviour per request.
const HeaderScenario = "X-Scenario"

// Correlation baggage keys copied onto every span (see BaggageSpanProcessor).
const (
	BaggageRunID    = "test.run.id"
	BaggageScenario = "test.scenario"
)

// Scenarios lists the supported scenarios in deterministic order.
func Scenarios() []string {
	return []string{"happy", "payment_decline", "inventory_out", "slow", "legacy_path", "reorder"}
}
