// Package result holds the types a completed run produces and a Reporter renders.
//
// They live here rather than in the facade for a hard reason (feature 010, D5): the
// root package imports internal/report, internal/engine and internal/registry, so any
// type those packages CONSUME cannot be declared at the root without an import cycle.
// Every public type on the facade is an alias for exactly this reason. Only terminal
// types — produced at the facade and never passed back down — could be declared there,
// and a seam's parameter type is by definition not terminal.
//
// They live here rather than in internal/core because core already declares an unrelated
// ScenarioResult vocabulary and because these types are the run's OUTPUT, not part of the
// comparator/driver contract vocabulary core exists to hold. core imports nothing from
// this package; this package imports core for JudgeUsage and AggregateDetail.
package result

import (
	"io"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

// Results is the whole-run artifact a Reporter renders, and the value mentat.Run returns.
// Pure data.
//
// Before feature 010 this type was core.RunReport and the facade declared a separate,
// lossy struct of its own; a reporter therefore saw more than an external author could.
// They are one type now, so a custom reporter and a built-in one render from exactly the
// same input by construction (FR-011, FR-012).
type Results struct {
	Scenarios []ScenarioResult
	Total     int
	Passed    int
	Failed    int
	TotalCost float64
	StartedAt time.Time
	Duration  time.Duration
	// Interrupted marks a run that a SIGINT/SIGTERM cancelled before it ran to
	// completion (feature 003, FR-006). The report then carries the scenarios that
	// finished plus this explicit marker; omitted from a clean run's JSON.
	Interrupted bool `json:"interrupted,omitempty"`
	// JudgeTotal is the suite-wide judge-token ledger, summed field-wise across the
	// scenarios that made judge calls (US6). Non-nil ONLY when at least one scenario
	// issued a judge call — absence of usage is not a fabricated all-zero total
	// (judge-ledger contract, FR-006). Its Model is intentionally empty (the total is
	// not attributed to one model); CostUsd is filled by report.Price at render time.
	JudgeTotal *core.JudgeUsage `json:"judgeTotal,omitempty"`
}

// ExitCode maps Results onto the process exit code the CLI uses, so a library
// consumer (and the CLI as "consumer zero") can turn a Run into an os.Exit code with
// one call: an interrupted run is 130 (128 + SIGINT, and it wins over a red suite so
// CI can tell cancellation from a plain failure), else any failed scenario is 1, else
// 0. These three codes are the stable Results-to-exit-status contract.
func (r Results) ExitCode() int {
	switch {
	case r.Interrupted:
		return 130
	case r.Failed > 0:
		return 1
	default:
		return 0
	}
}

// ScenarioResult is one scenario's outcome, derived from its Evidence + Verdict.
type ScenarioResult struct {
	Name string
	// FeatureFile is the source .feature file this scenario was parsed from (godog's
	// scenario Uri), so scenarios can be told apart by origin, not just by Name.
	FeatureFile string `json:"FeatureFile,omitempty"`
	Tags        []string
	Pass        bool
	Reasons     []string
	// Qualifiers are the completeness qualifiers the engine attached to this scenario's
	// verdict (feature 008, US2) — e.g. the ingestion-window caveat a bounded
	// request-scoped run carries on a completeness-sensitive assertion. Carried verbatim
	// from Verdict.Qualifiers by report.Derive and rendered by every reporter on pass AND
	// fail; empty (omitted from JSON) when none apply. Reporters never derive them.
	Qualifiers []string `json:"qualifiers,omitempty"`
	Cost       float64
	Sequence   []string
	Runs       []RunRecord
	Aggregate  *core.AggregateDetail
	// DerivationNote is a non-fatal, human-readable note recorded when report
	// derivation (sequence/cost) could not be completed for this scenario — e.g. a
	// span missing service.name. It is an observer artifact: it never changes Pass
	// (verdicts come only from step results, audit A8) but stays visible in the JSON
	// and HTML report so the degradation is surfaced, not swallowed. Empty when
	// derivation was clean.
	DerivationNote string `json:"DerivationNote,omitempty"`
	// Judge is this scenario's summed judge-token ledger (US6), carried from the
	// semantic matcher's Verdict.Judge through report.Derive. Non-nil ONLY when the
	// scenario made a judge call — a scenario with no `the result means` step leaves
	// it nil (no fabricated zeros, FR-006). CostUsd is 0 until report.Price fills it.
	Judge *core.JudgeUsage `json:"judge,omitempty"`
	// RunIDs is the injected run id of each run, positionally aligned with Runs — the
	// convenience projection library callers of Run already read.
	//
	// EXCLUDED from serialization (feature 010). Before the type collapse the facade
	// carried RunIDs and the report record carried Runs; they were different structs, so
	// the reports never had a "RunIDs" key. Emitting one now would silently change every
	// user's report JSON, so this field stays a Go-level convenience only. Populated
	// beside Runs in report.Derive, in the same loop, so the two cannot disagree.
	RunIDs []string `json:"-"`
}

// RunRecord is one run within a scenario (one element per @runs iteration).
type RunRecord struct {
	RunID       string
	Passed      bool
	FailureKind string
	LatencyMS   int64
	Cost        float64
}

// Reporter renders a completed run to a writer. Stateless.
//
// Implementations receive the same Results a library caller receives from Run — there is
// no richer internal input, which is what makes the seam genuinely implementable from
// outside the module (feature 010, FR-003/FR-012).
type Reporter interface {
	Report(res Results, w io.Writer) error
}
