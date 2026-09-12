package steps

import (
	"fmt"
	"os"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stepSpy is a stepRegistrar stand-in that records every (pattern, handler) pair
// the registration path emits. It observes the LIVE registration — the exact same
// registerSteps call the InitializerWithCollector closure delegates to — so the
// drift test compares what is really registered against the metadata table, not a
// re-read of the table itself.
type stepSpy struct {
	patterns []string
	nilFuncs []string
}

func (s *stepSpy) Step(expr, stepFunc any) {
	p, ok := expr.(string)
	if !ok {
		// The metadata table registers string patterns; anything else is a defect.
		s.patterns = append(s.patterns, "<non-string pattern>")
		return
	}
	s.patterns = append(s.patterns, p)
	if stepFunc == nil {
		s.nilFuncs = append(s.nilFuncs, p)
	}
}

// registeredPatterns drives the real registerSteps path with a spy and returns the
// patterns it emitted, plus any pattern registered with a nil handler.
func registeredPatterns(t *testing.T, phrases ...phraseStep) (patterns []string, nilFuncs []string) {
	t.Helper()
	// A zero world is sufficient: registerSteps only binds method values
	// (w.method), it never invokes them, so no engine/ctx is needed.
	//
	// That zero world is also WHY registerSteps takes its contributed phrases as a
	// parameter. w.eng is nil here, so reaching through it for phrases would panic,
	// and nil-guarding it would be exactly the silent fallback No Silent Fallbacks
	// forbids: the drift test would then be asserting over an empty set while
	// believing it covered the real one.
	spy := &stepSpy{}
	registerSteps(spy, &world{}, phrases)
	return spy.patterns, spy.nilFuncs
}

func tablePatterns() []string {
	out := make([]string, 0, len(stepDefs))
	for _, sd := range stepDefs {
		out = append(out, sd.pattern)
	}
	return out
}

// TestStepMetadataMatchesRegistration is the drift guard (US1, E1): every pattern
// the registration path emits must have a metadata entry and vice-versa, and the
// two counts must match. It asserts against the LIVE registration (via the spy),
// not a hardcoded 37, so the invariant holds as steps are added or removed.
func TestStepMetadataMatchesRegistration(t *testing.T) {
	t.Parallel()

	registered, nilFuncs := registeredPatterns(t)
	table := tablePatterns()

	if len(nilFuncs) > 0 {
		t.Errorf("patterns registered with a nil handler: %v", nilFuncs)
	}

	regSet := toSet(registered)
	tabSet := toSet(table)

	// Duplicate registration would make godog ambiguous and hide a drift.
	if len(regSet) != len(registered) {
		t.Errorf("duplicate pattern registrations: %d calls, %d distinct", len(registered), len(regSet))
	}
	if len(tabSet) != len(table) {
		t.Errorf("duplicate metadata entries: %d entries, %d distinct patterns", len(table), len(tabSet))
	}

	for _, p := range sortedKeys(regSet) {
		if _, ok := tabSet[p]; !ok {
			t.Errorf("registered pattern has no metadata entry: %q", p)
		}
	}
	for _, p := range sortedKeys(tabSet) {
		if _, ok := regSet[p]; !ok {
			t.Errorf("metadata entry is never registered: %q", p)
		}
	}

	if len(registered) != len(stepDefs) {
		t.Errorf("count mismatch: %d steps registered, %d metadata entries", len(registered), len(stepDefs))
	}
}

// TestStepRegistrationIsAPartition is the drift gate generalised for 012.
//
// The gate is NOT relaxed into "registration may be a superset of the table". That
// would surrender exactly what stepDefs is for. It becomes a PARTITION:
//
//	every registered pattern is EITHER a stepDefs row OR a member of the contributed
//	set supplied for this engine — and the counts add up.
//
// An unaccounted registered pattern is still a hard failure. The gate loses no
// strength; it gains a second ACCOUNTED source.
//
// Both halves are asserted, because each fails differently: a built-in going missing
// is a regression in the table, while a contributed phrase going missing means the
// engine's own comparators were silently ignored.
//
// Mutation rehearsals (2026-09-11), naming what was mutated:
//
//	A. Deleted the contributed-phrase registration loop from registerSteps
//	   (`_ = phrases`). RED: "0 contributed patterns registered, want 2" and the
//	   count mismatch. This is the mutation that matters — a partition test that
//	   could not detect the contributed half going missing would be strictly weaker
//	   than the bidirectional equality it replaced.
//	B. Swapped the two loops so phrases register first. RED in
//	   TestBuiltinsRegisterBeforeContributedPhrases: "a contributed phrase registered
//	   at index 0, before the last built-in at 40".
//
// Both reverted; tests re-observed green.
func TestStepRegistrationIsAPartition(t *testing.T) {
	t.Parallel()

	contributed := []phraseStep{
		{pattern: `^the revenue is shaped like a (\w+) report$`, handler: func(string) error { return nil }},
		{pattern: `^the ledger balances$`, handler: func() error { return nil }},
	}

	registered, nilFuncs := registeredPatterns(t, contributed...)
	if len(nilFuncs) > 0 {
		t.Errorf("patterns registered with a nil handler: %v", nilFuncs)
	}

	builtin := toSet(tablePatterns())
	contrib := make(map[string]struct{}, len(contributed))
	for _, p := range contributed {
		contrib[p.pattern] = struct{}{}
	}

	var fromBuiltin, fromContrib int
	for _, p := range registered {
		_, isBuiltin := builtin[p]
		_, isContrib := contrib[p]
		switch {
		case isBuiltin && isContrib:
			t.Errorf("pattern %q is BOTH a stepDefs row and a contributed phrase; the two sources must be disjoint or the partition is meaningless", p)
		case isBuiltin:
			fromBuiltin++
		case isContrib:
			fromContrib++
		default:
			t.Errorf("registered pattern belongs to neither source: %q", p)
		}
	}

	if fromBuiltin != len(stepDefs) {
		t.Errorf("%d built-in patterns registered, want %d", fromBuiltin, len(stepDefs))
	}
	if fromContrib != len(contributed) {
		t.Errorf("%d contributed patterns registered, want %d", fromContrib, len(contributed))
	}
	if len(registered) != len(stepDefs)+len(contributed) {
		t.Errorf("count mismatch: %d registered, want %d built-in + %d contributed", len(registered), len(stepDefs), len(contributed))
	}
}

// TestBuiltinsRegisterBeforeContributedPhrases pins the ordering rule collision
// resolution depends on. The runner returns the FIRST matching step definition, so a
// contributed phrase colliding with a built-in must never be able to displace it.
//
// Under Strict the collision surfaces as an ambiguity failure rather than the silent
// shadowing that would otherwise occur — but the ordering is what decides which way
// a non-strict runner would resolve it, so it is asserted directly rather than left
// to the flag.
func TestBuiltinsRegisterBeforeContributedPhrases(t *testing.T) {
	t.Parallel()

	contributed := []phraseStep{
		{pattern: `^the contributed phrase runs$`, handler: func() error { return nil }},
	}
	registered, _ := registeredPatterns(t, contributed...)

	builtin := toSet(tablePatterns())
	lastBuiltin, firstContrib := -1, -1
	for i, p := range registered {
		if _, ok := builtin[p]; ok {
			lastBuiltin = i
			continue
		}
		if firstContrib == -1 {
			firstContrib = i
		}
	}
	if firstContrib == -1 {
		t.Fatalf("no contributed pattern was registered at all: %v", registered)
	}
	if lastBuiltin > firstContrib {
		t.Errorf("a contributed phrase registered at index %d, before the last built-in at %d; built-ins must register first so a colliding phrase cannot displace one",
			firstContrib, lastBuiltin)
	}
}

// TestStepMetadataFieldsPresent proves no blank documentation slips through: every
// entry carries a non-empty pattern, summary, and example plus a non-nil handler.
func TestStepMetadataFieldsPresent(t *testing.T) {
	t.Parallel()

	if len(stepDefs) == 0 {
		t.Fatal("stepDefs is empty; the metadata table must document every step")
	}
	for _, sd := range stepDefs {
		if strings.TrimSpace(sd.pattern) == "" {
			t.Errorf("metadata entry has an empty pattern (summary=%q)", sd.summary)
			continue
		}
		if strings.TrimSpace(sd.group) == "" {
			t.Errorf("metadata entry %q has an empty group", sd.pattern)
		}
		if strings.TrimSpace(sd.summary) == "" {
			t.Errorf("metadata entry %q has an empty summary", sd.pattern)
		}
		if strings.TrimSpace(sd.example) == "" {
			t.Errorf("metadata entry %q has an empty example", sd.pattern)
		}
		if sd.handler == nil {
			t.Errorf("metadata entry %q has a nil handler", sd.pattern)
			continue
		}
		if sd.handler(&world{}) == nil {
			t.Errorf("metadata entry %q binds to a nil handler value", sd.pattern)
		}
	}
}

// TestNoDirectStepRegistration closes the drift hole the spy alone cannot: it fails
// if steps.go registers any step directly via sc.Step( instead of through the
// metadata table. All registration must flow through registerSteps so the table
// stays the single source of truth.
func TestNoDirectStepRegistration(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("steps.go")
	if err != nil {
		t.Fatalf("read steps.go: %v", err)
	}
	if strings.Contains(string(data), "sc.Step(") {
		t.Error("steps.go calls sc.Step( directly; register every step through the metadata table (registerSteps)")
	}
}

func toSet(xs []string) map[string]struct{} {
	m := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		m[x] = struct{}{}
	}
	return m
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestBuiltinStepPatternsArePairwiseDisjoint is the SENTENCE-CORPUS CROSS-CHECK of
// built-in disjointness. It is no longer the evidence for the property.
//
// # Read this together with TestBuiltinStepPatternsAreDecidedDisjoint
//
// The two names are one word apart and the difference matters:
//
//	…AreDecidedDisjoint    decides the property over each PATTERN PAIR. Load-bearing.
//	…ArePairwiseDisjoint   samples SENTENCES generated from the patterns. Cross-check.
//
// This one was the load-bearing gate until feature 013, and that is precisely what 013
// was raised to fix: it substitutes nine fixed fillers into capture groups and character
// classes, so two built-ins colliding only on a string no filler produces would pass it.
// "The 40 built-in patterns are pairwise disjoint" was therefore evidence, not proof.
//
// Keeping it is deliberate (013 D3), and it is a promotion rather than a demotion. Two
// mechanisms of different kinds now check one property, so a DISAGREEMENT between them
// proves one is broken — which is worth more than either alone. It is also cheap, runs
// on every build, and would catch an obvious collision immediately.
//
// Its `generated < 500` floor stays for the same reason: a generator that silently
// produced nothing would report success forever.
//
// # What it originally pinned
//
// V4 rejects a contributed pattern identical to a built-in's, on the grounds that
// built-ins register first and would permanently shadow it. That rule is only
// meaningful if the built-in set is itself unambiguous — and until this test, nothing
// asserted that. It was measured while investigating the ambiguity defect (012 R10)
// and turned out to be TRUE, which corrected a spec claim that the ambiguous branch was
// already reachable between two built-ins. After 013 that assumption is DECIDED rather
// than sampled, so V4's rationale no longer rests on this test.
//
// Sentences are generated from each pattern's own syntax tree, expanding every
// alternation branch, so the check does not depend on anyone hand-listing examples.
//
// The name is kept rather than changed, recorded as spec.md D6. Measured 2026-09-11:
// **18** references outside 013's own tasks.md — 5 in internal/steps, 2 in CLAUDE.md, 3
// in merged 012 artifacts, 8 across 013's. Two of them settle it: FR-005 names this test
// BY NAME, so renaming it makes a requirement stale, and the merged-012 citations sit in
// contracts T048 deliberately annotated rather than rewrote.
//
// This comment said "six references" until the count was actually taken, as did the
// tasks.md item directing the rename — so the rename was costed at a third of its reach
// by both. Dangling citations in shipped artifacts are a worse defect than two
// neighbouring names that the comment above now distinguishes.
func TestBuiltinStepPatternsArePairwiseDisjoint(t *testing.T) {
	t.Parallel()

	docs := StepDocs()
	pats := make([]*regexp.Regexp, len(docs))
	for i, d := range docs {
		pats[i] = regexp.MustCompile(d.Pattern)
	}

	// Fillers stand in for capture groups and character classes. They are varied
	// deliberately: a single filler could miss an overlap that only appears for, say,
	// a numeric capture.
	fillers := []string{"x", "", "a b", "1", "2nd", "true", "0.5", "tool-name", "a/b.c"}

	seen := map[string]bool{}
	generated := 0
	for _, d := range docs {
		re, err := syntax.Parse(d.Pattern, syntax.Perl)
		if err != nil {
			t.Fatalf("parse %q: %v", d.Pattern, err)
		}
		for _, f := range fillers {
			for _, sentence := range expandPattern(re.Simplify(), f, 0) {
				if seen[sentence] {
					continue
				}
				seen[sentence] = true
				generated++

				var matched []int
				for j, p := range pats {
					if p.MatchString(sentence) {
						matched = append(matched, j)
					}
				}
				if len(matched) > 1 {
					names := make([]string, 0, len(matched))
					for _, j := range matched {
						names = append(names, docs[j].Pattern)
					}
					t.Errorf("sentence %q matches %d built-in patterns, which makes the built-in grammar ambiguous:\n\t%s",
						sentence, len(matched), strings.Join(names, "\n\t"))
				}
			}
		}
	}

	// A generator that silently produced nothing would report success forever.
	if generated < 500 {
		t.Fatalf("only %d sentences generated from %d patterns; the generator has likely broken rather than the grammar having shrunk", generated, len(docs))
	}
}

// expandPattern generates candidate sentences from a parsed pattern, taking EVERY
// alternation branch and substituting filler for anything variable.
func expandPattern(re *syntax.Regexp, filler string, depth int) []string {
	if depth > 12 {
		return []string{""}
	}
	switch re.Op {
	case syntax.OpLiteral:
		return []string{string(re.Rune)}
	case syntax.OpConcat:
		out := []string{""}
		for _, sub := range re.Sub {
			subs := expandPattern(sub, filler, depth+1)
			next := make([]string, 0, len(out)*len(subs))
			for _, pre := range out {
				for _, s := range subs {
					next = append(next, pre+s)
				}
			}
			if len(next) > 4000 {
				next = next[:4000]
			}
			out = next
		}
		return out
	case syntax.OpAlternate:
		var out []string
		for _, sub := range re.Sub {
			out = append(out, expandPattern(sub, filler, depth+1)...)
		}
		return out
	case syntax.OpCapture:
		return expandPattern(re.Sub[0], filler, depth+1)
	case syntax.OpQuest:
		return append([]string{""}, expandPattern(re.Sub[0], filler, depth+1)...)
	case syntax.OpStar, syntax.OpPlus, syntax.OpRepeat,
		syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return []string{filler}
	default:
		// Anchors, empty matches and word boundaries contribute no characters.
		return []string{""}
	}
}

// decidedCollisions decides EVERY pair in patterns and returns one description per
// intersecting pair, naming both patterns and the verified witness.
//
// The pattern list is a PARAMETER, not read from stepDefs inside. Two reasons, and the
// second is the load-bearing one:
//
//  1. A gate that can only ever be pointed at the real table cannot be shown to fire.
//  2. `make test` runs `go test ./... -race` and the tests here call t.Parallel(), so
//     injecting a colliding row by mutating package-level stepDefs would be a data
//     race — a guard whose rehearsal corrupts the thing it guards.
func decidedCollisions(t *testing.T, patterns []string) []string {
	t.Helper()

	var out []string
	pairs := 0
	start := time.Now()
	for i := 0; i < len(patterns); i++ {
		for j := i + 1; j < len(patterns); j++ {
			pairs++
			got, err := Intersects(patterns[i], patterns[j])
			if err != nil {
				// A refusal is not a disjointness result and must never be counted as
				// one. It means the decider cannot model one of these patterns, which
				// is a finding in its own right.
				t.Fatalf("decider refused the pair %q / %q: %v", patterns[i], patterns[j], err)
			}
			if !got.Intersects {
				continue
			}
			reA, reB := regexp.MustCompile(patterns[i]), regexp.MustCompile(patterns[j])
			out = append(out, fmt.Sprintf("%q and %q both match %q (verified: %v/%v)",
				patterns[i], patterns[j], got.Witness,
				reA.MatchString(got.Witness), reB.MatchString(got.Witness)))
		}
	}
	// FR-016: the cost is REPORTED, not asserted. A ceiling here would turn a slow CI
	// runner into a false red about pattern disjointness, which is a different claim.
	t.Logf("decided %d pairs over %d patterns in %s", pairs, len(patterns), time.Since(start))
	return out
}

// TestBuiltinStepPatternsAreDecidedDisjoint is US2's gate: FR-013 and SC-007.
//
// This DECIDES the property. It consults no sentence corpus, so unlike its cross-check
// sibling it cannot miss a collision that only appears for a string no filler produces
// — which is the gap feature 013 exists to close.
//
// It is the load-bearing assertion; TestBuiltinStepPatternsArePairwiseDisjoint remains
// as an independent cross-check of a different kind (D3). Two mechanisms agreeing is
// worth more than either alone, because a disagreement proves one is broken.
func TestBuiltinStepPatternsAreDecidedDisjoint(t *testing.T) {
	t.Parallel()

	docs := StepDocs()
	patterns := make([]string, 0, len(docs))
	for _, d := range docs {
		patterns = append(patterns, d.Pattern)
	}

	if got := decidedCollisions(t, patterns); len(got) > 0 {
		t.Errorf("the built-in pattern set is NOT pairwise disjoint — %d colliding pair(s):\n  %s",
			len(got), strings.Join(got, "\n  "))
	}
}

// TestDecidedDisjointnessGateFiresOnACollision is US2's T021 and pins SC-003.
//
// The gate above passes today, so nothing in it demonstrates that it CAN fail. This
// rehearses it against a deliberately overlapping row, passed as a local slice.
//
// The point SC-003 makes: this fires with NO feature file present. A sentence-corpus
// check can only report a collision on a sentence the corpus contains; a decider is
// asked about the patterns themselves, so "nobody has written that step yet" is not a
// way to hide.
func TestDecidedDisjointnessGateFiresOnACollision(t *testing.T) {
	t.Parallel()

	// The real built-in, plus a plausible future row pinning one literal value.
	patterns := []string{overlappingPair[0], overlappingPair[1]}

	got := decidedCollisions(t, patterns)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 collision for %v, got %d: %v", patterns, len(got), got)
	}
	// strconv.Quote: the report renders patterns and the witness with %q, so each
	// comes back escaped. This is the fourth place in this feature where asserting the
	// raw form would have failed against a correct message; the repo's own
	// stepargs_test.go documents the same trap for step text.
	for _, want := range []string{overlappingPair[0], overlappingPair[1], overlappingWitness} {
		if !strings.Contains(got[0], strconv.Quote(want)) {
			t.Errorf("collision report does not name %q:\n  %s", want, got[0])
		}
	}
	if !strings.Contains(got[0], "true/true") {
		t.Errorf("the witness was not verified against both patterns:\n  %s", got[0])
	}
}
