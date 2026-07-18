package report

import (
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestDerive(t *testing.T) {
	tests := []struct {
		name      string
		scenName  string
		tags      []string
		v         core.Verdict
		evs       []core.Evidence
		pricing   core.Pricing
		wantPass  bool
		wantRuns  int
		wantKind  string   // FailureKind of second run (index 1)
		checkAgg  bool     // assert Aggregate is populated (replaces the 0-as-skip sentinel)
		wantAgg   float64  // expected Aggregate.Computed (checked only when checkAgg)
		wantSeq   []string // expected Sequence (nil = not checked)
		checkCost bool     // assert Cost (replaces the 0-as-skip sentinel; lets a case assert Cost==0)
		wantCost  float64  // expected total cost (checked only when checkCost)
		wantLat   int64    // LatencyMS of first run (if trace is non-nil)
		wantNote  bool     // DerivationNote must be non-empty (degraded derivation)
	}{
		{
			name:     "happy_path_no_trace",
			scenName: "flaky scenario",
			tags:     []string{"@runs(2)"},
			v: core.Verdict{
				Pass:    false,
				Reasons: []string{"aggregate-cel failed: rate = 0.50, want >= 0.80"},
				Detail: &core.AggregateDetail{
					Macro:    "rate",
					Op:       ">=",
					Computed: 0.5,
					Expected: 0.8,
					PerRun:   []float64{1, 0},
				},
			},
			evs: []core.Evidence{
				{RunID: "a", Output: core.Output{Status: 200}},
				{RunID: "b", Failed: true, FailureKind: core.FailureKindResolve},
			},
			pricing:  nil,
			wantPass: false,
			wantRuns: 2,
			wantKind: core.FailureKindResolve,
			checkAgg: true,
			wantAgg:  0.5,
		},
		{
			name:     "all_passed_with_trace_and_pricing",
			scenName: "success scenario",
			tags:     []string{"@smoke"},
			v: core.Verdict{
				Pass:    true,
				Reasons: nil,
			},
			evs: []core.Evidence{
				{
					RunID: "r1",
					Trace: &trace.Trace{
						RunID: "r1",
						Spans: []*trace.Span{
							{
								ID:    "s1",
								Name:  "call",
								Start: time.Unix(0, 0),
								End:   time.Unix(0, int64(500*time.Millisecond)),
								Attrs: map[string]string{
									"gen_ai.usage.cost_usd": "0.002",
									"service.name":          "my-service",
								},
							},
						},
					},
				},
			},
			pricing:   core.Pricing{},
			wantPass:  true,
			wantRuns:  1,
			checkCost: true,
			wantCost:  0.002,
			wantLat:   500,
		},
		{
			name:     "tool_sequence_derived_from_first_trace",
			scenName: "tool scenario",
			tags:     nil,
			v:        core.Verdict{Pass: true},
			evs: []core.Evidence{
				{
					RunID: "r1",
					Trace: &trace.Trace{
						RunID: "r1",
						Spans: []*trace.Span{
							{
								ID:    "s1",
								Name:  "execute_tool",
								Start: time.Unix(0, 0),
								End:   time.Unix(1, 0),
								Attrs: map[string]string{
									"gen_ai.operation.name": "execute_tool",
									"gen_ai.tool.name":      "search",
								},
							},
							{
								ID:    "s2",
								Name:  "execute_tool",
								Start: time.Unix(1, 0),
								End:   time.Unix(2, 0),
								Attrs: map[string]string{
									"gen_ai.operation.name": "execute_tool",
									"gen_ai.tool.name":      "summarize",
								},
							},
						},
					},
				},
			},
			wantPass: true,
			wantRuns: 1,
			wantSeq:  []string{"search", "summarize"},
		},
		{
			name:     "empty_evidence_slice",
			scenName: "empty",
			tags:     nil,
			v:        core.Verdict{Pass: true},
			evs:      nil,
			wantPass: true,
			wantRuns: 0,
		},
		{
			// A malformed gen_ai.usage.cost_usd degrades the cost derivation. Per R5
			// this is now a note, not a scenario failure: the run keeps cost 0 and
			// the verdict is untouched.
			name:     "malformed_cost_records_note",
			scenName: "bad cost",
			tags:     nil,
			v:        core.Verdict{Pass: true},
			evs: []core.Evidence{
				{
					RunID: "r1",
					Trace: &trace.Trace{
						RunID: "r1",
						Spans: []*trace.Span{
							{
								ID:    "s1",
								Name:  "call",
								Start: time.Unix(0, 0),
								End:   time.Unix(1, 0),
								Attrs: map[string]string{
									"gen_ai.usage.cost_usd": "not-a-number",
								},
							},
						},
					},
				},
			},
			wantPass:  true,
			wantRuns:  1,
			checkCost: true,
			wantCost:  0,
			wantNote:  true,
		},
		{
			// An execute_tool span missing gen_ai.tool.name degrades the tool
			// sequence → Derive records a note and keeps the verdict (R5).
			name:     "tool_sequence_records_note",
			scenName: "bad tool seq",
			tags:     nil,
			v:        core.Verdict{Pass: true},
			evs: []core.Evidence{
				{
					RunID: "r1",
					Trace: &trace.Trace{
						RunID: "r1",
						Spans: []*trace.Span{
							{
								ID:    "s1",
								Name:  "execute_tool",
								Start: time.Unix(0, 0),
								End:   time.Unix(1, 0),
								Attrs: map[string]string{
									"gen_ai.operation.name": "execute_tool",
									// gen_ai.tool.name intentionally absent → degraded
								},
							},
						},
					},
				},
			},
			wantPass: true,
			wantRuns: 1,
			wantNote: true,
		},
		{
			// A trace with no tool spans and a span missing service.name degrades the
			// service sequence → Derive records a note and keeps the verdict (R5).
			name:     "service_sequence_records_note",
			scenName: "bad service seq",
			tags:     nil,
			v:        core.Verdict{Pass: true},
			evs: []core.Evidence{
				{
					RunID: "r1",
					Trace: &trace.Trace{
						RunID: "r1",
						Spans: []*trace.Span{
							{
								ID:    "s1",
								Name:  "some-span",
								Start: time.Unix(0, 0),
								End:   time.Unix(1, 0),
								// no service.name, no tool attrs
								Attrs: map[string]string{},
							},
						},
					},
				},
			},
			wantPass: true,
			wantRuns: 1,
			wantNote: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			featureFile := tt.scenName + ".feature"
			sr := Derive(tt.scenName, featureFile, tt.tags, tt.v, tt.evs, tt.pricing)

			if (sr.DerivationNote != "") != tt.wantNote {
				t.Errorf("DerivationNote = %q, wantNote = %v", sr.DerivationNote, tt.wantNote)
			}
			if sr.Name != tt.scenName {
				t.Errorf("Name = %q, want %q", sr.Name, tt.scenName)
			}
			if sr.FeatureFile != featureFile {
				t.Errorf("FeatureFile = %q, want %q", sr.FeatureFile, featureFile)
			}
			if sr.Pass != tt.wantPass {
				t.Errorf("Pass = %v, want %v", sr.Pass, tt.wantPass)
			}
			if len(sr.Runs) != tt.wantRuns {
				t.Fatalf("len(Runs) = %d, want %d; runs=%+v", len(sr.Runs), tt.wantRuns, sr.Runs)
			}
			if tt.wantRuns >= 2 && tt.wantKind != "" {
				if sr.Runs[1].FailureKind != tt.wantKind {
					t.Errorf("Runs[1].FailureKind = %q, want %q", sr.Runs[1].FailureKind, tt.wantKind)
				}
			}
			if tt.checkAgg {
				if sr.Aggregate == nil {
					t.Fatalf("Aggregate is nil, want Computed=%v", tt.wantAgg)
				}
				if sr.Aggregate.Computed != tt.wantAgg {
					t.Errorf("Aggregate.Computed = %v, want %v", sr.Aggregate.Computed, tt.wantAgg)
				}
			}
			if tt.checkCost {
				if sr.Cost != tt.wantCost {
					t.Errorf("Cost = %v, want %v", sr.Cost, tt.wantCost)
				}
			}
			if tt.wantLat != 0 {
				if len(sr.Runs) > 0 && sr.Runs[0].LatencyMS != tt.wantLat {
					t.Errorf("Runs[0].LatencyMS = %d, want %d", sr.Runs[0].LatencyMS, tt.wantLat)
				}
			}
			if tt.wantSeq != nil {
				if len(sr.Sequence) != len(tt.wantSeq) {
					t.Fatalf("Sequence = %v, want %v", sr.Sequence, tt.wantSeq)
				}
				for i, s := range tt.wantSeq {
					if sr.Sequence[i] != s {
						t.Errorf("Sequence[%d] = %q, want %q", i, sr.Sequence[i], s)
					}
				}
			}
		})
	}
}

// TestDeriveDegradation locks in the audit-A8 contract (R5): a run whose
// sequence/cost derivation is impossible must NOT fail the scenario. Derive yields
// best-effort detail (empty sequence) plus a non-empty, span-naming DerivationNote,
// and the caller's verdict (from step results) is left untouched. A healthy trace
// derives cleanly and carries no note.
func TestDeriveDegradation(t *testing.T) {
	t.Parallel()

	// degraded: one span with no gen_ai.* tool attrs and no service.name — the
	// tool sequence is empty and the service sequence cannot be built.
	degraded := &trace.Trace{
		RunID: "r1",
		Spans: []*trace.Span{
			{
				ID:    "abc123",
				Name:  "fetch",
				Start: time.Unix(0, 0),
				End:   time.Unix(1, 0),
				Attrs: map[string]string{},
			},
		},
	}

	tests := []struct {
		name     string
		v        core.Verdict
		evs      []core.Evidence
		wantPass bool
		wantNote bool     // DerivationNote must be non-empty
		noteHas  []string // substrings the note must name (only checked when wantNote)
		wantSeq  int      // expected len(Sequence)
	}{
		{
			name:     "service_sequence_impossible_records_note_keeps_pass",
			v:        core.Verdict{Pass: true},
			evs:      []core.Evidence{{RunID: "r1", Trace: degraded}},
			wantPass: true, // verdict is unchanged by the derivation problem
			wantNote: true,
			noteHas:  []string{"sequence unavailable", "service.name", "fetch"},
			wantSeq:  0, // best-effort empty sequence
		},
		{
			name: "healthy_trace_has_no_note",
			v:    core.Verdict{Pass: true},
			evs: []core.Evidence{
				{
					RunID: "r1",
					Trace: &trace.Trace{
						RunID: "r1",
						Spans: []*trace.Span{
							{
								ID:    "s1",
								Name:  "execute_tool",
								Start: time.Unix(0, 0),
								End:   time.Unix(1, 0),
								Attrs: map[string]string{
									"gen_ai.operation.name": "execute_tool",
									"gen_ai.tool.name":      "search",
								},
							},
						},
					},
				},
			},
			wantPass: true,
			wantNote: false,
			wantSeq:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sr := Derive("s", "s.feature", nil, tt.v, tt.evs, core.Pricing{})
			if sr.Pass != tt.wantPass {
				t.Errorf("Pass = %v, want %v (derivation must not flip the verdict)", sr.Pass, tt.wantPass)
			}
			if tt.wantNote {
				if sr.DerivationNote == "" {
					t.Fatalf("DerivationNote is empty, want a note naming the span")
				}
				for _, sub := range tt.noteHas {
					if !strings.Contains(sr.DerivationNote, sub) {
						t.Errorf("DerivationNote = %q, want it to contain %q", sr.DerivationNote, sub)
					}
				}
			} else if sr.DerivationNote != "" {
				t.Errorf("DerivationNote = %q, want empty for a healthy trace", sr.DerivationNote)
			}
			if len(sr.Sequence) != tt.wantSeq {
				t.Errorf("len(Sequence) = %d, want %d", len(sr.Sequence), tt.wantSeq)
			}
		})
	}
}
