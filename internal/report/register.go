package report

import "github.com/thetonymaster/mentat/internal/registry"

// RegisterBuiltins registers the built-in reporters at the composition root.
func RegisterBuiltins() {
	registry.RegisterReporter("json", jsonReporter{})
	registry.RegisterReporter("html", htmlReporter{})
}
