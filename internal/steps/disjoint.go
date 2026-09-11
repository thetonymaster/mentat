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
	na, err := compileNFA(a)
	if err != nil {
		return Intersection{}, err
	}
	nb, err := compileNFA(b)
	if err != nil {
		return Intersection{}, err
	}

	witness, found, err := searchProduct(na, nb)
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
	n := &nfa{pat: pat, prog: prog}
	if err := n.checkModelled(); err != nil {
		return nil, err
	}
	return n, nil
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
		if n.prog.Inst[i].Op != syntax.InstEmptyWidth {
			continue
		}
		if err := emptyOpSupported(syntax.EmptyOp(n.prog.Inst[i].Arg)); err != nil {
			return fmt.Errorf("cannot decide pattern %q: %w", n.pat, err)
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
			// checkModelled has already rejected anything but the text anchors.
			op := syntax.EmptyOp(inst.Arg)
			if op&syntax.EmptyBeginText != 0 && !atStart {
				continue
			}
			if op&syntax.EmptyEndText != 0 && !atEnd {
				continue
			}
			push(inst.Out)
		default:
			out[pc] = true
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
func searchProduct(a, b *nfa) (witness string, found bool, err error) {
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
