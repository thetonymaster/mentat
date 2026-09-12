package steps

import (
	"errors"
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
		// Per-branch-anchored alternation must be DECIDED, not refused. V2's
		// anchoredShape accepts `^a$|^b$` explicitly, and its meaning IS whole-text, so
		// refusing it would exclude a legal phrase from overlap checking entirely — and
		// the first version of the anchoring check did exactly that, with a message
		// claiming the pattern was unanchored and substring-matching. Both false of it.
		{
			name: "alternation anchored on every branch, overlapping",
			a:    `^the result contains "x"$|^the result contains "y"$`,
			b:    overlappingPair[0],
			want: true,
		},
		{
			name: "alternation anchored on every branch, disjoint",
			a:    `^the widget is "a"$|^the widget is "b"$`,
			b:    `^the widget is "c"$`,
			want: false,
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
		{name: "wholly unanchored", pattern: `a`, wantSub: "anchored at both ends"},
		{name: "anchored at the start only", pattern: `^a`, wantSub: "anchored at both ends"},
		{name: "anchored at the end only", pattern: `a$`, wantSub: "anchored at both ends"},
		// Both anchors PRESENT and still not whole-text: this means "starts with a, OR
		// ends with b". A textual prefix/suffix check would accept it, which is why the
		// real check is on the syntax tree.
		{name: "both anchors, neither branch whole-text", pattern: `^a|b$`, wantSub: "anchored at both ends"},
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

		// Two character classes overlapping only on `z`. Neither pattern contains a
		// literal the generator can emit as the shared string: expandPattern substitutes a
		// FILLER into a character class, and no filler is `y`, `z` or `w`.
		//
		// The reasoning is not the guarantee, though. THREE successive versions of this
		// control were satisfied straight from the base corpus, each for a different
		// reason: the shared string was itself a generated sentence; then `corpusFor`
		// runs on BOTH patterns and the second was a pure literal, so the generator
		// emitted the target directly; then the second pattern was `(z)` — a capture
		// around a literal, which expandPattern emits just the same. Every version came
		// with a correct-sounding argument for why the mutation loop was needed.
		//
		// The third was caught by the assertion below rather than by review, which is
		// the only reason to prefer an assertion to an argument.
		//
		// So the property is ASSERTED below instead of argued: the located string must
		// not be in the base corpus. That holds regardless of which fixtures anyone
		// picks later, which is the difference between reasoning about a guard and
		// measuring it — the thing this whole feature is about.
		a, b := `^the result contains "[yz]"$`, `^the result contains "[wz]"$`
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
		// The mutation loop, specifically. If the base corpus already contains the
		// shared string then deleting every mutation would leave this control green,
		// and the machinery it exists to verify would be untested.
		for _, s := range corpusFor(t, []string{a, b}) {
			if s == found {
				t.Fatalf("the control's shared string %q is already in the BASE corpus, so "+
					"the mutation loop is not exercised by it — deleting every mutation "+
					"would leave this control green", found)
			}
		}
		t.Logf("control: sampler located %q, and it is NOT in the base corpus", found)
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
			// Test the BOOL, not the string: the empty string is a legitimate witness
			// (both `^$` and `^a*$` match it), so `shared != ""` would silently skip
			// exactly the case where a false-disjoint is easiest to produce.
			if shared, ok := sharedStringUpTo(reA, reB, fuzzAlphabet(a, b), 3); ok {
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
// TestSearchBudgetRefusesRatherThanReportingDisjoint pins the bound added after review
// found `searchProduct` had none at all — no state cap, no memory cap, no deadline, and
// no cancellation — while `mentat.Validate` runs it over CONSUMER-SUPPLIED patterns.
//
// The product automaton is worst-case exponential in the two programs' state counts, so
// "no blowup could be produced by hand" is a measurement, not a bound. This is the same
// distinction the whole feature is about, turned on the decider's cost instead of its
// verdict.
//
// # The direction the budget must fail in
//
// Exhausting the budget means "I could not decide this pair", which is a REFUSAL. It
// must never become a disjoint verdict — that is FR-012 exactly, and a budget that
// silently answered "disjoint" on the hard cases would be the worst possible version of
// this feature: a gate that gets quieter the more it is stressed.
func TestSearchBudgetRefusesRatherThanReportingDisjoint(t *testing.T) {
	t.Parallel()

	// A pair that genuinely INTERSECTS, so a wrong answer is unmistakable: anything
	// other than a refusal here is either a false disjoint or a silent success.
	a, err := compileNFA(`^the result contains "([^"]*)"$`)
	if err != nil {
		t.Fatalf("compile a: %v", err)
	}
	b, err := compileNFA(`^the result contains "revenue"$`)
	if err != nil {
		t.Fatalf("compile b: %v", err)
	}

	// Budget 1: one product state is not enough to reach acceptance for this pair.
	_, found, _, err := searchProduct(a, b, 1)
	if err == nil {
		t.Fatalf("an exhausted budget returned no error (found=%v); a decider that cannot "+
			"finish must refuse, never report a verdict", found)
	}
	if found {
		t.Error("an exhausted budget reported a positive verdict")
	}
	if !errors.Is(err, errSearchBudget) {
		t.Errorf("budget exhaustion must be identifiable with errors.Is so callers can "+
			"classify it as undecidable rather than fatal; got %v", err)
	}

	// The real constant must still decide this pair, or the bound is set below the
	// feature's own working set and the gate would refuse its own built-ins.
	got, err := Intersects(`^the result contains "([^"]*)"$`, `^the result contains "revenue"$`)
	if err != nil {
		t.Fatalf("the shipped budget refused a pair the built-in gate must decide: %v", err)
	}
	if !got.Intersects {
		t.Fatal("the shipped budget changed a known-intersecting verdict")
	}

	// And the SHIPPED budget must actually fire on a legal pattern pair, or everything
	// above only proves that passing budget=1 works.
	//
	// Measured 2026-09-12 with these patterns at increasing n: n=4 365µs, n=8 5.37ms,
	// n=12 83.5ms — then the bound stops it at n=16 and n=20 in ~255ms. That is ~15×
	// per +4, i.e. genuinely exponential, and both patterns are ANCHORED and legal, so a
	// consumer can contribute them and reach this through mentat.Validate.
	//
	// This row exists because the feature shipped claiming the opposite. contracts/
	// decider.md recorded the unbounded path as acceptable on the grounds that "no
	// blowup could be produced (best adversarial attempt: 532µs)" — which was true of
	// the attempts made and false of the code, exactly the evidence-versus-proof gap
	// 013 was raised to close. It took one deliberate construction to refute.
	//
	// The refusal is deterministic (it counts STATES, not time), so a slow machine
	// changes the duration above and not the outcome asserted here.
	adversarialA, adversarialB := `^[ab]*a[ab]{16}$`, `^[ab]*b[ab]{16}x$`
	if !isAnchored(adversarialA) || !isAnchored(adversarialB) {
		t.Fatal("the adversarial fixtures must be LEGAL phrases, or this proves nothing about reachable input")
	}
	if _, err := Intersects(adversarialA, adversarialB); !errors.Is(err, errSearchBudget) {
		t.Errorf("the shipped budget did not fire on a pair measured to blow up: %v", err)
	}
}

// TestBudgetRefusalOnAPairIsAFindingNotAnError is the FR-018 half of the budget, and it
// exists because the obvious implementation reintroduces the exact defect FR-018 was
// written to remove.
//
// `patternOverlapFindings` pre-scans each pattern with compileNFA and reports the
// undecidable ones. A budget refusal cannot be caught there: it is a property of the
// PAIR, not of either pattern, so it surfaces from Intersects inside the pair loop —
// whose error branch was written as "unreachable" and returns `nil, err` all the way out
// of mentat.Validate. Routing a budget refusal through it would make the validator
// refuse a suite the runner executes, which is the mirror of drift D7.
func TestBudgetRefusalOnAPairIsAFindingNotAnError(t *testing.T) {
	t.Parallel()

	labelled := []LabelledPattern{
		{Pattern: overlappingPair[0], Source: SourceBuiltin},
		{Pattern: overlappingPair[1], Source: SourceContributed, Comparator: "widgets"},
	}

	got, err := patternOverlapFindingsWithBudget(labelled, Source{}, 1)
	if err != nil {
		t.Fatalf("a budget refusal must be a finding, not an error out of Validate: %v", err)
	}

	var undecidable []Finding
	for _, f := range got {
		if f.Class == "pattern-undecidable" {
			undecidable = append(undecidable, f)
		}
	}
	if len(undecidable) != 1 {
		t.Fatalf("want exactly 1 pattern-undecidable finding for the refused PAIR, got %d: %+v", len(undecidable), got)
	}
	// Both patterns, because the undecidability belongs to the pair: naming one would
	// send the author looking for a defect in a pattern that may be perfectly fine.
	for _, want := range []string{strconv.Quote(overlappingPair[0]), strconv.Quote(overlappingPair[1])} {
		if !strings.Contains(undecidable[0].Message, want) {
			t.Errorf("message does not name %s:\n  %s", want, undecidable[0].Message)
		}
	}
	// And it must NOT be reported as an overlap, which would be a verdict it did not reach.
	for _, f := range got {
		if f.Class == "pattern-overlap" {
			t.Errorf("a refused pair was reported as an overlap: %s", f.Message)
		}
	}
}

// TestOverlapBudgetIsSharedAcrossPairs pins that the decider's allowance bounds the
// WORK, not merely each search.
//
// The first version of the bound handed every pair a fresh maxProductStates. That reads
// like a fix and is not one: an overlap analysis decides N(N-1)/2 + 40N pairs, so ~990
// searches at 20 contributed phrases, each entitled to the full per-pair budget —
// minutes of CPU inside mentat.Validate. Bounding the inner loop while leaving the outer
// one unbounded is the same defect one level out, which is how it got shipped and then
// caught in review.
//
// # What distinguishes the two designs
//
// Under a per-pair budget B, every pair costing ≤ B decides. Under a shared allowance B,
// an early pair spends it and later pairs are refused even though each would have been
// affordable alone. So the discriminating observation is: identical, individually-cheap
// pairs where the FIRST decides and a LATER one does not.
func TestOverlapBudgetIsSharedAcrossPairs(t *testing.T) {
	t.Parallel()

	// Three mutually-overlapping patterns → three pairs, each individually cheap.
	labelled := []LabelledPattern{
		{Pattern: `^the widget is "([^"]*)"$`, Source: SourceContributed, Comparator: "w1"},
		{Pattern: `^the widget is "([a-z]*)"$`, Source: SourceContributed, Comparator: "w2"},
		{Pattern: `^the widget is "revenue"$`, Source: SourceContributed, Comparator: "w3"},
	}

	countClass := func(fs []Finding, class string) int {
		n := 0
		for _, f := range fs {
			if f.Class == class {
				n++
			}
		}
		return n
	}

	// With the shipped allowance every pair decides, or the bound is set below the
	// feature's own working set and everything below would be measuring a broken
	// baseline rather than the sharing.
	full, err := patternOverlapFindings(labelled, Source{})
	if err != nil {
		t.Fatalf("patternOverlapFindings: %v", err)
	}
	if got := countClass(full, "pattern-undecidable"); got != 0 {
		t.Fatalf("the shipped allowance refused %d pair(s) of three cheap ones: %+v", got, full)
	}
	if got := countClass(full, "pattern-overlap"); got != 3 {
		t.Fatalf("want 3 overlapping pairs decided, got %d: %+v", got, full)
	}

	// Cost of the FIRST pair alone, measured rather than assumed, so the budget below
	// is expressed in the same units the implementation spends.
	_, firstPairStates, err := intersectsCounting(labelled[0].Pattern, labelled[1].Pattern, maxProductStates)
	if err != nil {
		t.Fatalf("measuring the first pair: %v", err)
	}
	if firstPairStates <= 0 {
		t.Fatalf("first pair reported %d states; the measurement below would be meaningless", firstPairStates)
	}

	// An allowance sufficient for ONE pair and no more. Per-pair semantics would decide
	// all three, because each costs about the same.
	got, err := patternOverlapFindingsWithBudget(labelled, Source{}, firstPairStates)
	if err != nil {
		t.Fatalf("an exhausted allowance must report, not error: %v", err)
	}
	undecidable := countClass(got, "pattern-undecidable")
	if undecidable == 0 {
		t.Fatalf("no pair was refused with an allowance of %d states, so the budget is being "+
			"RESET PER PAIR and bounds one search rather than the work: %+v", firstPairStates, got)
	}
	if countClass(got, "pattern-overlap") == 0 {
		t.Errorf("every pair was refused; the allowance should be spent by earlier pairs, not "+
			"denied to all of them: %+v", got)
	}

	// EXACTLY ONE summary for the exhausted remainder, not one finding per unchecked
	// pair. Reporting per pair is Θ(N²) findings to say a single thing — the mistake the
	// per-pattern branch already rejects ("40 identical complaints about one defect"),
	// and at 1000 phrases it is ~500k formatted messages nobody can read.
	if undecidable != 1 {
		t.Errorf("want 1 summary finding for the unchecked remainder, got %d; a per-pair "+
			"report is quadratic in the phrase count: %+v", undecidable, got)
	}
	// The summary must QUANTIFY what was lost. "Some pairs were skipped" leaves an
	// author unable to tell a rounding error from most of their coverage.
	for _, f := range got {
		if f.Class != "pattern-undecidable" {
			continue
		}
		for _, want := range []string{"not checked for overlap", "budget"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("summary does not mention %q:\n  %s", want, f.Message)
			}
		}
		if !strings.ContainsAny(f.Message, "0123456789") {
			t.Errorf("summary reports no counts, so the lost coverage is unquantified:\n  %s", f.Message)
		}
	}
}

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
// TestSharedStringUpToDistinguishesEmptyWitnessFromNoWitness pins the fix directly,
// because the bug it closes is invisible from the sampler's own green.
//
// Before the bool, the first row below returned ("", ...) and the caller's `!= ""` read
// it as "no shared string" — so the sampler was structurally unable to report a
// false-disjoint on the empty string, while looking exactly like a sampler that had
// checked and found nothing.
func TestSharedStringUpToDistinguishesEmptyWitnessFromNoWitness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		a, b   string
		want   string
		wantOK bool
	}{
		// Both match ONLY the empty string: the witness is "" and it is real.
		{name: "empty string is a real witness", a: `^$`, b: `^a*$`, want: "", wantOK: true},
		// Genuinely disjoint: "" is not a witness and none exists.
		{name: "no shared string at all", a: `^a$`, b: `^b$`, want: "", wantOK: false},
		// A non-empty witness still works, so the bool did not replace the string.
		{name: "non-empty witness", a: `^a$`, b: `^[ab]$`, want: "a", wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reA, reB := regexp.MustCompile(tt.a), regexp.MustCompile(tt.b)
			got, ok := sharedStringUpTo(reA, reB, []rune{'a', 'b'}, 2)
			if ok != tt.wantOK {
				t.Fatalf("sharedStringUpTo(%q, %q) ok = %v, want %v (got %q)", tt.a, tt.b, ok, tt.wantOK, got)
			}
			if got != tt.want {
				t.Errorf("sharedStringUpTo(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// sharedStringUpTo returns a string both patterns match, searching all strings over
// alphabet up to maxLen runes, and reports whether it found one.
//
// # Why the bool, and why it is not ceremony
//
// The first version returned only a string and signalled "nothing found" with "". The
// search starts at the EMPTY PREFIX, so when both patterns match the empty string the
// witness IS "" — indistinguishable from failure. The caller then read a genuine
// false-disjoint as "sampler found nothing" and stayed green.
//
// That is this file's own subject pointed at this file: a falsifiability helper that
// cannot fire in one case is exactly the unexamined guard US3 exists to catch, and it
// sat here through the whole feature. Found in review, not by any test — which is the
// honest version of how it was found.
func sharedStringUpTo(reA, reB *regexp.Regexp, alphabet []rune, maxLen int) (string, bool) {
	var rec func(prefix []rune) (string, bool)
	rec = func(prefix []rune) (string, bool) {
		s := string(prefix)
		if reA.MatchString(s) && reB.MatchString(s) {
			return s, true
		}
		if len(prefix) == maxLen {
			return "", false
		}
		for _, r := range alphabet {
			if found, ok := rec(append(prefix, r)); ok {
				return found, true
			}
		}
		return "", false
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
