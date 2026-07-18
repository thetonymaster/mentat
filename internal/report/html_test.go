package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// qualifierText is the canonical completeness qualifier (contracts §3), shared by the
// reporter render tests. The reporters render Verdict.Qualifiers verbatim.
const qualifierText = "trace-completeness: bounded by ingestion window (settle 5s); spans exported later are not observed"

// TestHTMLReporterQualifierRendersOnPassAndFail pins the T019 html requirement (E1):
// the completeness qualifier renders on a PASSING scenario too — the block must sit
// OUTSIDE the fail-only {{if not .Pass}} reasons guard. A scenario with no qualifier
// renders no qualifier block.
func TestHTMLReporterQualifierRendersOnPassAndFail(t *testing.T) {
	rep := core.RunReport{Total: 3, Passed: 2, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "green-bounded", Pass: true, Qualifiers: []string{qualifierText}},
		{Name: "red-bounded", Pass: false, Reasons: []string{"boom"}, Qualifiers: []string{qualifierText}},
		{Name: "green-plain", Pass: true},
	}}
	var buf bytes.Buffer
	if err := (htmlReporter{}).Report(rep, &buf); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	// Rendered once per qualifier-bearing scenario — including the passing one.
	if n := strings.Count(out, "trace-completeness: bounded by ingestion window"); n != 2 {
		t.Fatalf("qualifier rendered %d times, want 2 (pass AND fail):\n%s", n, out)
	}
	// The plain passing scenario carries no qualifier block.
	if strings.Contains(out, `class="qualifiers"`) != true {
		t.Fatalf("expected a qualifiers block class in output:\n%s", out)
	}
}

// errWriter always returns an error on Write, used to exercise the
// htmlTmpl.Execute error branch.
type errWriter struct{}

func (errWriter) Write(_ []byte) (int, error) {
	return 0, bytes.ErrTooLarge
}

func TestHTMLReporter(t *testing.T) {
	failingRep := core.RunReport{Total: 1, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "flaky", Pass: false, Cost: 0.0125,
			Reasons:   []string{"rate = 0.50, want >= 0.80"},
			Runs:      []core.RunRecord{{RunID: "abc", Passed: true, LatencyMS: 120}},
			Aggregate: &core.AggregateDetail{Macro: "rate", Op: ">=", Computed: 0.5, Expected: 0.8}},
	}}

	passingRep := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{
		{Name: "sunny", Pass: true, Cost: 0.005},
	}}

	noAggRep := core.RunReport{Total: 1, Failed: 1, Scenarios: []core.ScenarioResult{
		{Name: "no-agg", Pass: false, Cost: 0.001,
			Reasons:   []string{"something failed"},
			Aggregate: nil},
	}}

	seqRep := core.RunReport{Total: 1, Passed: 1, Scenarios: []core.ScenarioResult{
		{Name: "with-seq", Pass: true, Cost: 0.002,
			Sequence: []string{"search", "summarize"}},
	}}

	tests := []struct {
		name        string
		rep         core.RunReport
		writer      interface{ Write([]byte) (int, error) }
		wantStrings []string
		wantAbsent  []string
		wantErr     bool
	}{
		{
			name:   "failing scenario with reasons, aggregate, and run rows",
			rep:    failingRep,
			writer: &bytes.Buffer{},
			wantStrings: []string{
				"<html",
				"flaky",
				"rate = 0.50, want &gt;= 0.80",
				"abc",
				"0.0125",
			},
		},
		{
			name:       "passing scenario has no reasons ul block",
			rep:        passingRep,
			writer:     &bytes.Buffer{},
			wantAbsent: []string{"<ul>"},
			wantStrings: []string{
				"<html",
				"sunny",
			},
		},
		{
			name:        "nil aggregate renders without panic and without aggregate line",
			rep:         noAggRep,
			writer:      &bytes.Buffer{},
			wantStrings: []string{"<html", "no-agg"},
			wantAbsent:  []string{`class="aggregate`},
		},
		{
			name:   "non-empty sequence appears in output",
			rep:    seqRep,
			writer: &bytes.Buffer{},
			wantStrings: []string{
				"with-seq",
				"search",
				"summarize",
			},
		},
		{
			name:    "broken writer causes Execute to return error",
			rep:     failingRep,
			writer:  errWriter{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := (htmlReporter{}).Report(tt.rep, tt.writer)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			buf, ok := tt.writer.(*bytes.Buffer)
			if !ok {
				t.Fatal("writer is not *bytes.Buffer for non-error case")
			}
			out := buf.String()
			for _, want := range tt.wantStrings {
				if !strings.Contains(out, want) {
					t.Errorf("html missing %q", want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(out, absent) {
					t.Errorf("html should not contain %q but does", absent)
				}
			}
		})
	}
}
