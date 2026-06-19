package engine

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
)

func TestToPricing(t *testing.T) {
	t.Run("empty maps to nil", func(t *testing.T) {
		if got := toPricing(nil); got != nil {
			t.Fatalf("toPricing(nil) = %v, want nil", got)
		}
		if got := toPricing(config.Pricing{}); got != nil {
			t.Fatalf("toPricing(empty) = %v, want nil", got)
		}
	})
	t.Run("converts rates", func(t *testing.T) {
		in := config.Pricing{"m": {InputPerMTok: 3, OutputPerMTok: 15}}
		got := toPricing(in)
		r, ok := got["m"]
		if !ok {
			t.Fatalf("missing model m in %v", got)
		}
		if r.InputPerMTok != 3 || r.OutputPerMTok != 15 {
			t.Fatalf("rate = %+v, want {3 15}", r)
		}
	})
}
