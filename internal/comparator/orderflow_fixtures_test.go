package comparator

import (
	"context"
	"os"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/store"
)

func loadOrderflowGolden(t *testing.T, scenario string) *core.Evidence {
	t.Helper()
	data, err := os.ReadFile("../../testdata/traces/orderflow/" + scenario + ".json")
	if err != nil {
		t.Fatalf("read golden %q: %v", scenario, err)
	}
	tr, err := store.LoadFixture(data)
	if err != nil {
		t.Fatalf("load golden %q: %v", scenario, err)
	}
	return &core.Evidence{Trace: tr}
}

// TestOrderflowSequenceService asserts the portable service-sequence comparator
// against the real captured goldens (spec §9 L1).
func TestOrderflowSequenceService(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		exp      SequenceExpectation
		wantPass bool
	}{
		{
			name:     "happy: services in expected order",
			scenario: "happy",
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: true,
		},
		{
			name:     "happy: legacy-pricing never called",
			scenario: "happy",
			exp:      SequenceExpectation{Kind: "service", Forbidden: []string{"legacy-pricing"}},
			wantPass: true,
		},
		{
			name:     "reorder: payment-before-inventory trips order",
			scenario: "reorder",
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: false,
		},
		{
			name:     "legacy_path: forbidden legacy-pricing trips",
			scenario: "legacy_path",
			exp:      SequenceExpectation{Kind: "service", Forbidden: []string{"legacy-pricing"}},
			wantPass: false,
		},
		{
			name:     "inventory_out: payment/notify skipped trips order",
			scenario: "inventory_out",
			exp:      SequenceExpectation{Kind: "service", Order: []string{"auth", "inventory", "payment", "notify"}},
			wantPass: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := loadOrderflowGolden(t, tt.scenario)
			v, err := NewSequence().Compare(context.Background(), *ev, tt.exp)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

// TestOrderflowBudgetsError asserts the error-span budget against the goldens.
// payment_decline carries a payment.declined span with status "Error"; happy has
// none. (Latency is NOT testable here — goldens carry no timestamps; see the
// plan's reality-vs-model note. Latency is asserted at L3.)
func TestOrderflowBudgetsError(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		wantPass bool
	}{
		{"happy: zero error spans", "happy", true},
		{"payment_decline: one error span trips", "payment_decline", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := loadOrderflowGolden(t, tt.scenario)
			zero := 0
			v, err := NewBudgets(nil).Compare(context.Background(), *ev, BudgetExpectation{MaxErrors: &zero})
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}

// TestOrderflowResult asserts the result comparator against each scenario's
// defined gateway response (tracelab/orderflow/handlers.go::planFor).
func TestOrderflowResult(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		exp      ResultExpectation
		wantPass bool
	}{
		{
			name:     "happy: status 201",
			status:   201,
			body:     `{"status":"confirmed"}`,
			exp:      ResultExpectation{Matcher: "status", Want: "201"},
			wantPass: true,
		},
		{
			name:     "happy: body json-contains confirmed",
			status:   201,
			body:     `{"status":"confirmed"}`,
			exp:      ResultExpectation{Matcher: "json-subset", Want: `{"status":"confirmed"}`},
			wantPass: true,
		},
		{
			name:     "payment_decline: status 402 not 201",
			status:   402,
			body:     `{"status":"declined"}`,
			exp:      ResultExpectation{Matcher: "status", Want: "201"},
			wantPass: false,
		},
		{
			name:     "inventory_out: status 409 not 201",
			status:   409,
			body:     `{"status":"out_of_stock"}`,
			exp:      ResultExpectation{Matcher: "status", Want: "201"},
			wantPass: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ev := core.Evidence{Output: core.Output{Status: tt.status, Body: []byte(tt.body)}}
			v, err := NewResult().Compare(context.Background(), ev, tt.exp)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Errorf("Pass=%v want=%v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
		})
	}
}
