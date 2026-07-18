package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
	"github.com/thetonymaster/mentat/internal/trace"
)

// wantQualifier5s is the canonical completeness qualifier (contracts §3) for a
// bounded target whose effective settle window is 5s. The engine must attach exactly
// this text — the report layer renders Verdict.Qualifiers verbatim.
const wantQualifier5s = "trace-completeness: bounded by ingestion window (settle 5s); spans exported later are not observed"

// stubVerdictComparator returns a fixed verdict/error so a test drives Compare's
// qualifier-attachment join (pass, fail, error) without a real comparator or trace.
type stubVerdictComparator struct {
	v   core.Verdict
	err error
}

func (stubVerdictComparator) Name() string { return "stub-verdict" }
func (s stubVerdictComparator) Compare(context.Context, core.Evidence, core.Expectation) (core.Verdict, error) {
	return s.v, s.err
}

// qualifierEngine builds an engine with a request-scoped (http, 5s settle → bounded)
// target "web" and a spawned (shell → unbounded) target "cli", plus the caller's
// stub comparator registered as "stub-verdict".
func qualifierEngine(t *testing.T, cmp core.Comparator) *Engine {
	t.Helper()
	cfg := config.Config{
		OTLPEndpoint: "x",
		Targets: map[string]config.Target{
			"web": {Adapter: "http", MaxConcurrency: 1, Completeness: config.Completeness{Mode: "settle", Settle: 5 * time.Second}},
			"cli": {Adapter: "shell", Command: []string{"true"}, MaxConcurrency: 1, Completeness: config.Completeness{Mode: "settle", Settle: 2 * time.Second}},
			// Strict-mode counterparts (FR-009): a strict contract asserts exact
			// completeness, so it must NOT carry the ingestion-window qualifier on ANY
			// adapter kind — including the request-scoped "web-strict".
			"web-strict": {Adapter: "http", MaxConcurrency: 1, Completeness: config.Completeness{Mode: "strict", Settle: 5 * time.Second}},
			"cli-strict": {Adapter: "shell", Command: []string{"true"}, MaxConcurrency: 1, Completeness: config.Completeness{Mode: "strict", Settle: 2 * time.Second}},
		},
	}
	ctrl := gomock.NewController(t)
	st := mocks.NewMockTraceStore(ctrl)
	cor := mocks.NewMockCorrelator(ctrl)
	eng, err := Build(cfg, st, cor, WithExtraComparator("stub-verdict", stubComparatorFactory(cmp)))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return eng
}

// TestCompareAttachesBoundedQualifier pins the T017 join: the engine appends the
// canonical completeness qualifier to a comparator's Verdict when the expectation is
// completeness-sensitive AND the target's contract is bounded (request-scoped,
// non-strict) — on pass AND fail alike. Spawned targets and non-sensitive
// expectations get no qualifier; a comparator error propagates unqualified.
func TestCompareAttachesBoundedQualifier(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		verdict   core.Verdict
		cmpErr    error
		sensitive bool
		wantQual  bool
		wantErr   bool
	}{
		{name: "bounded_sensitive_pass_attaches", target: "web", verdict: core.Verdict{Pass: true}, sensitive: true, wantQual: true},
		{name: "bounded_sensitive_fail_attaches", target: "web", verdict: core.Verdict{Pass: false, Reasons: []string{"nope"}}, sensitive: true, wantQual: true},
		{name: "spawned_sensitive_no_qualifier", target: "cli", verdict: core.Verdict{Pass: true}, sensitive: true, wantQual: false},
		{name: "bounded_non_sensitive_no_qualifier", target: "web", verdict: core.Verdict{Pass: true}, sensitive: false, wantQual: false},
		{name: "comparator_error_propagates_unqualified", target: "web", cmpErr: errors.New("boom"), sensitive: true, wantErr: true, wantQual: false},
		{name: "unknown_target_no_qualifier", target: "ghost", verdict: core.Verdict{Pass: true}, sensitive: true, wantQual: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := qualifierEngine(t, stubVerdictComparator{v: tt.verdict, err: tt.cmpErr})
			v, err := eng.Compare(context.Background(), tt.target, "stub-verdict", core.Evidence{}, nil, tt.sensitive)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			gotQual := len(v.Qualifiers) > 0
			if gotQual != tt.wantQual {
				t.Fatalf("Qualifiers=%v, wantQual=%v", v.Qualifiers, tt.wantQual)
			}
			if tt.wantQual && v.Qualifiers[0] != wantQualifier5s {
				t.Fatalf("Qualifiers[0] = %q, want %q", v.Qualifiers[0], wantQualifier5s)
			}
		})
	}
}

// TestCompareStrictSuppressesQualifierFR009 pins FR-009: a strict-mode contract
// asserts EXACT completeness (the SUT declares its own span count), so the engine
// must NOT attach the ingestion-window qualifier to a strict-mode verdict on ANY
// adapter kind — including a request-scoped ("http") target, where a non-strict run
// WOULD carry it. This is the explicit FR-009 guard: the completeness-sensitive
// expectation is present and the verdict is bounded-eligible by kind, yet strict mode
// suppresses the caveat.
func TestCompareStrictSuppressesQualifierFR009(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		// The load-bearing FR-009 row: request-scoped + strict → still no qualifier.
		{name: "request_scoped_strict_no_qualifier", target: "web-strict"},
		{name: "spawned_strict_no_qualifier", target: "cli-strict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := qualifierEngine(t, stubVerdictComparator{v: core.Verdict{Pass: true}})
			// sensitive=true: the expectation IS completeness-sensitive, so the only
			// thing that can suppress the qualifier here is the strict contract itself.
			v, err := eng.Compare(context.Background(), tt.target, "stub-verdict", core.Evidence{}, nil, true)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if len(v.Qualifiers) != 0 {
				t.Fatalf("strict target %q attached qualifiers %v; FR-009 forbids the ingestion-window caveat on strict-mode verdicts", tt.target, v.Qualifiers)
			}
		})
	}
}

// TestCompareUnknownComparator proves Compare hard-errors on an unregistered
// comparator name rather than silently returning a zero verdict (Constitution IV).
func TestCompareUnknownComparator(t *testing.T) {
	eng := qualifierEngine(t, stubVerdictComparator{})
	if _, err := eng.Compare(context.Background(), "web", "nope", core.Evidence{}, nil, true); err == nil {
		t.Fatal("expected an error for an unknown comparator, got nil")
	}
}

// TestAggregateAttachesBoundedQualifier pins the aggregate sibling of the join: the
// CEL-aggregate path (the runs satisfy) is completeness-sensitive, so a bounded
// target carries the qualifier and a spawned target does not.
func TestAggregateAttachesBoundedQualifier(t *testing.T) {
	tr := &trace.Trace{Spans: []*trace.Span{{Name: "root"}}, Roots: []*trace.Span{{Name: "root"}}}
	evs := []core.Evidence{{RunID: "r1", Trace: tr}, {RunID: "r2", Trace: tr}}
	tests := []struct {
		name      string
		target    string
		sensitive bool
		wantQual  bool
	}{
		{name: "bounded_sensitive_attaches", target: "web", sensitive: true, wantQual: true},
		{name: "spawned_sensitive_no_qualifier", target: "cli", sensitive: true, wantQual: false},
		{name: "bounded_non_sensitive_no_qualifier", target: "web", sensitive: false, wantQual: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := qualifierEngine(t, stubVerdictComparator{})
			v, err := eng.Aggregate(context.Background(), tt.target, "aggregate-cel", evs, comparator.AggregateCELExpectation{Expr: "count(r, r.failed) == 0"}, tt.sensitive)
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			gotQual := len(v.Qualifiers) > 0
			if gotQual != tt.wantQual {
				t.Fatalf("Qualifiers=%v, wantQual=%v", v.Qualifiers, tt.wantQual)
			}
			if tt.wantQual && v.Qualifiers[0] != wantQualifier5s {
				t.Fatalf("Qualifiers[0] = %q, want %q", v.Qualifiers[0], wantQualifier5s)
			}
		})
	}
}

// TestAggregateUnknownComparator proves Aggregate hard-errors on an unregistered
// aggregate comparator name.
func TestAggregateUnknownComparator(t *testing.T) {
	eng := qualifierEngine(t, stubVerdictComparator{})
	if _, err := eng.Aggregate(context.Background(), "web", "nope", nil, nil, true); err == nil {
		t.Fatal("expected an error for an unknown aggregate comparator, got nil")
	}
}
