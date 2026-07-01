package judge

import "github.com/thetonymaster/mentat/internal/registry"

// RegisterBuiltins registers the built-in judge backends into the global registry.
// It is called at the composition root (engine.Build) and in test setup. Like the
// sibling report.RegisterBuiltins / comparator.RegisterBuiltinMatchers, registration
// is a map write, so calling it more than once simply re-registers the same factory.
func RegisterBuiltins() {
	registry.RegisterJudge("claude", NewClaude)
}
