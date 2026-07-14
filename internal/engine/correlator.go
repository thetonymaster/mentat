package engine

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/correlate"
)

// Poll defaults — the single source of truth for both binaries (audit D6). Before
// this both mains carried copy-pasted literals that could silently drift.
const (
	defaultPollInterval  = 200 * time.Millisecond
	defaultPollTimeout   = 30 * time.Second
	defaultPollStableFor = 3
)

// pollConfig resolves cfg.Poll into a correlate.PollConfig, applying the named
// defaults for empty/zero fields. A malformed duration is a returned, field-named
// error — library code never os.Exit, unlike the old main.go parseDur helper.
func pollConfig(cfg config.Config) (correlate.PollConfig, error) {
	interval, err := parseDur(cfg.Poll.Interval, defaultPollInterval)
	if err != nil {
		return correlate.PollConfig{}, fmt.Errorf("poll.interval: %w", err)
	}
	timeout, err := parseDur(cfg.Poll.Timeout, defaultPollTimeout)
	if err != nil {
		return correlate.PollConfig{}, fmt.Errorf("poll.timeout: %w", err)
	}
	stableFor := cfg.Poll.StableFor
	if stableFor == 0 {
		stableFor = defaultPollStableFor
	}
	return correlate.PollConfig{Interval: interval, Timeout: timeout, StableFor: stableFor}, nil
}

// parseDur returns def for an empty string, else parses a Go duration. A malformed
// value is a returned error naming the bad input (not os.Exit).
func parseDur(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

// BuildCorrelator is the single correlator composition root shared by mentat and
// mentatctl (audit D6): it owns the uuid run-id source, the poll defaults, the
// store endpoint (for enriched not-found errors / narration), and the injected
// logger. Mirrors BuildStore — the engine still depends on core.Correlator, so
// constructing the concrete correlate.correlator here is composition-root wiring,
// not an interface-invariant violation.
func BuildCorrelator(cfg config.Config, logger *slog.Logger) (core.Correlator, error) {
	pc, err := pollConfig(cfg)
	if err != nil {
		return nil, err
	}
	return correlate.New(
		func() string { return uuid.NewString() },
		pc,
		correlate.WithLogger(logger),
		correlate.WithEndpoint(cfg.Tempo.Endpoint),
	), nil
}
