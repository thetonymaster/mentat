package engine

import (
	"log/slog"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// options carries the resolved composition-root configuration set by functional
// Option values. It is constructed inside Build/BuildStore (constructor injection)
// — there is no package-global logger and no slog.SetDefault.
//
// The extra* slices carry custom-adapter registrations funneled from the public
// facade (spec 007 FR-002). Drivers and comparators are shared instances (built by
// the facade before Build); stores and judges are factories (stateful seams built
// from config at the composition root, mirroring the built-in tempo/file/claude
// registration shape). They are applied in order after the built-ins so a name
// colliding with a built-in — or an earlier extra — is caught (see Build/BuildStore).
type options struct {
	logger           *slog.Logger
	extraDrivers     []namedDriver
	extraComparators []namedComparator
	extraJudges      []namedJudge
	extraStores      []namedStore
}

type namedDriver struct {
	name   string
	driver core.Driver
}

type namedComparator struct {
	name       string
	comparator core.Comparator
}

type namedJudge struct {
	name    string
	factory registry.JudgeFactory
}

type namedStore struct {
	name    string
	factory registry.StoreFactory
}

// Option configures Build, the single composition root.
type Option func(*options)

// WithLogger injects the slog.Logger the engine — and the drivers it constructs —
// narrate through. A nil logger is treated as "use the silent default" so callers
// can pass through an unconditionally-resolved logger without a nil check.
//
// Build receives the correlator as a parameter it never invokes, so the logger
// does NOT reach the correlator through Build; the correlator is injected its own
// logger at the composition root via correlate.WithLogger.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithExtraDriver funnels a custom driver instance from the public facade into the
// composition root under name. Build registers it after the built-ins, so a name
// clashing with a built-in (or an earlier extra) fails loudly (FR-002).
func WithExtraDriver(name string, d core.Driver) Option {
	return func(o *options) { o.extraDrivers = append(o.extraDrivers, namedDriver{name: name, driver: d}) }
}

// WithExtraComparator funnels a custom comparator instance into the composition
// root under name, with the same collision discipline as WithExtraDriver.
func WithExtraComparator(name string, c core.Comparator) Option {
	return func(o *options) {
		o.extraComparators = append(o.extraComparators, namedComparator{name: name, comparator: c})
	}
}

// WithExtraJudge funnels a custom judge factory into the composition root under
// name. Like the built-in "claude" backend it is a factory: Build resolves it only
// when cfg.Judge.Backend names it, but the collision check runs unconditionally.
func WithExtraJudge(name string, f registry.JudgeFactory) Option {
	return func(o *options) { o.extraJudges = append(o.extraJudges, namedJudge{name: name, factory: f}) }
}

// WithExtraStore funnels a custom store factory into the store composition root
// (BuildStore) under name, with the same collision discipline as the other seams.
func WithExtraStore(name string, f registry.StoreFactory) Option {
	return func(o *options) { o.extraStores = append(o.extraStores, namedStore{name: name, factory: f}) }
}

// resolveOptions applies opts over a silent (discard-handler) default so the
// engine and its drivers narrate nothing unless a caller opts in (SC-005).
func resolveOptions(opts []Option) options {
	o := options{logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
