package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/store"
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

// TestBuildStoreResolvesFile proves the "file" store factory is registered at the
// composition root (invariant §3) and wired to cfg.StorePath: a dir with a fixture
// resolves to a *store.FileStore, while a storePath that cannot be read is a hard
// build error (the factory does not swallow it).
func TestBuildStoreResolvesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.json"),
		[]byte(`{"runScenario":"r","spans":[{"name":"root","parentIndex":-1}]}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	st, err := BuildStore(config.Config{Store: "file", StorePath: dir})
	if err != nil {
		t.Fatalf("BuildStore(file): %v", err)
	}
	if _, ok := st.(*store.FileStore); !ok {
		t.Fatalf("BuildStore(file): got %T, want *store.FileStore", st)
	}

	if _, err := BuildStore(config.Config{Store: "file", StorePath: filepath.Join(dir, "missing")}); err == nil {
		t.Fatalf("BuildStore(file) with unreadable storePath: want error, got nil")
	}
}
