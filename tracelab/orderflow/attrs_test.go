package orderflow

import (
	"reflect"
	"testing"
)

func TestScenariosAreTheSixKnownNames(t *testing.T) {
	want := []string{"happy", "payment_decline", "inventory_out", "slow", "legacy_path", "reorder"}
	if got := Scenarios(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Scenarios() = %v, want %v", got, want)
	}
}
