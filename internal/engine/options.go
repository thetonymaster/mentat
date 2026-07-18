package engine

import (
	"log/slog"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// options carries the resolved composition-root configuration set by functional
// Option values. It is constructed inside Build/BuildStore (constructor injection)
// — there is no package-global logger and no slog.SetDefault.
//
// The extra* slices carry custom-adapter registrations funneled from the public
// facade (spec 007 FR-002). All four seams are factories (built from config at the
// composition root, mirroring the built-in tempo/file/claude registration shape),
// so construction is deferred until AFTER the collision check — a colliding
// registration is rejected before ITS factory runs, so the caller sees the collision
// (not a factory error). (In a duplicate-name pair the first, non-colliding, entry
// is still constructed before the second is rejected.)
type options struct {
	logger           *slog.Logger
	extraDrivers     []namedDriver
	extraComparators []namedComparator
	extraJudges      []namedJudge
	extraStores      []namedStore
	extraMatchers    []namedMatcher
}

type namedDriver struct {
	name    string
	factory func(config.Config) (core.Driver, error)
}

type namedMatcher struct {
	name    string
	matcher core.Matcher
}

type namedComparator struct {
	name    string
	factory func(config.Config) (core.Comparator, error)
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

// WithExtraDriver funnels a custom driver factory from the public facade into the
// composition root under name. Build validates the name against the built-ins (and
// earlier extras) BEFORE invoking the factory, so a clashing name fails loudly with
// a collision error and never runs the factory (FR-002).
func WithExtraDriver(name string, f func(config.Config) (core.Driver, error)) Option {
	return func(o *options) { o.extraDrivers = append(o.extraDrivers, namedDriver{name: name, factory: f}) }
}

// WithExtraComparator funnels a custom comparator factory into the composition root
// under name, with the same defer-past-collision discipline as WithExtraDriver.
func WithExtraComparator(name string, f func(config.Config) (core.Comparator, error)) Option {
	return func(o *options) {
		o.extraComparators = append(o.extraComparators, namedComparator{name: name, factory: f})
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

// WithExtraMatcher registers a result Matcher under name into the engine's registry.
// Unlike the driver/comparator/judge/store seams, matchers have no public facade hook
// (they are an internal detail of the result comparator), so this is an internal
// composition/test hook — NOT part of the public extension surface. It is applied
// AFTER the built-in and "semantic" matchers, so it is OVERRIDE-capable
// (last-writer-wins, no collision check): a test can substitute a mock "semantic"
// matcher, and the semantic seam is (re)wired by name at Build.
func WithExtraMatcher(name string, m core.Matcher) Option {
	return func(o *options) { o.extraMatchers = append(o.extraMatchers, namedMatcher{name: name, matcher: m}) }
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
