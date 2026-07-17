// Package mentat_test is an external-style test: it imports ONLY the public
// facade (github.com/thetonymaster/mentat), never any internal/... package.
// That import restriction is the whole point — it proves a third-party module
// could implement the seams and read the evidence types using the published
// surface alone (spec FR-001, US1/US2 "Independent Test").
package mentat_test

import (
	"context"
	"testing"

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
