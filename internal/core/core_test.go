package core

import "testing"

func TestExtractAnswerTrimsWhitespace(t *testing.T) {
	if got := ExtractAnswer("  the answer\n"); got != "the answer" {
		t.Fatalf("ExtractAnswer = %q", got)
	}
}
