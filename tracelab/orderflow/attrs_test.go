package orderflow

import (
	"reflect"
	"testing"
)

func TestScenariosAreTheSixKnownNames(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{
			name: "all six known scenarios in order",
			want: []string{"happy", "payment_decline", "inventory_out", "slow", "legacy_path", "reorder"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := Scenarios(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Scenarios() = %v, want %v", got, tt.want)
			}
		})
	}
}
