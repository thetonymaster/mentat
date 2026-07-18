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

// wantBoundedQualifier5s is the canonical trace-completeness qualifier (contracts §3)
// for the http `checkout` orderflow target. That target declares no `completeness`
// block in mentat.yaml, so its adapter kind-default applies: http → request-scoped →
// effective settle 5s. The engine attaches this exact string (const qualifierBounded,
// single source of truth) to the completeness-sensitive `never called` verdict, and
// every emitted report renders it verbatim. Matching the whole string proves BOTH the
// qualifier text AND the effective settle value (the embedded "settle 5s").
const wantBoundedQualifier5s = "trace-completeness: bounded by ingestion window (settle 5s); spans exported later are not observed"

// TestCompletenessQualifierInReports is the feature-008 (US2, T020) live-harness proof
// that a bounded, request-scoped verdict states its weaker guarantee in the emitted
// reports (quickstart §4). It drives features/checkout.feature — whose
// `the service "legacy-pricing" is never called` step is an absence assertion, hence
// completeness-sensitive — against the http `checkout` target (request-scoped,
// non-strict → bounded contract). The absence assertion PASSES on the happy scenario,
// so this specifically exercises the pass-side qualifier: a green verdict still carries
// the ingestion-window caveat with the target's effective settle value (5s).
//
// Both emitted report formats that show verdict reasons are checked (SC-003): the JSON
// report is asserted structurally (ScenarioResult.Qualifiers), and the JUnit XML by the
// verbatim <system-out> the reporter emits on passing cases. Each subtest execs the
// prebuilt mentatBin writing its own report file, so the two Tempo-ingestion waits
// overlap. Requires: make harness-up (Tempo + Collector + orderflow containers).
func TestCompletenessQualifierInReports(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		flag   string // report flag; MUST sit between "run" and the feature path
		file   string // basename of the written report artifact
		assert func(t *testing.T, data []byte)
	}{
		{
			name: "json report carries the qualifier with settle 5s",
			flag: "--report-json",
			file: "report.json",
			assert: func(t *testing.T, data []byte) {
				var rep core.RunReport
				if err := json.Unmarshal(data, &rep); err != nil {
					t.Fatalf("invalid json in report: %v\nraw:\n%s", err, data)
				}
				if !hasQualifier(rep, wantBoundedQualifier5s) {
					t.Fatalf("no scenario carried the bounded qualifier %q (settle value missing or wrong);\nreport scenarios: %+v", wantBoundedQualifier5s, rep.Scenarios)
				}
			},
		},
		{
			name: "junit report carries the qualifier with settle 5s",
			flag: "--junit",
			file: "report.xml",
			assert: func(t *testing.T, data []byte) {
				// The JUnit reporter emits the qualifier verbatim as <system-out> on pass
				// AND fail (the <failure> body is fail-only), so a substring check on the
				// XML proves the exact canonical text + effective settle value survived.
				if !strings.Contains(string(data), wantBoundedQualifier5s) {
					t.Fatalf("junit report missing the bounded qualifier %q (settle value missing or wrong):\n%s", wantBoundedQualifier5s, data)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, c.file)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// Flag ordering: the report flag MUST precede the feature path — cmd/mentat
			// parses os.Args[2:] with the flag package, which stops at the first non-flag
			// positional argument. cmd.Dir = ".." resolves mentat.yaml + the feature path
			// from the repo root.
			cmd := exec.CommandContext(ctx, mentatBin, "run", c.flag, path, "features/checkout.feature")
			cmd.Dir = ".."
			out, err := cmd.CombinedOutput()

			if ctx.Err() == context.DeadlineExceeded {
				t.Fatalf("completeness qualifier(%s): checkout run timed out before the report was written:\n%s", c.flag, out)
			}
			// The happy checkout scenario passes (the qualifier rides a GREEN absence
			// verdict), so a non-zero exit is a genuine failure, not the expected outcome.
			if err != nil {
				t.Fatalf("completeness qualifier(%s): checkout run failed (want pass):\n%s", c.flag, out)
			}

			data, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("completeness qualifier(%s): report not written: %v\ncombined output:\n%s", c.flag, rerr, out)
			}
			c.assert(t, data)
		})
	}
}

// hasQualifier reports whether any scenario in the report carries the given qualifier
// string verbatim in its ScenarioResult.Qualifiers.
func hasQualifier(rep core.RunReport, want string) bool {
	for _, s := range rep.Scenarios {
		for _, q := range s.Qualifiers {
			if q == want {
				return true
			}
		}
	}
	return false
}
