package steps

import (
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"testing"
)

// TestIntersectsKnownVerdicts is US2's T016/T017/T019: a corpus of pattern pairs whose
// answer is known independently of this code, with every positive verdict's witness
// re-verified against both patterns.
//
// The corpus exists because a decider's FALSE answers are only as good as the decider.
// A true answer proves itself through its witness; a false one cannot, so the only
// available check is a set of pairs whose verdict was established some other way.
//
// The `fold` rows are not decoration. They are research.md R3: regexp/syntax
// materializes case-folding into rune ranges for a character class but NOT for a
// literal, which compiles to a single-rune instruction carrying FoldCase in Inst.Arg.
// A decider reading Inst.Rune alone sees only 'A' and concludes the program cannot match
// 'a'. The prototype behind this file decided all 780 built-in pairs correctly and got
// this wrong, reporting ^(?i)abc$ and ^abc$ as disjoint when both match "abc".
func TestIntersectsKnownVerdicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{
			name: "exact vs general (the spec's Edge Cases collision)",
			a:    overlappingPair[0], b: overlappingPair[1], want: true,
		},
		{
			name: "two real built-ins differing by one word",
			a:    disjointPair[0], b: disjointPair[1], want: false,
		},
		{name: "disjoint literals", a: `^a$`, b: `^b$`, want: false},
		{
			name: "digits overlap digits-and-dots",
			a:    `^total tokens are under (\d+)$`, b: `^total tokens are under ([0-9.]+)$`, want: true,
		},
		{
			name: "optional plural and optional verb suffix",
			a:    `^at least (\d+) spans? match(?:es)? "([^"]*)"$`, b: `^at least 1 span matches "x"$`, want: true,
		},
		{
			name: "empty capture cannot meet one-or-more",
			a:    `^the result contains ""$`, b: `^the result contains "([^"]+)"$`, want: false,
		},
		{
			name: "fold on a single-rune literal (R3)",
			a:    `^(?i)abc$`, b: `^abc$`, want: true,
		},
		{
			name: "fold materialized into a character class",
			a:    `^(?i)[a-c]$`, b: `^a$`, want: true,
		},
		{
			name: "fold cannot rescue a genuinely different letter",
			a:    `^(?i)abc$`, b: `^abd$`, want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Intersects(tt.a, tt.b)
			if err != nil {
				t.Fatalf("Intersects(%q, %q): %v", tt.a, tt.b, err)
			}
			if got.Intersects != tt.want {
				t.Fatalf("Intersects(%q, %q) = %v, want %v", tt.a, tt.b, got.Intersects, tt.want)
			}
			if !tt.want {
				if got.Witness != "" {
					t.Errorf("a disjoint verdict carries witness %q; it must be empty", got.Witness)
				}
				return
			}
			// FR-011: the witness proves the verdict, so the test verifies it rather
			// than trusting the decider that produced it.
			reA, reB := regexp.MustCompile(tt.a), regexp.MustCompile(tt.b)
			if !reA.MatchString(got.Witness) || !reB.MatchString(got.Witness) {
				t.Errorf("witness %q does not match both patterns (%q: %v, %q: %v)",
					got.Witness, tt.a, reA.MatchString(got.Witness), tt.b, reB.MatchString(got.Witness))
			}
		})
	}
}

// TestIntersectsRefusesWhatItCannotModel is US2's T018(a): FR-012 / SC-010 at the
// pattern level.
//
// Answering "disjoint" for a construct this code misreads would be the silent fallback
// the decider exists to remove — and it would be worse than the gap it replaced, because
// it would carry the word "decided".
func TestIntersectsRefusesWhatItCannotModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		wantSub string
	}{
		{name: "word boundary", pattern: `\bword\b`, wantSub: "word boundary"},
		{name: "non word boundary", pattern: `\Bword`, wantSub: "word boundary"},
		{name: "multi-line begin anchor", pattern: `(?m)^line`, wantSub: "multi-line"},
		{name: "multi-line end anchor", pattern: `(?m)line$`, wantSub: "multi-line"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Both argument positions: a refusal that only fires for the first operand
			// would let the second pattern's construct through unexamined.
			for _, pair := range [][2]string{{tt.pattern, `^x$`}, {`^x$`, tt.pattern}} {
				got, err := Intersects(pair[0], pair[1])
				if err == nil {
					t.Fatalf("Intersects(%q, %q) returned %+v with no error; an unmodelled "+
						"construct must never yield a verdict", pair[0], pair[1], got)
				}
				// strconv.Quote, because the error renders the pattern with %q and a
				// pattern full of backslashes comes back double-escaped. Asserting the
				// raw form fails against a CORRECT message — and it fails ASYMMETRICALLY:
				// `\Bword` has one escape, so the raw substring still aligns inside the
				// quoted form and the row passes by luck, while `\bword\b` has two and
				// does not. One passing row is not evidence the assertion is right.
				if !strings.Contains(err.Error(), strconv.Quote(tt.pattern)) {
					t.Errorf("error does not name the offending pattern %q: %v", tt.pattern, err)
				}
				if !strings.Contains(err.Error(), tt.wantSub) {
					t.Errorf("error does not name the construct (%q): %v", tt.wantSub, err)
				}
			}
		})
	}
}

// TestClosureRefusesUnrecognisedEmptyOp is US2's T018(b), and it is a DIRECT unit test
// rather than a fifth row in the table above, for a reason worth stating.
//
// Go's regexp/syntax defines exactly six EmptyOp bits and all six are classified by the
// decider, so NO pattern string can reach the default-refusal branch. Listing it as a
// pattern-level case would be a test whose red is unachievable — the shape this repo has
// shipped twice (011's T009 and T015). The branch is still worth having: it is what
// refuses the seventh bit a future Go might add, rather than silently treating it as
// satisfiable.
func TestClosureRefusesUnrecognisedEmptyOp(t *testing.T) {
	t.Parallel()

	// A bit outside the six syntax defines today.
	const unknownBit = syntax.EmptyOp(1 << 6)

	if err := emptyOpSupported(unknownBit); err == nil {
		t.Fatal("an unrecognised EmptyOp was accepted; the next assertion Go adds would " +
			"silently join the set this decider believes it models")
	}
}
