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
		// Each of these is WHOLE-TEXT ANCHORED and still unmodellable, which is the
		// point: the anchoring check runs first, so an unanchored fixture would be
		// refused for the wrong reason and this row would assert nothing about the
		// construct it names. The first version of this table did exactly that — `\bword\b`
		// and `(?m)^line` are both unanchored — and the rows only started asserting what
		// they claim once the anchoring refusal existed to shadow them.
		{name: "word boundary", pattern: `^\bword\b$`, wantSub: "word boundary"},
		{name: "non word boundary", pattern: `^\Bword$`, wantSub: "word boundary"},
		{name: "multi-line begin anchor", pattern: `^(?m:^)line$`, wantSub: "multi-line"},
		{name: "multi-line end anchor", pattern: `^line(?m:$)$`, wantSub: "multi-line"},

		// Unanchored patterns. The decider reasons over the LANGUAGE of the compiled
		// program; regexp.MatchString asks whether an unanchored pattern matches
		// ANYWHERE. The two disagree, so no verdict is sound.
		//
		// Found by review, not by this table's first version: before the anchoring check
		// existed, Intersects("a", "ba") answered "disjoint" with no error while
		// regexp.MatchString("ba") is true for both. A silent false-disjoint, in the
		// file that argues at length against exactly that.
		{name: "wholly unanchored", pattern: `a`, wantSub: "not anchored at both ends"},
		{name: "anchored at the start only", pattern: `^a`, wantSub: "not anchored at both ends"},
		{name: "anchored at the end only", pattern: `a$`, wantSub: "not anchored at both ends"},
		// Both anchors PRESENT and still not whole-text: this means "starts with a, OR
		// ends with b". A textual prefix/suffix check would accept it, which is why the
		// real check is on the syntax tree.
		{name: "both anchors, neither branch whole-text", pattern: `^a|b$`, wantSub: "not anchored at both ends"},
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

// TestEmptyOpSupportedRefusesUnrecognisedOp is US2's T018(b), and it is a DIRECT unit test
// rather than a fifth row in the table above, for a reason worth stating.
//
// Go's regexp/syntax defines exactly six EmptyOp bits and all six are classified by the
// decider, so NO pattern string can reach the default-refusal branch. Listing it as a
// pattern-level case would be a test whose red is unachievable — the shape this repo has
// shipped twice (011's T009 and T015). The branch is still worth having: it is what
// refuses the seventh bit a future Go might add, rather than silently treating it as
// satisfiable.
func TestEmptyOpSupportedRefusesUnrecognisedOp(t *testing.T) {
	t.Parallel()

	// A bit outside the six syntax defines today.
	const unknownBit = syntax.EmptyOp(1 << 6)

	if err := emptyOpSupported(unknownBit); err == nil {
		t.Fatal("an unrecognised EmptyOp was accepted; the next assertion Go adds would " +
			"silently join the set this decider believes it models")
	}
}

// # Mutation rehearsals for the decider (2026-09-11) — FR-007, SC-008
//
// Each mutation was applied to disjoint.go, CONFIRMED PRESENT by grep before the test
// run, then reverted and the suite re-observed green. Recording the mutation itself and
// not merely "red occurred", because 011 hit a rehearsal that stayed green only because
// the mutation had not applied, and that is indistinguishable from a working guard.
//
//  1. Drop the rune-class UPPER boundaries (`cuts[rg[1]+1]`) in alphabet().
//     -> STAYED GREEN, and that is CORRECT rather than a missing guard.
//
//     The partition keeps every range's LOWER bound. For a class [c_i, c_{i+1}-1] with
//     representative c_i: if c_i is not in range R, then R's lower bound is itself a
//     kept cut and must be >= c_{i+1}, so R does not intersect the class at all.
//     Therefore a lower-bounds-only partition can only ever OVER-approximate, never
//     under — it cannot produce a false "disjoint". It can produce a false "intersect",
//     and witness re-verification in Intersects catches that loudly as a decider defect.
//
//     So the upper cuts buy precision (they avoid spurious verification failures), not
//     correctness. Worth writing down: the obvious reading of that green is "the
//     alphabet is unguarded", and the obvious reading is wrong.
//
//     1b. Collapse the alphabet to a SINGLE class. -> RED on four rows of
//     TestIntersectsKnownVerdicts. This is the mutation that probes the direction that
//     matters: fewer representatives means missed transitions, which is exactly the
//     false-disjoint direction 1 could not reach.
//
//  2. `emptyOpSupported` returns nil for every assertion.
//     -> RED on TestIntersectsRefusesWhatItCannotModel and
//     TestEmptyOpSupportedRefusesUnrecognisedOp. A decider that silently models \b would
//     answer "disjoint" for patterns it cannot reason about.
//
//  3. `foldRanges` ignores FoldCase.
//     -> RED on the fold rows. This mutation reproduces research.md R3 exactly — the
//     defect the prototype shipped with — so it is the one rehearsal with a known-good
//     expected output to compare against rather than a prediction.
//
//  4. `accepts` calls closure with atEnd=false, collapsing the two closures into one.
//     -> RED broadly, because every $-anchored pattern becomes unsatisfiable.
//
// deciderFillers is the filler set the sentence-corpus cross-check uses. Named once so
// the agreement test below and metadata_test.go cannot drift apart about what "the
// generated corpus" means.
var deciderFillers = []string{"x", "", "a b", "1", "2nd", "true", "0.5", "tool-name", "a/b.c"}

// corpusFor generates every sentence the cross-check's generator derives from patterns.
func corpusFor(t *testing.T, patterns []string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, p := range patterns {
		re, err := syntax.Parse(p, syntax.Perl)
		if err != nil {
			t.Fatalf("parse %q: %v", p, err)
		}
		for _, f := range deciderFillers {
			for _, s := range expandPattern(re.Simplify(), f, 0) {
				if !seen[s] {
					seen[s] = true
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// TestDeciderAgreesWithTheSentenceCorpus is US3's T039 and FR-015(c).
//
// The two mechanisms must not disagree. Any sentence the generator produces that matches
// two patterns is a shared string, so the decider MUST report that pair as intersecting.
// A disagreement means one of them is broken, and this test is what turns that into a
// failure rather than a puzzle.
//
// # The positive control is mandatory, not decorative
//
// Verified 2026-09-11: over the real built-in set, NO generated sentence matches two
// patterns. So the natural input to this check is the EMPTY SET, and without a control
// this test would be green while asserting nothing at all — the shape of defect this
// whole feature exists to remove, reproduced in its own verification. The control adds a
// deliberately colliding pattern so the loop is provably entered.
func TestDeciderAgreesWithTheSentenceCorpus(t *testing.T) {
	t.Parallel()

	docs := StepDocs()
	builtin := make([]string, 0, len(docs))
	for _, d := range docs {
		builtin = append(builtin, d.Pattern)
	}

	tests := []struct {
		name     string
		patterns []string
		// wantChecked is the minimum number of multi-match sentences this row must
		// find. The real set is expected to be 0; the control must be > 0 or the
		// agreement check never ran.
		wantChecked int
	}{
		{name: "the real built-in set", patterns: builtin},
		{
			name:        "positive control: a deliberately colliding pattern",
			patterns:    append(append([]string{}, builtin...), overlappingPair[1]),
			wantChecked: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			compiled := make([]*regexp.Regexp, len(tt.patterns))
			for i, p := range tt.patterns {
				compiled[i] = regexp.MustCompile(p)
			}
			corpus := corpusFor(t, tt.patterns)
			if len(corpus) < 500 {
				t.Fatalf("generated only %d sentences; the generator produced far less than "+
					"expected and this check would be near-vacuous", len(corpus))
			}

			checked := 0
			for _, sentence := range corpus {
				var hits []int
				for i, re := range compiled {
					if re.MatchString(sentence) {
						hits = append(hits, i)
					}
				}
				if len(hits) < 2 {
					continue
				}
				checked++
				// Every pair sharing this sentence must be reported intersecting.
				for x := 0; x < len(hits); x++ {
					for y := x + 1; y < len(hits); y++ {
						a, b := tt.patterns[hits[x]], tt.patterns[hits[y]]
						got, err := Intersects(a, b)
						if err != nil {
							t.Fatalf("decider refused %q / %q, which share the sentence %q: %v", a, b, sentence, err)
						}
						if !got.Intersects {
							t.Errorf("DECIDER DEFECT: %q and %q both match %q, but the decider "+
								"reports them disjoint. The two mechanisms disagree, so one is "+
								"wrong — and the corpus has a concrete counterexample.", a, b, sentence)
						}
					}
				}
			}
			if checked < tt.wantChecked {
				t.Fatalf("found %d multi-match sentences, want at least %d; with none the "+
					"agreement assertion never executed", checked, tt.wantChecked)
			}
			t.Logf("%d sentences, %d multi-match", len(corpus), checked)
		})
	}
}

// sampleAlphabet is the rune set the mutator substitutes: characters that actually
// appear in step patterns, plus the ones most likely to expose a boundary bug. `z` is
// deliberately present and deliberately absent from deciderFillers — it is what lets a
// control pair exist whose shared string the generator alone cannot produce.
var sampleAlphabet = []rune(`abcxz01239 "'/.-_:*+?[](){}|^$\` + "\t\n")

// mutationBase caps how many generated sentences are mutated per pair. The systematic
// mutation below is sentences x positions x alphabet, so an uncapped base over the real
// pattern set is minutes of work in a unit lane.
const mutationBase = 8

// sampleStrings returns a deterministic set of candidate strings for a pattern pair:
// every sentence the generator derives from EITHER pattern, plus SYSTEMATIC single-rune
// replacements at every position of the first few, drawn from sampleAlphabet.
//
// # Systematic, not random
//
// The first version used a seeded PRNG. It was deterministic, but it could not be shown
// to find any particular neighbour: hitting one specific character at one specific
// position is a ~0.1% shot per attempt, so its positive control passed only because the
// shared string was already in the BASE corpus — the half that needs no verification.
// Review caught that the mutation machinery was never exercised at all, and that
// `rounds = 0` would have left the control green.
//
// Enumerating position x alphabet removes the question. Any string one replacement away
// from a generated sentence is now guaranteed to be produced, so a control pair whose
// shared string is exactly that is a real test of the mutator.
//
// Deterministic matters independently: FR-015 must hold in `make ci`, where no fuzzing
// runs (R7). A differential check that only samples under `-fuzz` is green in CI while
// never having sampled anything.
func sampleStrings(t *testing.T, a, b string) []string {
	t.Helper()
	base := corpusFor(t, []string{a, b})
	out := append([]string{}, base...)
	for i, s := range base {
		if i >= mutationBase {
			break
		}
		r := []rune(s)
		for pos := range r {
			for _, sub := range sampleAlphabet {
				if r[pos] == sub {
					continue
				}
				out = append(out, string(r[:pos])+string(sub)+string(r[pos+1:]))
			}
		}
	}
	return out
}

// TestDeciderDisjointVerdictsSurviveSampling is US3's T038 and FR-015(b).
//
// It attacks the one direction a decider cannot prove about itself. A TRUE verdict
// carries a witness anyone can check; a FALSE verdict is only as good as the code. So
// this samples strings against pairs the decider called disjoint and fails if any string
// matches both.
//
// A failure here is a DECIDER defect, never a disjointness result — and the message says
// so, because the two would otherwise be confused at exactly the moment it matters (D4).
//
// # The positive control
//
// Over the real built-in set every pair is disjoint, so nothing in the negative rows
// demonstrates that the sampler can find a shared string when one exists. The control
// pair has one, and the sampler must locate it. Without that, "no shared string found"
// would be indistinguishable from "this sampler never finds anything".
func TestDeciderDisjointVerdictsSurviveSampling(t *testing.T) {
	t.Parallel()

	t.Run("positive control: the sampler finds a real shared string", func(t *testing.T) {
		t.Parallel()

		// The shared string is `the result contains "z"`. `z` appears in sampleAlphabet
		// and in NO deciderFillers entry, so the generator cannot produce this sentence
		// and only the mutation loop can reach it. That is the point: the previous
		// control's shared string was already in the base corpus, so it proved nothing
		// about the mutator.
		a, b := `^the result contains "(.)"$`, `^the result contains "z"$`
		reA, reB := regexp.MustCompile(a), regexp.MustCompile(b)
		found := ""
		for _, s := range sampleStrings(t, a, b) {
			if reA.MatchString(s) && reB.MatchString(s) {
				found = s
				break
			}
		}
		if found == "" {
			t.Fatal("the sampler found no shared string for a pair that provably has one; " +
				"every negative result it produces below would be worthless")
		}
		t.Logf("control: sampler located %q", found)
	})

	t.Run("pairs the decider called disjoint", func(t *testing.T) {
		t.Parallel()

		docs := StepDocs()
		patterns := make([]string, 0, len(docs))
		for _, d := range docs {
			patterns = append(patterns, d.Pattern)
		}
		// A representative slice rather than all 780 pairs: the full cross-product of
		// pairs x sentences x mutations is minutes of work for a unit lane. The fuzz
		// target is where unbounded exploration belongs.
		checked := 0
		for i := 0; i < len(patterns) && i < 8; i++ {
			for j := i + 1; j < len(patterns) && j < 8; j++ {
				a, b := patterns[i], patterns[j]
				got, err := Intersects(a, b)
				if err != nil {
					t.Fatalf("decider refused %q / %q: %v", a, b, err)
				}
				if got.Intersects {
					continue
				}
				reA, reB := regexp.MustCompile(a), regexp.MustCompile(b)
				for _, s := range sampleStrings(t, a, b) {
					if reA.MatchString(s) && reB.MatchString(s) {
						t.Fatalf("DECIDER DEFECT: it reported %q and %q disjoint, but %q matches both",
							a, b, s)
					}
				}
				checked++
			}
		}
		if checked == 0 {
			t.Fatal("no disjoint pair was sampled; this assertion never ran")
		}
		t.Logf("sampled %d disjoint pairs", checked)
	})
}

// FuzzDecider is US3's T040. Its SEED CORPUS runs under plain `go test`, which is what
// `make ci` exercises; `-fuzz` extends it without the gate depending on that having
// happened (R7).
//
// It asserts the two properties that must hold for any input, valid or not: a positive
// verdict's witness really matches both patterns, and no input produces both an error
// and a verdict.
func FuzzDecider(f *testing.F) {
	f.Add(overlappingPair[0], overlappingPair[1])
	f.Add(disjointPair[0], disjointPair[1])
	f.Add(`^(?i)abc$`, `^abc$`)
	f.Add(`^a$`, `^a$`)
	f.Add(`\bword\b`, `^x$`)
	f.Add(`^`, `$`)
	f.Add(``, ``)
	f.Add(`(`, `^x$`)

	f.Fuzz(func(t *testing.T, a, b string) {
		got, err := Intersects(a, b)
		if err != nil {
			// A refusal must not also claim a verdict.
			if got.Intersects || got.Witness != "" {
				t.Fatalf("Intersects(%q, %q) returned both an error and a verdict %+v", a, b, got)
			}
			return
		}
		if !got.Intersects {
			if got.Witness != "" {
				t.Fatalf("a disjoint verdict for %q / %q carries witness %q", a, b, got.Witness)
			}
			// THE DEFECT CLASS THIS TARGET EXISTS FOR.
			//
			// The first version of this fuzz body asserted only witness validity and
			// error/verdict exclusivity — both properties of the POSITIVE direction,
			// which the contract already says proves itself. Nothing in it could detect
			// a false "disjoint", which is the one direction that cannot. Review
			// demonstrated the gap concretely: the unanchored-pattern defect was a
			// false disjoint, and this target passed it.
			//
			// Brute force is affordable here because the bound is tiny and the alphabet
			// is drawn from the patterns themselves, where any shared string must live.
			reA, errA := regexp.Compile(a)
			reB, errB := regexp.Compile(b)
			if errA != nil || errB != nil {
				return
			}
			if shared := sharedStringUpTo(reA, reB, fuzzAlphabet(a, b), 3); shared != "" {
				t.Fatalf("DECIDER DEFECT: reported %q and %q disjoint, but %q matches both",
					a, b, shared)
			}
			return
		}
		// FR-011 under arbitrary input: the witness is the proof, so it must hold.
		reA, errA := regexp.Compile(a)
		reB, errB := regexp.Compile(b)
		if errA != nil || errB != nil {
			t.Fatalf("decider accepted patterns regexp rejects: %q (%v), %q (%v)", a, errA, b, errB)
		}
		if !reA.MatchString(got.Witness) || !reB.MatchString(got.Witness) {
			t.Fatalf("DECIDER DEFECT: witness %q does not match both %q and %q", got.Witness, a, b)
		}
	})
}

// TestUndecidablePatternIsReportedNotFatal is the guard BLOCK 2 lacked.
//
// A contributed phrase containing `\b`, `\B` or a `(?m)` anchor is LEGAL — V2
// (isAnchored) admits it and godog runs the step correctly — so a decider refusal must
// not take `mentat.Validate` down with it. Before this, the refusal propagated out of
// EngineStepChecks and Validate returned `nil, err`: a validator refusing a suite the
// runner executes, which is the mirror image of the drift D7 removed.
func TestUndecidablePatternIsReportedNotFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
	}{
		{name: "word boundary", pattern: `^\bthe widget is "([^"]*)"\b$`},
		{name: "multi-line anchor", pattern: `^the gadget is fine(?m:$)$`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Precondition: the phrase really is legal by the existing rules. If it were
			// not, this test would be asserting about a pattern no consumer can ship.
			if !isAnchored(tt.pattern) {
				t.Fatalf("fixture %q fails V2, so a refusal here would be moot", tt.pattern)
			}

			labelled := []LabelledPattern{
				{Pattern: overlappingPair[0], Source: SourceBuiltin},
				{Pattern: tt.pattern, Source: SourceContributed, Comparator: "widgets"},
			}
			got, err := patternOverlapFindings(labelled, Source{})
			if err != nil {
				t.Fatalf("a refusal on a LEGAL contributed phrase must be a finding, not an "+
					"error — Validate would return no findings at all: %v", err)
			}

			var undecidable []Finding
			for _, f := range got {
				if f.Class == "pattern-undecidable" {
					undecidable = append(undecidable, f)
				}
			}
			// Once per pattern, not once per pair: pairing against 40 built-ins would
			// otherwise emit 40 complaints about one defect.
			if len(undecidable) != 1 {
				t.Fatalf("want exactly 1 pattern-undecidable finding, got %d: %+v", len(undecidable), got)
			}
			for _, want := range []string{strconv.Quote(tt.pattern), "widgets"} {
				if !strings.Contains(undecidable[0].Message, want) {
					t.Errorf("message does not name %s:\n  %s", want, undecidable[0].Message)
				}
			}
		})
	}
}

// sharedStringUpTo brute-forces every string of length <= maxLen over alphabet and
// returns the first that matches both patterns, or "".
//
// Exhaustive rather than sampled, which is what lets a caller treat "" as meaningful for
// short strings. This is the check that finds a FALSE DISJOINT — the one direction the
// decider cannot prove about itself.
func sharedStringUpTo(reA, reB *regexp.Regexp, alphabet []rune, maxLen int) string {
	var rec func(prefix []rune) string
	rec = func(prefix []rune) string {
		s := string(prefix)
		if reA.MatchString(s) && reB.MatchString(s) {
			return s
		}
		if len(prefix) == maxLen {
			return ""
		}
		for _, r := range alphabet {
			if found := rec(append(prefix, r)); found != "" {
				return found
			}
		}
		return ""
	}
	return rec(nil)
}

// fuzzAlphabet picks a handful of runes for the brute-force check: any shared string is
// built from characters both patterns can match, so the pattern text is where to look.
// Capped hard — the search is |alphabet|^maxLen and this runs on every fuzz input.
func fuzzAlphabet(a, b string) []rune {
	seen := map[rune]bool{}
	var out []rune
	for _, r := range a + b {
		if r == '^' || r == '$' || r == '\\' || r == '(' || r == ')' || r == '[' || r == ']' ||
			r == '*' || r == '+' || r == '?' || r == '|' || r == '{' || r == '}' || r == '.' {
			continue
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
		if len(out) == 4 {
			break
		}
	}
	// An empty pattern pair still deserves a probe of the empty string, which
	// sharedStringUpTo tests before recursing.
	return out
}
