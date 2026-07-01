package judge

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/registry"
)

// TestRegisterBuiltins asserts the composition-root helper registers the built-in
// judge backends into the global registry, and that each registered factory builds
// a non-nil core.Judge from a default config without needing an API key — NewClaude
// defers the credential check to the first Judge call (judge-seam.md). Registration
// mutates the package-global registry, so this test stays serial (no t.Parallel).
func TestRegisterBuiltins(t *testing.T) {
	RegisterBuiltins()

	tests := []struct {
		name    string
		backend string
		wantOK  bool
	}{
		{name: "claude registered", backend: "claude", wantOK: true},
		{name: "unknown backend missing", backend: "nope-not-a-backend", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := registry.Judge(tt.backend)
			if ok != tt.wantOK {
				t.Fatalf("registry.Judge(%q) ok=%v, want %v", tt.backend, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			j, err := f(config.Config{})
			if err != nil {
				t.Fatalf("factory(%q) error = %v, want nil", tt.backend, err)
			}
			if j == nil {
				t.Fatalf("factory(%q) returned nil core.Judge, want non-nil", tt.backend)
			}
		})
	}
}

// TestRegisterBuiltinsIdempotent asserts calling RegisterBuiltins more than once is
// safe — the composition root and test setup may both call it. Like the sibling
// report.RegisterBuiltins / comparator.RegisterBuiltinMatchers, re-registration just
// overwrites the map entry, leaving the backend resolvable.
func TestRegisterBuiltinsIdempotent(t *testing.T) {
	RegisterBuiltins()
	RegisterBuiltins()
	if _, ok := registry.Judge("claude"); !ok {
		t.Fatalf(`registry.Judge("claude") not registered after repeated RegisterBuiltins`)
	}
}
