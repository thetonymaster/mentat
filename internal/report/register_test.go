package report

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/registry"
)

func TestRegisterBuiltins(t *testing.T) {
	RegisterBuiltins()
	for _, name := range []string{"json", "html"} {
		if _, ok := registry.Reporter(name); !ok {
			t.Errorf("reporter %q not registered", name)
		}
	}
}
