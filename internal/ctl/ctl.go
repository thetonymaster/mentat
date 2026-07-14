package ctl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

// RunOpts is the parsed input for `mentatctl agent run`.
type RunOpts struct {
	Target   string
	Scenario string
	Prompt   string
	JSON     bool
	Quiet    bool
	Save     string // fixture name; empty = don't save
}

// LastPath is where the most recent interactive run id is cached. Used by --last.
// It is for interactive single runs only — never read by the `mentat` suite runner
// (see CLAUDE.md known limitations).
func LastPath() string {
	return filepath.Join(os.Getenv("HOME"), ".mentat", "last")
}

func SaveLast(runID string) error {
	p := LastPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("ctl: mkdir %s: %w", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(runID+"\n"), 0o644); err != nil {
		return fmt.Errorf("ctl: write last: %w", err)
	}
	return nil
}

func ReadLast() (string, error) {
	b, err := os.ReadFile(LastPath())
	if err != nil {
		return "", fmt.Errorf("ctl: no recent run (run `mentatctl agent run` first): %w", err)
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("ctl: recorded last run is empty")
	}
	return id, nil
}

// Resolve fetches and merges a SAVED run's trace forest by run id (no driving).
// Every mentatctl call site (trace/tools/services/diff) operates on historical
// run ids, so this uses the correlator's known-complete mode: one fetch pass,
// no stability sleep; absence still errors descriptively (feature 004, FR-004).
func Resolve(ctx context.Context, cor core.Correlator, st core.TraceStore, runID string) (*trace.Trace, error) {
	return cor.ResolveComplete(ctx, st, runID)
}
