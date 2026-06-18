package main

import (
	"testing"
)

func TestParseTopology(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		wantLen   int
		wantErr   bool
		wantValue map[string]string // checked when wantErr is false
	}{
		{
			name:      "valid pairs",
			env:       "gateway=http://gateway:8080,auth=http://auth:8081",
			wantLen:   2,
			wantValue: map[string]string{"gateway": "http://gateway:8080", "auth": "http://auth:8081"},
		},
		{
			name:      "surrounding whitespace is trimmed",
			env:       " gateway = http://gateway:8080 , auth=http://auth:8081 ",
			wantLen:   2,
			wantValue: map[string]string{"gateway": "http://gateway:8080", "auth": "http://auth:8081"},
		},
		{
			name:    "empty env",
			env:     "",
			wantErr: true,
		},
		{
			name:    "missing equals",
			env:     "gateway",
			wantErr: true,
		},
		{
			name:    "empty value",
			env:     "gateway=",
			wantErr: true,
		},
		{
			name:    "empty key",
			env:     "=http://gateway:8080",
			wantErr: true,
		},
		{
			name:    "duplicate key",
			env:     "gateway=http://a:8080,gateway=http://b:8080",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTopology(tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseTopology(%q) = %v, want error", tt.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTopology(%q) unexpected error: %v", tt.env, err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d (%v)", len(got), tt.wantLen, got)
			}
			for k, v := range tt.wantValue {
				if got[k] != v {
					t.Errorf("topo[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
