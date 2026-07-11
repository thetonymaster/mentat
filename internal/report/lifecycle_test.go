package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// Feature 003 (US2): the interrupted marker must render in every emitted format,
// and report emission must be atomic (temp + rename — a killed run never leaves a
// truncated file).

func TestInterruptedMarkerJSON(t *testing.T) {
	tests := []struct {
		name        string
		interrupted bool
		wantSub     string
		absentSub   string
	}{
		{name: "interrupted true renders the marker", interrupted: true, wantSub: `"interrupted": true`},
		{name: "clean run omits the marker", interrupted: false, absentSub: `"interrupted"`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			rep := core.RunReport{Total: 1, Passed: 1, Interrupted: tt.interrupted}
			if err := (jsonReporter{}).Report(rep, &buf); err != nil {
				t.Fatalf("Report: %v", err)
			}
			if tt.wantSub != "" && !strings.Contains(buf.String(), tt.wantSub) {
				t.Fatalf("json missing %q:\n%s", tt.wantSub, buf.String())
			}
			if tt.absentSub != "" && strings.Contains(buf.String(), tt.absentSub) {
				t.Fatalf("clean json should omit %q:\n%s", tt.absentSub, buf.String())
			}
		})
	}
}

func TestInterruptedBannerHTML(t *testing.T) {
	interrupted := core.RunReport{Total: 2, Passed: 1, Failed: 1, Interrupted: true,
		Scenarios: []core.ScenarioResult{{Name: "a", Pass: true}, {Name: "b", Pass: false, Reasons: []string{"x"}}}}
	clean := core.RunReport{Total: 1, Passed: 1,
		Scenarios: []core.ScenarioResult{{Name: "a", Pass: true}}}

	var ib, cb bytes.Buffer
	if err := (htmlReporter{}).Report(interrupted, &ib); err != nil {
		t.Fatalf("Report(interrupted): %v", err)
	}
	if err := (htmlReporter{}).Report(clean, &cb); err != nil {
		t.Fatalf("Report(clean): %v", err)
	}
	if !strings.Contains(ib.String(), `class="interrupted"`) {
		t.Fatalf("interrupted html missing the banner:\n%s", ib.String())
	}
	if strings.Contains(cb.String(), `class="interrupted"`) {
		t.Fatalf("clean html must not show the interrupted banner:\n%s", cb.String())
	}
}

func TestJUnitReporterMarkerAndShape(t *testing.T) {
	interrupted := core.RunReport{Total: 2, Passed: 1, Failed: 1, Interrupted: true,
		Scenarios: []core.ScenarioResult{
			{Name: "ok", Pass: true},
			{Name: "boom", Pass: false, Reasons: []string{"assertion failed: rate too low"}},
		}}
	clean := core.RunReport{Total: 1, Passed: 1,
		Scenarios: []core.ScenarioResult{{Name: "ok", Pass: true}}}

	var ib, cb bytes.Buffer
	if err := (junitReporter{}).Report(interrupted, &ib); err != nil {
		t.Fatalf("Report(interrupted): %v", err)
	}
	if err := (junitReporter{}).Report(clean, &cb); err != nil {
		t.Fatalf("Report(clean): %v", err)
	}
	for _, want := range []string{
		"<testsuite", `name="boom"`, "<failure", "rate too low",
		`name="interrupted" value="true"`,
	} {
		if !strings.Contains(ib.String(), want) {
			t.Fatalf("junit missing %q:\n%s", want, ib.String())
		}
	}
	if strings.Contains(cb.String(), `name="interrupted"`) {
		t.Fatalf("clean junit must not carry the interrupted property:\n%s", cb.String())
	}
	// Both outputs must be well-formed XML.
	if err := xml.Unmarshal(ib.Bytes(), new(any)); err != nil {
		t.Fatalf("interrupted junit is not well-formed xml: %v", err)
	}
}

func TestEmitReportsAllFormats(t *testing.T) {
	RegisterBuiltins() // idempotent; ensures json/html/junit are registered
	dir := t.TempDir()
	rep := core.RunReport{Total: 1, Failed: 1, Interrupted: true,
		Scenarios: []core.ScenarioResult{{Name: "b", Pass: false, Reasons: []string{"boom"}}}}
	targets := map[string]string{
		"json":  filepath.Join(dir, "r.json"),
		"html":  filepath.Join(dir, "r.html"),
		"junit": filepath.Join(dir, "r.xml"),
	}
	if err := EmitReports(rep, targets); err != nil {
		t.Fatalf("EmitReports: %v", err)
	}
	for name, path := range targets {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s report not written: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s report is empty", name)
		}
	}
	data, _ := os.ReadFile(targets["json"])
	var round core.RunReport
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("json report invalid: %v", err)
	}
	if !round.Interrupted {
		t.Fatalf("json report lost the interrupted marker")
	}
}

func TestEmitReportsUnknownReporterErrors(t *testing.T) {
	dir := t.TempDir()
	err := EmitReports(core.RunReport{}, map[string]string{"nope": filepath.Join(dir, "x")})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected an unknown-reporter error naming %q, got %v", "nope", err)
	}
}

// failingReporter writes some bytes then fails, to prove atomicity: a mid-write
// failure must leave NO final report file (only a temp, which is cleaned up).
type failingReporter struct{}

func (failingReporter) Report(_ core.RunReport, w io.Writer) error {
	_, _ = w.Write([]byte("partial garbage"))
	return errors.New("boom mid-encode")
}

func TestEmitReportsAtomicNoPartialFileOnError(t *testing.T) {
	registry.RegisterReporter("failing-emit-test", failingReporter{})
	dir := t.TempDir()
	path := filepath.Join(dir, "out.xml")
	if err := EmitReports(core.RunReport{}, map[string]string{"failing-emit-test": path}); err == nil {
		t.Fatal("expected an error from the failing reporter")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("atomicity violated: final report path exists after a failed write (stat: %v)", statErr)
	}
	// The temp files must not leak either.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temp files leaked in target dir: %v", entries)
	}
}
