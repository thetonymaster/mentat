package steps

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Intersection is the decider's answer for one pattern pair: whether any string
// matches both patterns, and — when one does — a witness string that both match.
//
// # Why not `Verdict`
//
// `core.Verdict` is a comparator's pass/fail result and is already in this package's
// scope (steps.go, the After hook). "Verdict" is also the word the spec uses for
// scenario outcomes. A second `Verdict` here would compile and read as the same
// concept; it is not.
//
// # The two directions are not equally trustworthy
//
// A TRUE answer proves itself: Witness is re-verified against both compiled patterns
// before it is returned, so a caller can check the claim without trusting this code.
//
// A FALSE answer does not. It means "this code found no shared string", which is only
// as good as this code is correct — and the prototype behind it decided all 780
// built-in pairs correctly while getting `(?i)` wrong. Never describe a false answer
// as proof of disjointness. See specs/013-builtin-pattern-disjointness/research.md R3.
type Intersection struct {
	// Intersects reports whether some string matches both patterns.
	Intersects bool
	// Witness is a string both patterns match. Non-empty only when Intersects is
	// true, and re-verified before return. The empty string is itself a legal
	// witness when both patterns match it, so emptiness is not the check.
	Witness string
}

// Intersects decides whether any string matches both patterns.
//
// # Method
//
// A lazy product automaton. Each pattern compiles to a syntax.Prog, which is an NFA
// whose instructions carry their own rune ranges. A search state is a pair of NFA
// program-counter sets plus whether we are at the start of the text; transitions are
// taken over EQUIVALENCE CLASSES of runes drawn from the union of both programs' rune
// ranges, which keeps the alphabet finite and small without enumerating Unicode. The
// search terminates because reachable state pairs are finite.
//
// It is tractable because RE2 has no backreferences or lookaround, so the language
// class is regular and emptiness-of-intersection is decidable. Measured over the 40
// built-in patterns: 780 pairs, max 74 product states per pair, well under a second.
//
// # It refuses rather than guesses
//
// Any construct this automaton cannot model produces an error naming the pattern and
// the construct. Returning "disjoint" for a pattern the decider misreads would be a
// silent fallback inside the very gate built to remove one (Constitution IV) — and
// worse than the gap it replaced, because the answer would carry the word "decided".
func Intersects(a, b string) (Intersection, error) {
	return intersectsWithBudget(a, b, maxProductStates)
}

// intersectsWithBudget is Intersects with the product-state bound supplied, so the
// budget's own behaviour can be tested without constructing an adversarial pattern pair
// that actually costs 100k states to decide.
//
// Testing the bound by exhausting it for real would make the test's runtime and memory
// the thing under test, and a machine-dependent one at that. Passing a small budget
// exercises the same code path deterministically, and a companion assertion pins that the
// SHIPPED constant still decides the pairs the gate depends on.
func intersectsWithBudget(a, b string, budget int) (Intersection, error) {
	na, err := compileNFA(a)
	if err != nil {
		return Intersection{}, err
	}
	nb, err := compileNFA(b)
	if err != nil {
		return Intersection{}, err
	}

	witness, found, err := searchProduct(na, nb, budget)
	if err != nil {
		return Intersection{}, err
	}
	if !found {
		return Intersection{}, nil
	}

	// FR-011: a positive verdict proves itself. Re-check the witness against both
	// patterns rather than asking the caller to trust the search that produced it.
	reA, errA := regexp.Compile(a)
	reB, errB := regexp.Compile(b)
	if errA != nil || errB != nil {
		return Intersection{}, fmt.Errorf("re-verifying witness for %q and %q: %w", a, b, errors.Join(errA, errB))
	}
	if !reA.MatchString(witness) || !reB.MatchString(witness) {
		// Unreachable unless the search is wrong, which is precisely when a loud
		// failure beats a confident answer.
		return Intersection{}, fmt.Errorf(
			"intersection decider produced witness %q for %q and %q, but it does not match both; this is a defect in the decider, not a disjointness result",
			witness, a, b)
	}
	return Intersection{Intersects: true, Witness: witness}, nil
}

// nfa is one compiled pattern, kept with its source text so every error can name the
// pattern the caller actually wrote.
type nfa struct {
	pat  string
	prog *syntax.Prog
}

func compileNFA(pat string) (*nfa, error) {
	re, err := syntax.Parse(pat, syntax.Perl)
	if err != nil {
		return nil, fmt.Errorf("parsing pattern %q: %w", pat, err)
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return nil, fmt.Errorf("compiling pattern %q: %w", pat, err)
	}
	if err := checkWholeTextAnchored(pat, re.Simplify()); err != nil {
		return nil, err
	}
	n := &nfa{pat: pat, prog: prog}
	if err := n.checkModelled(); err != nil {
		return nil, err
	}
	return n, nil
}

// checkWholeTextAnchored refuses any pattern whose regexp meaning is not whole-text.
//
// # Why this is a refusal and not a detail
//
// This decider reasons over the LANGUAGE OF THE COMPILED PROGRAM: the set of strings the
// NFA accepts end to end. `regexp.MatchString` asks a different question for an
// unanchored pattern — whether the pattern matches ANYWHERE in the string. The two
// coincide only when the pattern is anchored at both ends.
//
// Without this check the decider answers "disjoint" for pairs that genuinely share a
// string, silently:
//
//	Intersects("a", "ba")    -> disjoint, but regexp says "ba" matches both
//	Intersects("^a", "^ab$") -> disjoint, but "ab" matches both
//
// That is precisely the silent fallback this file argues against for empty-width
// assertions, in a case nobody had tested. A caller feeding unanchored patterns is
// asking a question this code cannot answer, so it says so.
//
// # Confirmed structurally, not textually
//
// A prefix "^" and a suffix "$" are not sufficient: `^a|b$` has both and means "starts
// with a, OR ends with b", neither branch whole-text. So the check is on the simplified
// syntax tree — the top level must be a concatenation beginning with OpBeginText and
// ending with OpEndText — and anything it cannot confirm is refused rather than assumed.
// Conservative by design: a false refusal is a loud, fixable error; a false "disjoint"
// is a gate reporting success it did not earn.
func checkWholeTextAnchored(pat string, re *syntax.Regexp) error {
	if wholeTextAnchored(re) {
		return nil
	}
	// Says what was not CONFIRMED, not what the pattern means. The first version of
	// this message asserted "it is not anchored at both ends, so its regexp meaning is
	// substring matching" — both clauses false for `^a$|^b$`, which is anchored on every
	// branch. An author would have been told to anchor a pattern that already was.
	return fmt.Errorf("cannot decide pattern %q: this decider reasons over whole-text "+
		"languages and could not confirm the pattern is anchored at both ends on every "+
		"alternative; for an unanchored pattern regexp matches anywhere in the string, so "+
		"the two disagree and no verdict would be sound", pat)
}

// wholeTextAnchored reports whether every way of matching re spans the entire text.
//
// Recurses through alternation because per-branch anchoring is legal and decidable:
// `^a$|^b$` means exactly "the text is a, or the text is b". V2's anchoredShape
// (phrase.go) already accepts that shape explicitly, so refusing it here would reject a
// phrase that passes validation — and, worse, exclude it from overlap checking
// altogether while reporting a diagnosis that does not apply to it.
//
// A bare prefix/suffix test is not enough: `^a|b$` has both anchors and means "starts
// with a, OR ends with b". Hence the walk, and hence the default of false — a shape this
// function does not recognise is refused rather than assumed.
func wholeTextAnchored(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpAlternate:
		if len(re.Sub) == 0 {
			return false
		}
		for _, sub := range re.Sub {
			if !wholeTextAnchored(sub) {
				return false
			}
		}
		return true
	case syntax.OpCapture:
		return len(re.Sub) == 1 && wholeTextAnchored(re.Sub[0])
	case syntax.OpConcat:
		subs := re.Sub
		return len(subs) >= 2 &&
			subs[0].Op == syntax.OpBeginText &&
			subs[len(subs)-1].Op == syntax.OpEndText
	default:
		return false
	}
}

// checkModelled refuses any pattern containing a construct the product automaton does
// not model, scanning EVERY instruction rather than waiting to meet one during the
// search.
//
// Eager on purpose. A `\b` sitting in a branch the search never reaches would otherwise
// be decided silently, and the verdict would be sound only by accident — the accident
// being that this particular pair happened not to explore that branch.
func (n *nfa) checkModelled() error {
	for i := range n.prog.Inst {
		switch n.prog.Inst[i].Op {
		case syntax.InstEmptyWidth:
			if err := emptyOpSupported(syntax.EmptyOp(n.prog.Inst[i].Arg)); err != nil {
				return fmt.Errorf("cannot decide pattern %q: %w", n.pat, err)
			}
		case syntax.InstAlt, syntax.InstAltMatch, syntax.InstCapture, syntax.InstNop,
			syntax.InstFail, syntax.InstMatch, syntax.InstRune, syntax.InstRune1,
			syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
			// Modelled by closure/instRanges.
		default:
			// An instruction kind this decider does not know. Refusing it HERE is what
			// lets closure avoid choosing a failure direction for it at all.
			//
			// Both of closure's fallbacks are wrong in one direction or the other, and
			// the first attempt at hardening them picked the worse one. Dropping a
			// transition UNDER-approximates, which produces a false "disjoint" — silent,
			// and the direction this whole feature exists to eliminate. Forwarding one
			// OVER-approximates, which produces a false "intersect" — caught loudly by
			// witness re-verification. An earlier comment in closure claimed the reverse,
			// while this file's own mutation record had it right.
			//
			// Refusing eagerly makes both arms unreachable by construction, so no
			// direction has to be chosen and no comment has to be trusted.
			return fmt.Errorf("cannot decide pattern %q: unrecognised program instruction %v "+
				"is refused rather than guessed at", n.pat, n.prog.Inst[i].Op)
		}
	}
	return nil
}

// emptyOpSupported reports whether an empty-width assertion is one this automaton
// models. Only the two whole-text anchors are.
//
// The final clause refuses any bit outside the set regexp/syntax defines today. Go
// currently defines six and all six are classified here, so no pattern string can reach
// it — which is exactly why it must exist: it is what refuses the seventh bit a future
// Go adds, instead of letting it join the set this code believes it models. That is the
// discipline 012 paid for three times with step arguments, where an unrecognised kind
// was silently treated as "none".
func emptyOpSupported(op syntax.EmptyOp) error {
	if op&(syntax.EmptyWordBoundary|syntax.EmptyNoWordBoundary) != 0 {
		return errors.New("word boundary assertions are not modelled; the product automaton tracks position, not the class of the previous rune")
	}
	if op&(syntax.EmptyBeginLine|syntax.EmptyEndLine) != 0 {
		return errors.New("multi-line anchors are not modelled; only whole-text ^ and $ are")
	}
	if rest := op &^ (syntax.EmptyBeginText | syntax.EmptyEndText); rest != 0 {
		return fmt.Errorf("unrecognised empty-width assertion %#x is refused rather than assumed satisfiable", uint32(rest))
	}
	return nil
}

// closure expands core program counters across epsilon edges, gating empty-width
// assertions on position, and returns the rune-consuming instructions and InstMatch it
// can reach.
//
// atEnd is a parameter rather than derived because the same state must be asked two
// different questions: "what can we consume next" (atEnd false) and "can we accept
// here" (atEnd true). Collapsing them into one closure makes every $-anchored pattern
// look unsatisfiable.
func (n *nfa) closure(core []uint32, atStart, atEnd bool) map[uint32]bool {
	seen := make(map[uint32]bool, len(core)*2)
	out := make(map[uint32]bool, len(core))
	stack := make([]uint32, 0, len(core)*2)
	push := func(pc uint32) {
		if !seen[pc] {
			seen[pc] = true
			stack = append(stack, pc)
		}
	}
	for _, pc := range core {
		push(pc)
	}
	for len(stack) > 0 {
		pc := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		inst := &n.prog.Inst[pc]
		switch inst.Op {
		case syntax.InstAlt, syntax.InstAltMatch:
			push(inst.Out)
			push(inst.Arg)
		case syntax.InstCapture, syntax.InstNop:
			push(inst.Out)
		case syntax.InstFail:
			// dead end
		case syntax.InstEmptyWidth:
			op := syntax.EmptyOp(inst.Arg)
			// Unreachable: checkModelled refuses anything but the text anchors before
			// any search begins. Kept as a belt-and-braces guard, not as the place the
			// decision is made — see checkModelled's default arm for why choosing a
			// direction here is the wrong place to do it.
			if op&^(syntax.EmptyBeginText|syntax.EmptyEndText) != 0 {
				continue
			}
			if op&syntax.EmptyBeginText != 0 && !atStart {
				continue
			}
			if op&syntax.EmptyEndText != 0 && !atEnd {
				continue
			}
			push(inst.Out)
		case syntax.InstMatch, syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
			out[pc] = true
		default:
			// Unreachable: checkModelled enumerates every instruction kind this decider
			// models and refuses the rest, so no unknown op reaches the search. That is
			// deliberate — either fallback here would be wrong in one direction, and the
			// refusal removes the choice.
			continue
		}
	}
	return out
}

// accepts reports whether this NFA can match having consumed exactly the string that
// led to core.
func (n *nfa) accepts(core []uint32, atStart bool) bool {
	for pc := range n.closure(core, atStart, true) {
		if n.prog.Inst[pc].Op == syntax.InstMatch {
			return true
		}
	}
	return false
}

// instRanges returns the rune intervals a consuming instruction accepts.
func instRanges(inst *syntax.Inst) [][2]rune {
	switch inst.Op {
	case syntax.InstRune1:
		return foldRanges(inst)
	case syntax.InstRuneAny:
		return [][2]rune{{0, utf8.MaxRune}}
	case syntax.InstRuneAnyNotNL:
		return [][2]rune{{0, '\n' - 1}, {'\n' + 1, utf8.MaxRune}}
	case syntax.InstRune:
		if len(inst.Rune) == 1 {
			return foldRanges(inst)
		}
		out := make([][2]rune, 0, len(inst.Rune)/2)
		for i := 0; i+1 < len(inst.Rune); i += 2 {
			out = append(out, [2]rune{inst.Rune[i], inst.Rune[i+1]})
		}
		return out
	}
	return nil
}

// foldRanges expands a single-rune instruction, honouring FoldCase.
//
// This is the one place a compiled program does not materialize its own alphabet, and
// missing it is research.md R3 — the defect the prototype shipped with. syntax compiles
// `(?i)[a-c]` into explicit ranges ['A' 'C' 'a' 'c'] with Arg=0, but compiles `(?i)abc`
// into single-rune instructions carrying FoldCase in Arg. A reader of Inst.Rune alone
// sees only 'A' and concludes the program cannot match 'a', so `^(?i)abc$` and `^abc$`
// get reported as disjoint when both match "abc".
//
// Mirrors Inst.MatchRunePos, which is the authority on what the program accepts.
func foldRanges(inst *syntax.Inst) [][2]rune {
	r0 := inst.Rune[0]
	out := [][2]rune{{r0, r0}}
	if syntax.Flags(inst.Arg)&syntax.FoldCase == 0 {
		return out
	}
	for r1 := unicode.SimpleFold(r0); r1 != r0; r1 = unicode.SimpleFold(r1) {
		out = append(out, [2]rune{r1, r1})
	}
	return out
}

// alphabet partitions the rune space into classes on which BOTH programs are constant,
// returning one representative per class.
//
// This is what makes the product automaton finite: within a class every rune drives
// both NFAs identically, so one representative decides for all of them. Dropping a
// class is a missed transition and therefore a false "disjoint" — one of the mutations
// US3 rehearses.
func alphabet(a, b *nfa) []rune {
	cuts := map[rune]bool{0: true}
	for _, n := range []*nfa{a, b} {
		for i := range n.prog.Inst {
			for _, rg := range instRanges(&n.prog.Inst[i]) {
				cuts[rg[0]] = true
				if rg[1] < utf8.MaxRune {
					cuts[rg[1]+1] = true
				}
			}
		}
	}
	reps := make([]rune, 0, len(cuts))
	for r := range cuts {
		reps = append(reps, r)
	}
	sort.Slice(reps, func(i, j int) bool { return reps[i] < reps[j] })
	return reps
}

func instMatchesRune(inst *syntax.Inst, r rune) bool {
	for _, rg := range instRanges(inst) {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// productKey identifies a search state: both program-counter sets plus whether we are
// at the start of the text. atStart belongs in the key because EmptyBeginText is
// satisfiable only there, so the same pc sets can behave differently.
func productKey(a, b []uint32, atStart bool) string {
	var sb strings.Builder
	if atStart {
		sb.WriteByte('^')
	}
	for _, pc := range a {
		fmt.Fprintf(&sb, "%d,", pc)
	}
	sb.WriteByte('|')
	for _, pc := range b {
		fmt.Fprintf(&sb, "%d,", pc)
	}
	return sb.String()
}

func sortedPCs(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for pc := range m {
		out = append(out, pc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// searchProduct breadth-first searches the product automaton, returning the shortest
// witness it finds.
//
// Breadth-first rather than depth-first so the witness is the shortest shared string,
// which is the one most useful in an error message.
// errSearchBudget reports that the product search hit its state budget before deciding.
//
// It is a REFUSAL, in the same family as an unmodelled construct, and callers must treat
// it that way: "I could not decide this pair" is never "these patterns are disjoint".
// A budget that answered "disjoint" under pressure would be a gate that gets quieter the
// harder the input is, which is the failure this whole feature exists to remove.
var errSearchBudget = errors.New("intersection decider: product search exceeded its state budget")

// maxProductStates bounds the product automaton's reachable-state set.
//
// # Why a bound exists at all
//
// The search is worst-case exponential in the two programs' state counts, and since
// FR-014 it runs over CONSUMER-SUPPLIED patterns inside mentat.Validate. Before this it
// had no state cap, no memory cap, no deadline and no cancellation — so a sufficiently
// complex but perfectly legal contributed phrase could burn CPU or memory until the
// process died, and `Validate` would never return.
//
// That gap was known and written down in contracts/decider.md as an unbounded-cost path,
// on the grounds that no blowup could be produced by hand (best adversarial attempt:
// 532µs). Review pointed out the obvious: "nobody managed to trigger it" is a
// measurement, not a bound — which is the exact distinction between evidence and proof
// that 013 was raised to fix. Applying the feature's own standard to the feature's own
// cost is how this ended up bounded.
//
// # Why this number
//
// Measured over the built-in set: 780 pairs, 2589 product states in total, max 74 for any
// single pair. This is ~1350× the largest pair observed, so it cannot refuse work the
// gate legitimately does, while still turning an exponential blowup into a prompt,
// descriptive refusal. It is deliberately a CONSTANT and not a tunable: a knob would need
// a second caller wanting a different value, and there is none.
const maxProductStates = 100_000

func searchProduct(a, b *nfa, budget int) (witness string, found bool, err error) {
	reps := alphabet(a, b)

	type node struct {
		a, b    []uint32
		atStart bool
		witness string
	}
	start := node{
		a:       []uint32{uint32(a.prog.Start)},
		b:       []uint32{uint32(b.prog.Start)},
		atStart: true,
	}
	seen := map[string]bool{productKey(start.a, start.b, true): true}
	queue := []node{start}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if a.accepts(cur.a, cur.atStart) && b.accepts(cur.b, cur.atStart) {
			return cur.witness, true, nil
		}

		clA := a.closure(cur.a, cur.atStart, false)
		clB := b.closure(cur.b, cur.atStart, false)

		for _, r := range reps {
			nextA := stepOn(a, clA, r)
			if len(nextA) == 0 {
				continue
			}
			nextB := stepOn(b, clB, r)
			if len(nextB) == 0 {
				continue
			}
			ka, kb := sortedPCs(nextA), sortedPCs(nextB)
			key := productKey(ka, kb, false)
			if seen[key] {
				continue
			}
			// Checked at the point of GROWTH, so the bound covers both the visited set
			// and the queue — the two things that actually consume memory here, since
			// every queued node also retains its witness prefix.
			if len(seen) >= budget {
				return "", false, fmt.Errorf("%w (%d states) deciding %q against %q",
					errSearchBudget, budget, a.pat, b.pat)
			}
			seen[key] = true
			queue = append(queue, node{a: ka, b: kb, witness: cur.witness + string(r)})
		}
	}
	return "", false, nil
}

// stepOn returns the program counters reached by consuming r from the given closure.
func stepOn(n *nfa, cl map[uint32]bool, r rune) map[uint32]bool {
	next := make(map[uint32]bool, len(cl))
	for pc := range cl {
		inst := &n.prog.Inst[pc]
		if inst.Op == syntax.InstMatch {
			continue
		}
		if instMatchesRune(inst, r) {
			next[inst.Out] = true
		}
	}
	return next
}

// PatternSource says where a step pattern came from. It exists so a pattern-overlap
// finding can name the contributing comparator, which is the only thing that makes such
// a finding actionable for a consumer.
type PatternSource int

const (
	// SourceBuiltin is a stepDefs row.
	SourceBuiltin PatternSource = iota
	// SourceContributed is a phrase a comparator declared.
	SourceContributed
)

// LabelledPattern is one step pattern with its provenance.
//
// StepPatterns is []*regexp.Regexp and has discarded both the pattern STRING and where
// it came from by the time a check sees it. A finding needs both.
type LabelledPattern struct {
	// Pattern is the regex source, as registered.
	Pattern string
	// Source distinguishes a stepDefs row from a contributed phrase.
	Source PatternSource
	// Comparator names the contributing comparator; empty for built-ins.
	Comparator string
}

// labelledPatternsFor returns the pattern set in REGISTRATION ORDER — built-ins first,
// in stepDefs table order, then contributed phrases in resolution order.
//
// Order is significant, not incidental: registration order is what decides which
// definition godog binds when two match, so a report listing patterns out of order
// would misdescribe the runtime.
func labelledPatternsFor(phrases []contributedPhrase) []LabelledPattern {
	docs := StepDocs()
	out := make([]LabelledPattern, 0, len(docs)+len(phrases))
	for _, d := range docs {
		out = append(out, LabelledPattern{Pattern: d.Pattern, Source: SourceBuiltin})
	}
	for _, cp := range phrases {
		out = append(out, LabelledPattern{
			Pattern:    cp.phrase.Pattern,
			Source:     SourceContributed,
			Comparator: cp.comparator,
		})
	}
	return out
}

// patternOverlapFindings decides every pair involving at least one CONTRIBUTED pattern
// and reports each overlap as a `pattern-overlap` finding carrying its witness.
//
// # Why contributed pairs only
//
// Built-in × built-in is the CI gate's job (TestBuiltinStepPatternsAreDecidedDisjoint).
// Deciding it again here would cost every Validate call 780 decisions to re-derive a
// property CI already proves, and — worse — would report to a consumer a defect they
// cannot fix.
//
// # Why this is not fatal
//
// Overlap between two independently-authored comparators is a POTENTIAL failure: it
// becomes real only for a sentence inside the overlap, which still fails loudly at run
// time through godog's Strict plus the per-sentence `ambiguous-step` finding. Refusing
// to build would make two otherwise-usable comparators mutually exclusive for a consumer
// whose feature files never enter the overlap. We own stepDefs and can simply not ship
// an overlapping pair; we do not own consumers' comparators. See 013 D5.
//
// # Why it lives here and not beside the fail-fast precheck
//
// This is called from EngineStepChecks, which scenario init does not call — it calls
// resolvePhrases directly. So these findings cannot reach the run path. That call-graph
// fact, not this function's unexported name, is what makes FR-017 structural: an
// unexported function is equally callable from elsewhere in this package.
//
// A refusal from the decider is returned as an error, never swallowed: a pattern the
// decider cannot model is a fact the author needs, and treating it as "no overlap" would
// be the silent fallback the decider exists to remove.
func patternOverlapFindings(labelled []LabelledPattern, src Source) ([]Finding, error) {
	return patternOverlapFindingsWithBudget(labelled, src, maxProductStates)
}

// patternOverlapFindingsWithBudget is patternOverlapFindings with the decider's
// product-state bound supplied, so the budget-refusal path can be driven in a test
// without an adversarial pattern pair. Production has exactly one caller, above.
func patternOverlapFindingsWithBudget(labelled []LabelledPattern, src Source, budget int) ([]Finding, error) {
	var out []Finding

	// An UNDECIDABLE pattern is reported once, by itself, and then excluded from
	// pairing.
	//
	// # Why a finding and not an error
	//
	// A contributed phrase may legally contain `\b`, `\B` or a `(?m)` anchor: V2
	// (isAnchored) admits all three, and godog runs such a step correctly. Before this,
	// a refusal propagated out of EngineStepChecks and mentat.Validate returned
	// `nil, err` — so the VALIDATOR REFUSED A SUITE THE RUNNER EXECUTES. That is the
	// mirror image of the drift D7 was created to remove ("a validator certifying a
	// suite the runner rejects"), and it is equally a drift.
	//
	// D5's ownership rule settles which way to resolve it: the pattern is the
	// consumer's, so we report it and keep working, rather than making their engine
	// unusable over a construct we chose not to model. Note the finding is still
	// LOUD — the author is told their phrase is excluded from overlap checking, which
	// is a real gap in their coverage and not a shrug.
	//
	// # Once per pattern, not once per pair
	//
	// Pairing an undecidable pattern with 40 built-ins would emit 40 identical
	// complaints about one defect, which is how a findings list becomes unreadable.
	decidable := make([]LabelledPattern, 0, len(labelled))
	for _, lp := range labelled {
		if _, err := compileNFA(lp.Pattern); err != nil {
			out = append(out, Finding{
				File:  src.File,
				Class: "pattern-undecidable",
				Message: fmt.Sprintf(
					"%s cannot be checked for overlap against other step patterns: %v; the step itself still runs, but a collision between it and another pattern would not be reported here",
					describeLabelled(lp), err),
			})
			continue
		}
		decidable = append(decidable, lp)
	}
	labelled = decidable

	for i := 0; i < len(labelled); i++ {
		for j := i + 1; j < len(labelled); j++ {
			if labelled[i].Source == SourceBuiltin && labelled[j].Source == SourceBuiltin {
				continue
			}
			got, err := intersectsWithBudget(labelled[i].Pattern, labelled[j].Pattern, budget)
			switch {
			case errors.Is(err, errSearchBudget):
				// A budget refusal is undecidability of the PAIR, so it cannot be caught
				// by the per-pattern pre-scan above and it must NOT take the error branch
				// below: propagating it would make mentat.Validate return `nil, err` for
				// two individually-legal phrases, which is precisely the validator-refuses-
				// a-suite-the-runner-executes drift FR-018 exists to prevent. Same class,
				// same treatment, different granularity — reported against the pair,
				// because neither pattern alone is the problem.
				out = append(out, Finding{
					File:  src.File,
					Class: "pattern-undecidable",
					Message: fmt.Sprintf(
						"%s and %s could not be checked for overlap against each other: %v; both steps still run, but a collision between them would not be reported here",
						describeLabelled(labelled[i]), describeLabelled(labelled[j]), err),
				})
				continue
			case err != nil:
				// Unreachable: both patterns compiled above and the budget case is
				// handled. Loud rather than ignored, because reaching it means the
				// pre-scan and the decider disagree.
				return nil, fmt.Errorf("deciding overlap between %s and %s: %w",
					describeLabelled(labelled[i]), describeLabelled(labelled[j]), err)
			}
			if !got.Intersects {
				continue
			}
			out = append(out, Finding{
				File:  src.File,
				Class: "pattern-overlap",
				Message: fmt.Sprintf(
					"%s and %s can both match the same step, for example %q; under Strict neither definition binds, so any feature file containing such a step fails as ambiguous",
					describeLabelled(labelled[i]), describeLabelled(labelled[j]), got.Witness),
			})
		}
	}
	return out, nil
}

// describeLabelled names a pattern in the author's terms: a contributed phrase is
// identified by its comparator, because that is what the author can change.
func describeLabelled(lp LabelledPattern) string {
	if lp.Source == SourceContributed {
		return fmt.Sprintf("the phrase %q contributed by comparator %q", lp.Pattern, lp.Comparator)
	}
	return fmt.Sprintf("the built-in step %q", lp.Pattern)
}
