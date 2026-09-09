package report

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// The emitted report format is a compatibility promise to everyone parsing a Mentat report
// — CI pipelines reading the JSON, dashboards ingesting the JUnit XML — and until spec 010
// nothing in the repo protected it. The surface golden does not render struct tags
// (stability.md boundary 2), and json_test.go round-trips a RunReport through
// encode/decode, which is symmetric and therefore structurally incapable of noticing a
// renamed tag.
//
// These goldens close that hole (010 FR-013, SC-009). They are committed BEFORE the
// reporters are touched, so they record today's bytes; the whole point is that a later
// refactor cannot quietly change them. Regenerate with:
//
//	MENTAT_UPDATE_GOLDEN=1 go test ./internal/report/ -run TestReportFormatGolden
//
// only when a format change is intended — never to make a red test green. A red result
// during the 010 type move means the transcription is wrong, not the golden.
//
// # Falsification rehearsal (010 T007, observed 2026-09-09)
//
// The guard was proven to fail before it was trusted. Two deliberate breaks, both reverted:
//
//  1. Renamed one tag, `json:"qualifiers,omitempty"` -> `json:"quals,omitempty"`:
//
//     --- FAIL: TestReportFormatGolden/full/json
//     This is a CHANGE TO THE EMITTED REPORT FORMAT, which users parse.
//     "quals": [        <- got
//     "qualifiers": [   <- want
//
//     Only the json case failed; html and junit do not serialize through struct tags.
//
//  2. Swapped two UNtagged adjacent fields, ScenarioResult.Cost and .Sequence — a pure
//     reorder with no tag change:
//
//     --- FAIL: TestReportFormatGolden/full/json
//     --- FAIL: TestReportFormatGolden/interrupted/json
//     "Sequence": null,  <- got, keys transposed
//     "Cost": 0.0125,
//
// Case 2 is the reason this file exists. Running TestPublicSurfaceGolden against that same
// swap gives:
//
//	ok  github.com/thetonymaster/mentat
//
// The surface gate PASSES. core.ScenarioResult is reached only as the type of a field, so
// its own field set is not expanded into the golden (stability.md boundary 3) — a reorder
// there changes every user's JSON key order and produces zero surface-golden diff. The two
// gates are complementary and neither subsumes the other.
const formatGoldenUpdateEnv = "MENTAT_UPDATE_GOLDEN"

// reporterFor returns the built-in reporter registered under name. It resolves through the
// package's own types rather than the registry so the golden does not depend on
// registration order or on RegisterBuiltins having run.
func reporterFor(t *testing.T, name string) core.Reporter {
	t.Helper()
	switch name {
	case "json":
		return jsonReporter{}
	case "html":
		return htmlReporter{}
	case "junit":
		return junitReporter{}
	default:
		t.Fatalf("unknown built-in reporter %q", name)
		return nil
	}
}

func formatGoldenCases() []struct {
	name   string
	format string
	report core.RunReport
	golden string
} {
	return []struct {
		name   string
		format string
		report core.RunReport
		golden string
	}{
		{"full/json", "json", fullFixture(), "report-full.json.golden"},
		{"full/html", "html", fullFixture(), "report-full.html.golden"},
		{"full/junit", "junit", fullFixture(), "report-full.junit.golden"},
		{"interrupted/json", "json", interruptedFixture(), "report-interrupted.json.golden"},
		{"interrupted/html", "html", interruptedFixture(), "report-interrupted.html.golden"},
		{"interrupted/junit", "junit", interruptedFixture(), "report-interrupted.junit.golden"},
	}
}

// TestReportFormatGolden pins the exact bytes each built-in reporter emits — field names,
// serialization tags, key order and omission behaviour included.
func TestReportFormatGolden(t *testing.T) {
	t.Parallel()

	for _, tt := range formatGoldenCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := reporterFor(t, tt.format).Report(tt.report, &buf); err != nil {
				t.Fatalf("%s reporter: %v", tt.format, err)
			}
			got := buf.Bytes()
			path := filepath.Join("testdata", tt.golden)

			if os.Getenv(formatGoldenUpdateEnv) != "" {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("update golden %q: %v", path, err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %q: %v (regenerate with %s=1)", path, err, formatGoldenUpdateEnv)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s report bytes differ from %s.\n"+
					"This is a CHANGE TO THE EMITTED REPORT FORMAT, which users parse.\n"+
					"If it was not intended, fix the code — do not regenerate the golden.\n"+
					"--- got ---\n%s\n--- want ---\n%s", tt.format, path, got, want)
			}
		})
	}
}

// TestReportFormatGoldenIsDeterministic proves the fixtures render identically twice in a
// row, so the golden above is a real guard and not a flake waiting to happen. None of the
// report types contains a map, and every time value in the fixtures is literal — this test
// is what makes that a checked fact rather than an assumption.
func TestReportFormatGoldenIsDeterministic(t *testing.T) {
	t.Parallel()

	for _, tt := range formatGoldenCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var first, second bytes.Buffer
			r := reporterFor(t, tt.format)
			if err := r.Report(tt.report, &first); err != nil {
				t.Fatalf("first render: %v", err)
			}
			if err := r.Report(tt.report, &second); err != nil {
				t.Fatalf("second render: %v", err)
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Errorf("%s render is not deterministic across two calls:\n--- first ---\n%s\n--- second ---\n%s",
					tt.format, first.Bytes(), second.Bytes())
			}
		})
	}
}
