package driver

import "log/slog"

// options carries the resolved, per-driver configuration set by functional
// Option values. It is constructed at the driver's own constructor (constructor
// injection at the composition root) — there is no package-global logger and no
// slog.SetDefault.
type options struct {
	logger *slog.Logger
}

// Option configures a driver constructor (NewShell, NewHTTP).
type Option func(*options)

// WithLogger injects the slog.Logger a driver narrates through. A nil logger is
// treated as "use the silent default" so callers can pass through an
// unconditionally-resolved logger without a nil check.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// resolveOptions applies opts over a silent (discard-handler) default. The
// default keeps drivers silent unless a caller opts in, which is what preserves
// byte-identical happy-path output (SC-005).
func resolveOptions(opts []Option) options {
	o := options{logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
