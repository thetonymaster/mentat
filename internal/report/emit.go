package report

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thetonymaster/mentat/internal/result"
)

// ReporterResolver is the slice of the registry EmitReports needs: look a reporter up
// by name, and list what is registered so an unknown name can name the alternatives.
// Consumer-defined and deliberately two methods wide — emission has no business holding
// a whole *registry.Registry, and this keeps the dependency inspectable (Constitution
// III: interfaces small, defined by the consumer).
type ReporterResolver interface {
	Reporter(name string) (result.Reporter, bool)
	Reporters() []string
}

// EmitReports writes each requested report format atomically. targets maps a
// registered reporter name (json/html/junit) to its output path. Each file is
// written to a temp file in the same directory and renamed into place, so a run
// interrupted mid-write never leaves a truncated report (feature 003, FR-006
// atomicity). An unknown reporter or any write/rename failure is returned wrapped
// (no silent fallback); the caller turns it into a non-zero exit.
//
// Every target is attempted in deterministic (sorted-name) order and each failure
// is collected, so one invalid destination never prevents a valid report from being
// written (map order is otherwise nondeterministic). The collected failures are
// returned via errors.Join — nil when none, and byte-identical to the single wrapped
// error for a single-target caller.
func EmitReports(rep result.Results, targets map[string]string, resolve ReporterResolver) error {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		path := targets[name]
		r, ok := resolve.Reporter(name)
		if !ok {
			errs = append(errs, fmt.Errorf("unknown reporter %q (registered: %s)", name, strings.Join(resolve.Reporters(), ", ")))
			continue
		}
		if err := emitAtomic(r, rep, path); err != nil {
			errs = append(errs, fmt.Errorf("writing %s report %q: %w", name, path, err))
		}
	}
	return errors.Join(errs...)
}

// emitAtomic renders rep through r into a temp file in path's directory, then
// renames it to path. On any failure the temp file is removed and path is left
// untouched, so a partial render never becomes the final artifact.
func emitAtomic(r result.Reporter, rep result.Results, path string) error {
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
