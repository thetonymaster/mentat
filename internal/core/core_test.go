package core

import "testing"

func TestEvidenceFailureFieldsDefaultZero(t *testing.T) {
	var ev Evidence
	if ev.Failed {
		t.Fatalf("zero Evidence must not be Failed")
	}
	if ev.FailureKind != "" {
		t.Fatalf("zero Evidence FailureKind = %q, want empty", ev.FailureKind)
	}
}

func TestExtractAnswerTrimsWhitespace(t *testing.T) {
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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractAnswer(tt.input); got != tt.want {
				t.Fatalf("ExtractAnswer(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
