package core

import (
	"regexp"
	"strings"
	"testing"
)

func TestEvidenceFailureFieldsDefaultZero(t *testing.T) {
	var ev Evidence
	if ev.Failed {
		t.Fatalf("zero Evidence must not be Failed")
	}
	if ev.FailureKind != "" {
		t.Fatalf("zero Evidence FailureKind = %q, want empty", ev.FailureKind)
	}
}

// TestExtractAnswerWholeIsTrimSpace pins the backward-compat guarantee (US8): the
// zero-value policy — what every hand-built RunSpec and every target with no
// `extract` config carries — must be byte-identical to today's strings.TrimSpace
// on the full stdout, and must never return an error. Explicit `whole` mode is
// identical to the zero value.
func TestExtractAnswerWholeIsTrimSpace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"leading and trailing whitespace", "  the answer\n", "the answer"},
		{"already trimmed is unchanged", "the answer", "the answer"},
		{"internal whitespace preserved", "  the  answer\t", "the  answer"},
		{"empty input", "", ""},
		{"only whitespace", " \t\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Premise: the zero-value policy is defined to equal TrimSpace byte-for-byte.
			if premise := strings.TrimSpace(tt.input); premise != tt.want {
				t.Fatalf("test premise wrong: TrimSpace(%q) = %q, want %q", tt.input, premise, tt.want)
			}
			got, err := ExtractAnswer(tt.input, ExtractPolicy{})
			if err != nil {
				t.Fatalf("ExtractAnswer(%q, zero) unexpected err: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ExtractAnswer(%q, zero) = %q, want %q", tt.input, got, tt.want)
			}
			gotWhole, err := ExtractAnswer(tt.input, ExtractPolicy{Mode: ExtractWhole})
			if err != nil {
				t.Fatalf("ExtractAnswer(%q, whole) unexpected err: %v", tt.input, err)
			}
			if gotWhole != tt.want {
				t.Fatalf("ExtractAnswer(%q, whole) = %q, want %q", tt.input, gotWhole, tt.want)
			}
		})
	}
}

// TestExtractAnswerModes covers the marker and pattern policies plus every error
// path (US8, data-model "Answer extraction"): marker returns text after the LAST
// occurrence, trimmed; a missing marker is a hard error naming the marker; pattern
// returns the first capture group of the first match; no match is a hard error
// naming the pattern; an unknown mode errors. No error path leaks a non-empty
// answer (no silent fallback — Constitution IV).
func TestExtractAnswerModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		stdout  string
		policy  ExtractPolicy
		want    string
		wantErr bool
		errSub  string
	}{
		{
			name:   "marker returns text after the single occurrence trimmed",
			stdout: "prelude\nANSWER:  forty-two  \n",
			policy: ExtractPolicy{Mode: ExtractMarker, Marker: "ANSWER:"},
			want:   "forty-two",
		},
		{
			name:   "marker returns text after the LAST of several occurrences",
			stdout: "ANSWER: first\nmid\nANSWER: second\ntail\nANSWER: final\n",
			policy: ExtractPolicy{Mode: ExtractMarker, Marker: "ANSWER:"},
			want:   "final",
		},
		{
			name:   "marker at very end yields empty answer without error",
			stdout: "chatter ANSWER:",
			policy: ExtractPolicy{Mode: ExtractMarker, Marker: "ANSWER:"},
			want:   "",
		},
		{
			name:    "marker absent is a hard error naming the marker",
			stdout:  "no such token here",
			policy:  ExtractPolicy{Mode: ExtractMarker, Marker: "<<RESULT>>"},
			wantErr: true,
			errSub:  "<<RESULT>>",
		},
		{
			// Defensive guard symmetric with the nil-pattern case: an empty marker
			// makes strings.LastIndex return len(stdout) (>=0), which would silently
			// extract the empty string as a "successful" answer. Marker mode with an
			// empty marker is a hard error, never a silent empty answer (Constitution IV).
			name:    "marker mode with an empty marker is a hard error",
			stdout:  "anything at all",
			policy:  ExtractPolicy{Mode: ExtractMarker, Marker: ""},
			wantErr: true,
			errSub:  "non-empty marker",
		},
		{
			name:   "pattern returns first capture group of first match",
			stdout: "id=alpha\nid=beta\n",
			policy: ExtractPolicy{Mode: ExtractPattern, Pattern: regexp.MustCompile(`id=(\w+)`)},
			want:   "alpha",
		},
		{
			name:   "pattern captures only the group, not the whole match",
			stdout: "result: [answer here] trailing",
			policy: ExtractPolicy{Mode: ExtractPattern, Pattern: regexp.MustCompile(`\[(.*?)\]`)},
			want:   "answer here",
		},
		{
			name:    "pattern no match is a hard error naming the pattern",
			stdout:  "nothing matches",
			policy:  ExtractPolicy{Mode: ExtractPattern, Pattern: regexp.MustCompile(`token=(\d+)`)},
			wantErr: true,
			errSub:  `token=(\d+)`,
		},
		{
			// Defensive guard: a hand-built policy (not via config, which requires a
			// pattern for pattern mode) that omits the compiled regexp is a hard error,
			// never a silent empty answer.
			name:    "pattern mode with a nil compiled pattern is a hard error",
			stdout:  "anything",
			policy:  ExtractPolicy{Mode: ExtractPattern},
			wantErr: true,
			errSub:  "compiled pattern",
		},
		{
			// Defensive guard: a hand-built policy (config rejects group-less patterns)
			// whose pattern matches but has no capture group has nothing to extract.
			name:    "pattern with zero capture groups is a hard error even on a match",
			stdout:  "foo bar",
			policy:  ExtractPolicy{Mode: ExtractPattern, Pattern: regexp.MustCompile(`foo`)},
			wantErr: true,
			errSub:  "no capture group",
		},
		{
			name:    "unknown mode is a hard error naming the mode",
			stdout:  "whatever",
			policy:  ExtractPolicy{Mode: "telepathy"},
			wantErr: true,
			errSub:  "telepathy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExtractAnswer(tt.stdout, tt.policy)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractAnswer(%q) err=%v wantErr=%v", tt.stdout, err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q does not name %q", err.Error(), tt.errSub)
				}
				if got != "" {
					t.Fatalf("error path returned %q, want empty (no silent fallback)", got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("ExtractAnswer(%q) = %q, want %q", tt.stdout, got, tt.want)
			}
		})
	}
}
