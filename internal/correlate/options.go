package correlate

import "log/slog"

// options carries the resolved, per-correlator configuration set by functional
// Option values. It is constructed inside New (constructor injection at the
// composition root) — there is no package-global logger and no slog.SetDefault.
type options struct {
	logger   *slog.Logger
	endpoint string
}

// Option configures the correlator constructor New.
type Option func(*options)

// WithLogger injects the slog.Logger the correlator narrates through. A nil
// logger is treated as "use the silent default" so callers can pass through an
// unconditionally-resolved logger without a nil check.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithEndpoint records the trace-store endpoint the correlator queries. It is
// named in the enriched not-found/timeout errors (store:/query: lines) and in the
// resolve.start narration so a correlation failure is diagnosable from the output.
func WithEndpoint(endpoint string) Option {
	return func(o *options) { o.endpoint = endpoint }
}

// resolveOptions applies opts over a silent (discard-handler) default so the
// correlator narrates nothing unless a caller opts in (SC-005).
func resolveOptions(opts []Option) options {
	o := options{logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
