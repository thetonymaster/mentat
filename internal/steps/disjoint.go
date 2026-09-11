package steps

import "fmt"

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
// It returns an error rather than a verdict for any pattern it cannot model, because
// answering "disjoint" for a construct this code misreads is the silent fallback the
// decider exists to remove (Constitution IV).
//
// STUB: the real product automaton lands in US2 (T024-T030). It returns an error so
// that US2's tests fail on their assertions rather than on a package-wide compile
// error — `internal/steps` is one package, and a missing symbol here would stop
// US1's tests from running at all.
func Intersects(a, b string) (Intersection, error) {
	return Intersection{}, fmt.Errorf("intersection decider: not implemented: %q vs %q", a, b)
}
