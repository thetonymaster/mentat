package report

import "github.com/thetonymaster/mentat/internal/registry"

// RegisterBuiltins registers the built-in reporters into the per-engine registry at the
// composition root.
//
// Before feature 010 this took no argument and wrote to a package-global map. Reporters
// are now per-engine like every other seam, so registration is explicit and scoped to
// one Run — which is what makes a caller-supplied WithReporter safe against concurrent
// runs (D4).
func RegisterBuiltins(reg *registry.Registry) {
	reg.RegisterReporter("json", jsonReporter{})
	reg.RegisterReporter("html", htmlReporter{})
	reg.RegisterReporter("junit", junitReporter{})
}
