package judge

import "github.com/thetonymaster/mentat/internal/registry"

// RegisterBuiltins registers the built-in judge backends into reg (the per-engine
// registry). It is called at the composition root (engine.Build) and in test setup.
// Like the sibling comparator.RegisterBuiltinMatchers, registration is a map write, so
// calling it more than once simply re-registers the same factory.
func RegisterBuiltins(reg *registry.Registry) {
	reg.RegisterJudge("claude", NewClaude)
}
