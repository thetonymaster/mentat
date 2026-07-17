//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestL3_ReportReflectsFailure proves that a failing run writes a JSON report
// whose Failed counter is > 0. It reuses the deterministic-fail fixture
// aggregate_scalar_bad.feature (p95 over checkout @runs(2)) which is proven to
// go red by TestAggregateScalarGoesRed.
func TestL3_ReportReflectsFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "r.json")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Flag ordering: flags MUST come between "run" and the feature path because
	// cmd/mentat/main.go does fs.Parse(os.Args[2:]) and Go's flag package stops
	// at the first non-flag positional argument.
	cmd := exec.CommandContext(ctx, mentatBin,
		"run", "--report-json", jsonPath,
		"features/meta/aggregate_scalar_bad.feature",
	)
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("run timed out before report was written:\n%s", out)
	}
	// The process exits non-zero because the scenario fails — that is expected.
	// We do not assert err == nil here; we assert on report content instead.
	_ = err

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("report not written: %v\ncombined output:\n%s", err, out)
	}
	var rep core.RunReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("invalid json in report: %v\nraw:\n%s", err, data)
	}
	if rep.Failed == 0 {
		t.Fatalf("expected Failed > 0 in report; got Failed=%d, Passed=%d\nraw:\n%s", rep.Failed, rep.Passed, data)
	}
	t.Logf("report OK: Total=%d Passed=%d Failed=%d", rep.Total, rep.Passed, rep.Failed)
}

// TestL3_UnwritableReportExitsNonZero proves that an unwritable --report-json
// path makes mentat exit non-zero with an error message containing
// "create json report" (from emitReports in cmd/mentat/main.go).
func TestL3_UnwritableReportExitsNonZero(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, mentatBin,
		"run", "--report-json", "/this/dir/does/not/exist/r.json",
		"features/meta/aggregate_scalar_bad.feature",
	)
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("run timed out:\n%s", out)
	}
	if err == nil {
		t.Fatalf("expected non-zero exit for unwritable report path, but mentat passed:\n%s", out)
	}
	outStr := string(out)
	// Feature 003 routes reports through report.EmitReports (atomic temp+rename), so
	// an unwritable directory fails at temp-file creation, wrapped "writing json report".
	const wantSubstr = "writing json report"
	if !strings.Contains(outStr, wantSubstr) {
		t.Fatalf("expected %q in combined output (proves unwritable path caused exit):\n%s", wantSubstr, outStr)
	}
	t.Logf("non-zero exit confirmed with message: %s", wantSubstr)
}
