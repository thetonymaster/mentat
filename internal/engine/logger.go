package engine

import (
	"io"
	"log/slog"
)

// NewLogger maps the two verbosity flags both binaries expose (-v/-vv, FR-001)
// onto a log/slog text handler writing to w (os.Stderr in production, a buffer
// in tests). It is the single shared constructor so cmd/mentat and cmd/mentatctl
// cannot drift on the level mapping (D6 anti-copy-paste):
//
//   - neither flag  -> slog.DiscardHandler, so the default happy path writes zero
//     bytes and stays byte-identical (SC-005);
//   - verbose only  -> Info level (Debug suppressed);
//   - debug (-vv)   -> Debug level; -vv wins when both flags are set.
//
// The logger is injected into the engine/correlate/driver seams via their
// WithLogger options; there is no package-global logger and no slog.SetDefault.
func NewLogger(w io.Writer, verbose, debug bool) *slog.Logger {
	if !verbose && !debug {
		return slog.New(slog.DiscardHandler)
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
