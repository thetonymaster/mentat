// Package mentat_test is an external-style test: it imports ONLY the public
// facade (github.com/thetonymaster/mentat), never any internal/... package.
// That import restriction is the whole point — it proves a third-party module
// could implement the seams and read the evidence types using the published
// surface alone (spec FR-001, US1/US2 "Independent Test").
package mentat_test

import (
	"context"
	"testing"
	"time"

	"github.com/thetonymaster/mentat"
)

// toyDriver implements mentat.Driver using only facade type names in its
// signature. Because mentat.Driver is a type ALIAS to the internal seam, a
// struct satisfying mentat.Driver satisfies the internal seam by identity.
type toyDriver struct{}

func (toyDriver) Run(_ context.Context, spec mentat.RunSpec) (mentat.RunResult, error) {
	return mentat.RunResult{RunID: spec.RunID, Output: mentat.Output{Answer: "ok"}}, nil
}

// toyStore implements mentat.TraceStore using only facade type names.
type toyStore struct{}

func (toyStore) FetchPayload(_ context.Context, _ string) ([]byte, error) { return []byte("{}"), nil }
func (toyStore) DecodePayload(id string, _ []byte) (*mentat.Trace, error) {
	return &mentat.Trace{RunID: id}, nil
}
func (toyStore) Query(_ context.Context, q mentat.TraceQuery) ([]mentat.TraceRef, error) {
	return []mentat.TraceRef{{TraceID: q.Value}}, nil
}
func (toyStore) Caps() mentat.StoreCaps { return mentat.StoreCaps{StructuralQuery: true} }

// toyComparator implements mentat.Comparator using only facade type names. A
// comparator author reads Evidence and only Evidence (Constitution I).
type toyComparator struct{}

func (toyComparator) Name() string { return "toy" }
func (toyComparator) Compare(_ context.Context, ev mentat.Evidence, _ mentat.Expectation) (mentat.Verdict, error) {
	if ev.Failed && ev.FailureKind == mentat.FailureKindDriver {
		return mentat.Verdict{Pass: false, Reasons: []string{"driver failed"}}, nil
	}
	// Fields a comparator author actually reads off Evidence.
	_ = ev.Trace
	_ = ev.Output
	return mentat.Verdict{Pass: true}, nil
}

// toyJudge implements mentat.Judge using only facade type names.
type toyJudge struct{}

func (toyJudge) Judge(_ context.Context, req mentat.JudgeRequest) (mentat.JudgeVerdict, error) {
	return mentat.JudgeVerdict{Match: req.Candidate == req.Expected, Reason: "compared"}, nil
}

// Alias-identity proof: these compile-time assignments only compile if the
// facade names are aliases to the internal seams (a fresh, unrelated interface
// with the same methods would still satisfy these, but the registration hooks
// added in later tasks — which take the internal seam — are what force true
// identity; the assertions here lock the method sets now).
var (
	_ mentat.Driver     = toyDriver{}
	_ mentat.TraceStore = toyStore{}
	_ mentat.Comparator = toyComparator{}
	_ mentat.Judge      = toyJudge{}
)

// Nameability proof (feature 009 US3, contracts/facade-nameability.md).
//
// REACHABLE SET — the transitive closure of exported struct types found in
// exported fields starting from mentat.Config and mentat.Results, following slice
// element, map key/value, pointer element and embedded types. Swept 2026-07-18;
// each row records HOW the type is reached and whether the facade could name it:
//
//	from mentat.Config:
//	  Config         (the root)                                alias, pre-existing
//	  Endpoint       Config.Tempo                              alias, pre-existing
//	  PollSpec       Config.Poll                               alias, pre-existing
//	  Pricing        Config.Pricing (named map type)           alias, pre-existing
//	  ModelRate      Pricing's map value type                  alias, pre-existing
//	  JudgeConfig    Config.Judge                              alias, pre-existing
//	  RunBudget      Config.Budget, Target.Budget              alias, pre-existing
//	  Target         Config.Targets' map value type            alias, pre-existing
//	  HTTP           Target.HTTP                               alias, pre-existing
//	  ExtractConfig  Target.Extract                            alias, pre-existing
//	  Completeness   Target.Completeness                       ALIAS ADDED (US3)
//	from mentat.Results:
//	  Results        (the root)                                facade-owned struct
//	  ScenarioResult Results.Scenarios' slice element          facade-owned struct
//	  JudgeUsage     Results.JudgeTotal, ScenarioResult.Judge  alias, pre-existing
//
// Completeness was the single gap: Target.Completeness has type config.Completeness
// and the facade aliased only CompletenessContract, a DIFFERENT (core-side) type.
// No exported field in the set is embedded, and every field type not listed above
// is predeclared or time.Duration, so the closure terminates here.
//
// One composite literal per member follows. Each sets at least one field and names
// every composite-typed field through the facade, so a field whose OWN type is an
// un-aliased internal type breaks this build. Because this file imports ONLY the
// facade, compiling IS the proof — no runtime assertion is needed beyond keeping
// the values referenced.
var (
	// --- reachable from mentat.Config ---
	_ = mentat.Config{
		Store:        "tempo",
		StorePath:    "testdata/traces",
		OTLPEndpoint: "localhost:4317",
		Expectations: "expectations",
		RunTimeout:   "5m",
		KillGrace:    "10s",
		Tempo:        mentat.Endpoint{Endpoint: "http://localhost:3200"},
		Poll:         mentat.PollSpec{Interval: "500ms", Timeout: "30s", StableFor: 2, SearchLimit: 100},
		Pricing:      mentat.Pricing{"claude-haiku-4-5": mentat.ModelRate{InputPerMTok: 1, OutputPerMTok: 5}},
		Judge:        mentat.JudgeConfig{Backend: "claude", Model: "claude-haiku-4-5", Votes: 1},
		Budget:       mentat.RunBudget{Timeout: 5 * time.Minute, KillGrace: 10 * time.Second},
		Targets:      map[string]mentat.Target{"agent": {Adapter: "shell"}},
	}
	_ = mentat.Endpoint{Endpoint: "http://localhost:3200"}
	_ = mentat.PollSpec{Interval: "500ms", Timeout: "30s", StableFor: 2, SearchLimit: 100}
	_ = mentat.Pricing{"claude-haiku-4-5": mentat.ModelRate{InputPerMTok: 1, OutputPerMTok: 5}}
	_ = mentat.ModelRate{InputPerMTok: 1, OutputPerMTok: 5}
	_ = mentat.JudgeConfig{Backend: "claude", Model: "claude-haiku-4-5", Votes: 3, Temperature: 0.7, MaxCostUSD: 1.50}
	_ = mentat.RunBudget{Timeout: 5 * time.Minute, Unbounded: false, KillGrace: 10 * time.Second}
	_ = mentat.Target{
		Adapter:        "shell",
		Command:        []string{"echo", "hello"},
		MaxConcurrency: 4,
		RunTimeout:     "1m",
		HTTP:           mentat.HTTP{URL: "http://localhost:8080/ask", Method: "POST", Headers: map[string]string{"content-type": "application/json"}},
		Budget:         mentat.RunBudget{Timeout: time.Minute, KillGrace: time.Second},
		Extract:        mentat.ExtractConfig{Mode: "marker", Marker: "ANSWER:"},
		Completeness:   mentat.Completeness{Mode: "strict", SettleRaw: "2s", Settle: 2 * time.Second},
	}
	_ = mentat.HTTP{URL: "http://localhost:8080/ask", Method: "POST", Headers: map[string]string{"content-type": "application/json"}}
	_ = mentat.ExtractConfig{Mode: "pattern", Marker: "ANSWER:", Pattern: `ANSWER:\s*(.*)`}
	_ = mentat.Completeness{Mode: "strict", SettleRaw: "2s", Settle: 2 * time.Second}

	// --- reachable from mentat.Results ---
	_ = mentat.Results{
		Scenarios:   []mentat.ScenarioResult{{Name: "a scenario"}},
		Passed:      1,
		Failed:      0,
		Interrupted: false,
		TotalCost:   0.0125,
		JudgeTotal:  &mentat.JudgeUsage{Calls: 1, Model: "claude-haiku-4-5"},
	}
	_ = mentat.ScenarioResult{
		Name:           "a scenario",
		FeatureFile:    "features/smoke.feature",
		Pass:           true,
		Reasons:        []string{"tool order matched"},
		Cost:           0.0125,
		RunIDs:         []string{"run-1", "run-2"},
		DerivationNote: "aggregated over 2 runs",
		Judge:          &mentat.JudgeUsage{Calls: 1, Model: "claude-haiku-4-5"},
	}
	_ = mentat.JudgeUsage{Calls: 1, InputTokens: 120, OutputTokens: 34, CostUsd: 0.0125, Model: "claude-haiku-4-5"}
)

// TestFacadeSurfaceExercisesContractTypes touches the evidence/contract types a
// store, comparator, and judge author reads or constructs through the facade —
// so the test exercises the whole minimal skeleton surface, not just Driver.
func TestFacadeSurfaceExercisesContractTypes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// A store decoder builds a Trace forest of Spans using the canonical
	// status/kind vocabulary (feature 002).
	span := &mentat.Span{
		ID:     "s1",
		Name:   "root",
		Kind:   mentat.KindServer,
		Status: mentat.StatusOk,
	}
	tr := &mentat.Trace{
		RunID: "run-1",
		Roots: []*mentat.Span{span},
		Spans: []*mentat.Span{span},
	}

	// A comparator reads Evidence: Trace, Output, FailureKind.
	ev := mentat.Evidence{
		RunID:  "run-1",
		Trace:  tr,
		Output: mentat.Output{Answer: "42"},
	}

	var exp mentat.Expectation = "budget<=5"
	got, err := (toyComparator{}).Compare(ctx, ev, exp)
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if !got.Pass {
		t.Fatalf("Compare verdict = %+v, want Pass", got)
	}

	// Driver: RunSpec in, RunResult out.
	res, err := (toyDriver{}).Run(ctx, mentat.RunSpec{RunID: "run-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RunID != "run-1" {
		t.Fatalf("RunResult.RunID = %q, want run-1", res.RunID)
	}

	// Store: TraceQuery in, TraceRef out; Caps returns StoreCaps.
	refs, err := (toyStore{}).Query(ctx, mentat.TraceQuery{Tag: "test.run.id", Value: "run-1"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(refs) != 1 || refs[0].TraceID != "run-1" {
		t.Fatalf("Query refs = %+v, want one ref for run-1", refs)
	}
	if !(toyStore{}).Caps().StructuralQuery {
		t.Fatal("Caps().StructuralQuery = false, want true")
	}

	// Judge: JudgeRequest in, JudgeVerdict out.
	jv, err := (toyJudge{}).Judge(ctx, mentat.JudgeRequest{Candidate: "a", Expected: "a"})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !jv.Match {
		t.Fatalf("JudgeVerdict = %+v, want Match", jv)
	}

	// FailureKind constants and the canonical status/kind vocabulary are part of
	// the surface an implementer compares against.
	if mentat.FailureKindDriver == mentat.FailureKindResolve {
		t.Fatal("FailureKind constants must be distinct")
	}
	if mentat.StatusUnset == "" || mentat.StatusError == "" {
		t.Fatal("status constants must be non-empty canonical values")
	}
	if mentat.KindUnspecified != "" {
		t.Fatal("KindUnspecified must be the empty-string canonical value")
	}
	_ = mentat.KindInternal
	_ = mentat.KindClient
	_ = mentat.KindProducer
	_ = mentat.KindConsumer
}
