package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/store"
	"github.com/thetonymaster/mentat/internal/trace"
)

// TestBuildStoreAppliesExtraStore pins the store half of FR-002: a facade-funneled
// store factory (WithExtraStore) is registered and resolvable by name, and a name
// colliding with a built-in (tempo/file) or an earlier extra fails loudly naming the
// seam and the conflicting name — never a silent last-wins overwrite (Constitution IV).
//
// No t.Parallel(): BuildStore mutates the registry's package-global maps.
func TestBuildStoreAppliesExtraStore(t *testing.T) {
	sf := func(config.Config) (core.TraceStore, error) {
		return store.NewInMemStore(map[string]*trace.Trace{}), nil
	}

	tests := []struct {
		name       string
		storeName  string // cfg.Store
		opts       []Option
		wantErrSub []string // nil ⇒ BuildStore succeeds and resolves the custom store
	}{
		{name: "custom store resolves by name", storeName: "xstore", opts: []Option{WithExtraStore("xstore", sf)}},
		{name: "store collides with built-in", storeName: "file", opts: []Option{WithExtraStore("file", sf)}, wantErrSub: []string{"WithStore", "file"}},
		{name: "store collides with earlier extra", storeName: "dup-s", opts: []Option{WithExtraStore("dup-s", sf), WithExtraStore("dup-s", sf)}, wantErrSub: []string{"WithStore", "dup-s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := BuildStore(config.Config{Store: tt.storeName}, tt.opts...)
			if len(tt.wantErrSub) == 0 {
				if err != nil {
					t.Fatalf("BuildStore with extra store: %v", err)
				}
				if st == nil {
					t.Fatal("BuildStore resolved a nil custom store")
				}
				return
			}
			if err == nil {
				t.Fatalf("BuildStore must reject %s, got nil error", tt.name)
			}
			for _, sub := range tt.wantErrSub {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("BuildStore error = %q, want substring %q", err, sub)
				}
			}
		})
	}
}

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
