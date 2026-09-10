package report

import (
	"testing"

	"github.com/thetonymaster/mentat/internal/registry"
)

func TestRegisterBuiltins(t *testing.T) {
	reg := registry.New()
	RegisterBuiltins(reg)
	for _, name := range []string{"json", "html"} {
		if _, ok := reg.Reporter(name); !ok {
			t.Errorf("reporter %q not registered", name)
		}
	}
}
