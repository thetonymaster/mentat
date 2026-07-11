package main

import (
	"testing"
	"time"
)

func TestParseDur(t *testing.T) {
	tests := []struct {
		name string
		s    string
		def  time.Duration
		want time.Duration
	}{
		{"empty uses default", "", 5 * time.Second, 5 * time.Second},
		{"parses valid duration", "200ms", time.Second, 200 * time.Millisecond},
		{"parses minutes", "2m", time.Second, 2 * time.Minute},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := parseDur(tt.s, tt.def)
			if got != tt.want {
				t.Fatalf("parseDur(%q, %v) = %v; want %v", tt.s, tt.def, got, tt.want)
			}
		})
	}
}

func TestOrDefault(t *testing.T) {
	tests := []struct {
		name string
		n    int
		def  int
		want int
	}{
		{"zero uses default", 0, 3, 3},
		{"non-zero uses n", 5, 3, 5},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := orDefault(tt.n, tt.def)
			if got != tt.want {
				t.Fatalf("orDefault(%d, %d) = %d; want %d", tt.n, tt.def, got, tt.want)
			}
		})
	}
}
