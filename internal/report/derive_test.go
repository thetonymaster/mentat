package report

import (
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/trace"
)

func TestDerive(t *testing.T) {
	tests := []struct {
		name     string
		scenName string
		tags     []string
		v        core.Verdict
		evs      []core.Evidence
		pricing  core.Pricing
		wantPass bool
		wantRuns int
		wantKind string   // FailureKind of second run (index 1)
		checkAgg bool     // assert Aggregate is populated (replaces the 0-as-skip sentinel)
		wantAgg  float64  // expected Aggregate.Computed (checked only when checkAgg)
		wantSeq  []string // expected Sequence (nil = not checked)
		wantCost float64  // total cost
		wantLat  int64    // LatencyMS of first run (if trace is non-nil)
		wantErr  bool
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
			pricing:  core.Pricing{},
			wantPass: true,
			wantRuns: 1,
			wantCost: 0.002,
			wantLat:  500,
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
			name:     "malformed_cost_propagates_error",
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
			wantErr: true,
		},
		{
			// An execute_tool span missing gen_ai.tool.name triggers the ToolSequence
			// error path → sequence returns the error → Derive wraps and returns it.
			name:     "tool_sequence_error_propagates",
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
									// gen_ai.tool.name intentionally absent → error
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			// A trace with no tool spans and a span missing service.name triggers
			// the ServiceSequence error path → Derive wraps and returns the error.
			name:     "service_sequence_error_propagates",
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
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			sr, err := Derive(tt.scenName, tt.tags, tt.v, tt.evs, tt.pricing)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Derive() err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if sr.Name != tt.scenName {
				t.Errorf("Name = %q, want %q", sr.Name, tt.scenName)
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
			if tt.wantCost != 0 {
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
