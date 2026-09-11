package steps

// Shared pattern fixtures for feature 013.
//
// This file is deliberately NEUTRAL — it belongs to neither US1 (the count-based
// deferral in stepargs.go) nor US2 (the intersection decider in disjoint.go), because
// both need the same colliding pair and putting it in either story's test file would
// couple two stories that are otherwise independent.

// overlappingPair is the exact-vs-general collision from the spec's Edge Cases: a
// future built-in pinning one literal value against the existing general pattern.
// Both are anchored, both are well-formed, and `the result contains "revenue"` matches
// both — which is what makes it a collision rather than a near miss.
//
// It is written as a pair of literal pattern strings, not as stepDefs rows, so a test
// can pass it to a gate as a local slice. Mutating the package-level stepDefs table
// instead would be a data race: `make test` runs `go test ./... -race` and the
// disjointness tests call t.Parallel().
var overlappingPair = [2]string{
	`^the result contains "([^"]*)"$`,
	`^the result contains "revenue"$`,
}

// overlappingWitness is the string both halves of overlappingPair match. Tests assert
// the decider reports exactly this, and verify it against both patterns rather than
// trusting the constant.
const overlappingWitness = `the result contains "revenue"`

// disjointPair is two real built-in patterns that differ by one word. It is the
// negative control: a gate that reddens on this is over-reporting.
var disjointPair = [2]string{
	`^the tool "([^"]+)" is never called$`,
	`^the service "([^"]+)" is never called$`,
}
