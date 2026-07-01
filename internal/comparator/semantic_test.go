package comparator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/core/mocks"
)

// vote is one scripted Judge response for a single best-of-N vote.
type vote struct {
	v   core.JudgeVerdict
	err error
}

// newScriptedJudge builds a MockJudge that expects exactly one Judge call per
// element of returns — each matching JudgeRequest{Candidate: candidate, Expected: want}
// and returning the scripted verdict/err in declaration order (gomock consumes
// same-arg expectations FIFO). The controller's Cleanup enforces the exact call
// count, so len(returns) IS the call-count assertion: extra calls fail as
// unexpected, missing calls fail as unsatisfied. A nil/empty returns asserts the
// judge is never called (.Times(0)).
func newScriptedJudge(t *testing.T, candidate, want string, returns []vote) core.Judge {
	t.Helper()
	ctrl := gomock.NewController(t)
	j := mocks.NewMockJudge(ctrl)
	if len(returns) == 0 {
		j.EXPECT().Judge(gomock.Any(), gomock.Any()).Times(0)
		return j
	}
	req := core.JudgeRequest{Candidate: candidate, Expected: want}
	for _, r := range returns {
		j.EXPECT().Judge(gomock.Any(), req).Return(r.v, r.err)
	}
	return j
}

func TestSemanticName(t *testing.T) {
	t.Parallel()
	if got := NewSemantic(nil, 1).Name(); got != "semantic" {
		t.Fatalf("Name()=%q want %q", got, "semantic")
	}
}

// T009 — verdict mapping: a true vote passes; a false vote fails carrying the
// judge's reason (FR-008).
func TestSemanticVerdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		candidate   string
		want        string
		returns     []vote
		wantPass    bool
		wantReasons []string
	}{
		{
			name:      "single true vote yields a passing verdict",
			candidate: "Total Q3 revenue was $4.2M.",
			want:      "the Q3 revenue figure",
			returns:   []vote{{v: core.JudgeVerdict{Match: true, Reason: "states the Q3 revenue"}}},
			wantPass:  true,
		},
		{
			name:        "single false vote fails carrying the judge reason",
			candidate:   "It was sunny on Tuesday.",
			want:        "the Q3 revenue figure",
			returns:     []vote{{v: core.JudgeVerdict{Match: false, Reason: "no revenue figure present"}}},
			wantPass:    false,
			wantReasons: []string{"no revenue figure present"},
		},
		{
			name:        "false vote with an empty reason fails carrying a non-empty placeholder",
			candidate:   "It was sunny on Tuesday.",
			want:        "the Q3 revenue figure",
			returns:     []vote{{v: core.JudgeVerdict{Match: false, Reason: ""}}},
			wantPass:    false,
			wantReasons: []string{"semantic judge returned no-match without a reason"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			j := newScriptedJudge(t, tt.candidate, tt.want, tt.returns)
			m := NewSemantic(j, 1)
			ev := core.Evidence{Output: core.Output{Answer: tt.candidate}}
			v, err := m.Match(context.Background(), ev, tt.want, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if !tt.wantPass {
				if len(v.Reasons) == 0 || v.Reasons[0] == "" {
					t.Fatalf("failing verdict must carry a non-empty reason, got %v", v.Reasons)
				}
				if !reflect.DeepEqual(v.Reasons, tt.wantReasons) {
					t.Fatalf("reasons=%v want %v", v.Reasons, tt.wantReasons)
				}
			}
		})
	}
}

// T010 — vote tally: exactly N judge calls; strict majority decides; an even
// split is a tie hard error (FR-015) with no verdict.
func TestSemanticVotes(t *testing.T) {
	t.Parallel()
	const (
		candidate = "The capital of France is Paris."
		want      = "Paris is the capital of France"
	)
	tests := []struct {
		name        string
		votes       int
		returns     []vote // len(returns) == expected Judge call count
		wantPass    bool
		wantReasons []string
		wantErr     bool
		errSub      string
	}{
		{
			name:     "single vote calls the judge exactly once and passes",
			votes:    1,
			returns:  []vote{{v: core.JudgeVerdict{Match: true, Reason: "ok"}}},
			wantPass: true,
		},
		{
			name:  "best-of-three majority true passes after three calls",
			votes: 3,
			returns: []vote{
				{v: core.JudgeVerdict{Match: true, Reason: "ok"}},
				{v: core.JudgeVerdict{Match: false, Reason: "missing"}},
				{v: core.JudgeVerdict{Match: true, Reason: "ok"}},
			},
			wantPass: true,
		},
		{
			name:  "best-of-three majority false fails carrying a no-match reason",
			votes: 3,
			returns: []vote{
				{v: core.JudgeVerdict{Match: false, Reason: "wrong meaning"}},
				{v: core.JudgeVerdict{Match: false, Reason: "wrong meaning"}},
				{v: core.JudgeVerdict{Match: true, Reason: "ok"}},
			},
			wantPass:    false,
			wantReasons: []string{"wrong meaning"},
		},
		{
			name:  "even split is a tie hard error with no verdict",
			votes: 2,
			returns: []vote{
				{v: core.JudgeVerdict{Match: true, Reason: "ok"}},
				{v: core.JudgeVerdict{Match: false, Reason: "no"}},
			},
			wantErr: true,
			errSub:  "tie",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			j := newScriptedJudge(t, candidate, want, tt.returns)
			m := NewSemantic(j, tt.votes)
			ev := core.Evidence{Output: core.Output{Answer: candidate}}
			v, err := m.Match(context.Background(), ev, want, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q missing %q", err.Error(), tt.errSub)
				}
				if v.Pass || len(v.Reasons) != 0 || v.Detail != nil {
					t.Fatalf("want zero verdict on error, got %+v", v)
				}
				return
			}
			if v.Pass != tt.wantPass {
				t.Fatalf("Pass=%v want %v; reasons=%v", v.Pass, tt.wantPass, v.Reasons)
			}
			if !tt.wantPass && !reflect.DeepEqual(v.Reasons, tt.wantReasons) {
				t.Fatalf("reasons=%v want %v", v.Reasons, tt.wantReasons)
			}
		})
	}
}

// T011 — error paths: blank expected meaning fails fast before any judge call
// (FR-013); a judge backend error is wrapped, never a guessed verdict (FR-007).
func TestSemanticErrors(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("judge backend boom")
	tests := []struct {
		name        string
		candidate   string
		want        string
		votes       int
		returns     []vote // nil => judge must NOT be called
		errSub      string
		wantWrapped error // when set, assert errors.Is
	}{
		{
			name:      "empty expected meaning fails fast before any judge call",
			candidate: "anything",
			want:      "",
			votes:     1,
			returns:   nil,
			errSub:    "empty",
		},
		{
			name:      "whitespace expected meaning fails fast before any judge call",
			candidate: "anything",
			want:      "   \t\n ",
			votes:     1,
			returns:   nil,
			errSub:    "empty",
		},
		{
			name:        "judge backend error is wrapped with no verdict",
			candidate:   "some answer",
			want:        "some meaning",
			votes:       1,
			returns:     []vote{{err: errBoom}},
			errSub:      "boom",
			wantWrapped: errBoom,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			j := newScriptedJudge(t, tt.candidate, tt.want, tt.returns)
			m := NewSemantic(j, tt.votes)
			ev := core.Evidence{Output: core.Output{Answer: tt.candidate}}
			v, err := m.Match(context.Background(), ev, tt.want, "")
			if err == nil {
				t.Fatalf("want error, got verdict %+v", v)
			}
			if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("error %q missing %q", err.Error(), tt.errSub)
			}
			if tt.wantWrapped != nil && !errors.Is(err, tt.wantWrapped) {
				t.Fatalf("error %v does not wrap %v", err, tt.wantWrapped)
			}
			if v.Pass || len(v.Reasons) != 0 || v.Detail != nil {
				t.Fatalf("want zero verdict on error, got %+v", v)
			}
		})
	}
}
