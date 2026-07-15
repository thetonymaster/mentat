package engine

import "log/slog"

// options carries the resolved composition-root configuration set by functional
// Option values. It is constructed inside Build (constructor injection) — there
// is no package-global logger and no slog.SetDefault.
type options struct {
	logger *slog.Logger
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

// resolveOptions applies opts over a silent (discard-handler) default so the
// engine and its drivers narrate nothing unless a caller opts in (SC-005).
func resolveOptions(opts []Option) options {
	o := options{logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
