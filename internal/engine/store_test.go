package engine

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
)

func TestBuildStore(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		wantErr bool
	}{
		{
			name:    "resolves tempo",
			cfg:     config.Config{Store: "tempo", Tempo: config.Endpoint{Endpoint: "http://localhost:3200"}},
			wantErr: false,
		},
		{
			name:    "unknown store errors",
			cfg:     config.Config{Store: "telepathy"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			st, err := BuildStore(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BuildStore(%q): want error, got nil", tt.cfg.Store)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildStore(%q): %v", tt.cfg.Store, err)
			}
			if st == nil {
				t.Fatalf("BuildStore(%q): returned nil store", tt.cfg.Store)
			}
		})
	}
}
