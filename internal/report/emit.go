package report

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// EmitReports writes each requested report format atomically. targets maps a
// registered reporter name (json/html/junit) to its output path. Each file is
// written to a temp file in the same directory and renamed into place, so a run
// interrupted mid-write never leaves a truncated report (feature 003, FR-006
// atomicity). An unknown reporter or any write/rename failure is returned wrapped
// (no silent fallback); the caller turns it into a non-zero exit.
func EmitReports(rep core.RunReport, targets map[string]string) error {
	for name, path := range targets {
		r, ok := registry.Reporter(name)
		if !ok {
			return fmt.Errorf("unknown reporter %q", name)
		}
		if err := emitAtomic(r, rep, path); err != nil {
			return fmt.Errorf("writing %s report %q: %w", name, path, err)
		}
	}
	return nil
}

// emitAtomic renders rep through r into a temp file in path's directory, then
// renames it to path. On any failure the temp file is removed and path is left
// untouched, so a partial render never becomes the final artifact.
func emitAtomic(r core.Reporter, rep core.RunReport, path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Until a successful rename, any early return must clean up the temp file.
	renamed := false
	defer func() {
		if !renamed {
			_ = tmp.Close() // may already be closed; ignore
			_ = os.Remove(tmpName)
		}
	}()

	if err := r.Report(rep, tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %q to %q: %w", tmpName, path, err)
	}
	renamed = true
	return nil
}
