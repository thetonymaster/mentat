package steps

import (
	"strings"
	"testing"

	"github.com/thetonymaster/mentat/internal/core"
)

// TestStepDocsMirrorsTable proves the exported StepDocs accessor is a faithful,
// lossless view of the stepDefs metadata table: same count, same field values,
// every row carrying a non-empty group. This is the seam `mentat steps` and
// docs/steps.md render from, so it must never drift from the single source.
func TestStepDocsMirrorsTable(t *testing.T) {
	t.Parallel()

	docs := StepDocs()
	if len(docs) != len(stepDefs) {
		t.Fatalf("StepDocs len = %d, want %d (one per metadata row)", len(docs), len(stepDefs))
	}
	for i, d := range docs {
		sd := stepDefs[i]
		if d.Pattern != sd.pattern {
			t.Errorf("row %d: Pattern = %q, want %q", i, d.Pattern, sd.pattern)
		}
		if d.Summary != sd.summary {
			t.Errorf("row %d: Summary = %q, want %q", i, d.Summary, sd.summary)
		}
		if d.Example != sd.example {
			t.Errorf("row %d: Example = %q, want %q", i, d.Example, sd.example)
		}
		if d.Group != sd.group {
			t.Errorf("row %d: Group = %q, want %q", i, d.Group, sd.group)
		}
		if strings.TrimSpace(d.Group) == "" {
			t.Errorf("row %d (%q): empty group", i, d.Pattern)
		}
	}
}

// TestStepDocsGroupsAreContiguous guards the invariant the markdown generator
// relies on: every distinct group forms ONE contiguous block, so the renderer can
// emit a single heading per group by watching for the group to change. An
// interleaved group would produce a duplicated heading in docs/steps.md.
func TestStepDocsGroupsAreContiguous(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	prev := ""
	for _, d := range StepDocs() {
		if d.Group == prev {
			continue
		}
		if seen[d.Group] {
			t.Errorf("group %q reappears after group %q; groups must be contiguous", d.Group, prev)
		}
		seen[d.Group] = true
		prev = d.Group
	}
}

// --- 012 US4: the engine-scoped reference ---

// TestEngineStepDocsContainsBuiltinsAndContributedPhrases is FR-012: a consumer can
// render the full reference for THEIR engine, not just Mentat's built-ins.
//
// Without it the only reference a consumer has lists steps their suite can use but
// omits the ones their own comparators added — the exact steps they are most likely
// to need documented, because nobody else wrote them down.
func TestEngineStepDocsContainsBuiltinsAndContributedPhrases(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t, withComparator("revenue-shape", &validationComparator{
		name: "revenue-shape",
		phrases: []core.ContributedPhrase{
			{Pattern: `^the revenue floor is (\d+)$`, Group: "Revenue", Summary: "Asserts the floor.", Example: "Then the revenue floor is 4"},
			{Pattern: `^the revenue ceiling is (\d+)$`, Group: "Revenue", Summary: "Asserts the ceiling.", Example: "Then the revenue ceiling is 9"},
		},
	}))

	docs, err := EngineStepDocs(eng)
	if err != nil {
		t.Fatalf("EngineStepDocs: %v", err)
	}

	builtins := StepDocs()
	if len(docs) != len(builtins)+2 {
		t.Fatalf("reference has %d rows, want %d built-in + 2 contributed", len(docs), len(builtins))
	}
	// Built-ins come first and are unchanged — the contributed rows are additive.
	for i, b := range builtins {
		if docs[i] != b {
			t.Errorf("built-in row %d changed: got %+v, want %+v", i, docs[i], b)
		}
	}
	// Each contributed phrase carries its documentation, not just its pattern: a
	// pattern with no summary or example is a row a reader cannot act on.
	for _, want := range []StepDoc{
		{Group: "Extension: Revenue", Pattern: `^the revenue floor is (\d+)$`, Summary: "Asserts the floor.", Example: "Then the revenue floor is 4"},
		{Group: "Extension: Revenue", Pattern: `^the revenue ceiling is (\d+)$`, Summary: "Asserts the ceiling.", Example: "Then the revenue ceiling is 9"},
	} {
		found := false
		for _, d := range docs {
			if d == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("contributed row %+v is missing from the engine reference", want)
		}
	}
}

// TestEngineStepDocsStaysContiguousWhenAPhraseReusesABuiltinGroupName is FR-014 and
// the specific edge case R9 identified.
//
// The markdown generator emits one heading per group by watching for the group to
// CHANGE. Contributed phrases render after the built-in groups, so a comparator
// declaring an existing name like "Shape" would reopen a block that already closed
// and produce a duplicated heading. Qualifying contributed group names makes that
// impossible regardless of what authors choose.
func TestEngineStepDocsStaysContiguousWhenAPhraseReusesABuiltinGroupName(t *testing.T) {
	t.Parallel()

	// "Shape" is a real built-in group; picking it is the whole point.
	eng := customComparatorEngine(t, withComparator("shape-ext", &validationComparator{
		name: "shape-ext",
		phrases: []core.ContributedPhrase{
			{Pattern: `^a widget matching "([^"]*)" exists$`, Group: "Shape", Summary: "s", Example: "e"},
		},
	}))

	docs, err := EngineStepDocs(eng)
	if err != nil {
		t.Fatalf("EngineStepDocs: %v", err)
	}
	assertGroupsContiguous(t, docs)

	// And the contributed row must be distinguishable from the built-in Shape block,
	// not merged into it: a reader needs to know which rows their own code added.
	var sawBuiltinShape, sawExtensionShape bool
	for _, d := range docs {
		switch d.Group {
		case "Shape":
			sawBuiltinShape = true
		case "Extension: Shape":
			sawExtensionShape = true
		}
	}
	if !sawBuiltinShape || !sawExtensionShape {
		t.Errorf("built-in Shape present=%v, contributed Shape present=%v; both must appear, distinctly", sawBuiltinShape, sawExtensionShape)
	}
}

// TestEngineStepDocsContiguityAcrossManyContributors exercises the harder case: two
// comparators declaring the SAME group name must share one block rather than opening
// two, or the reference gains a duplicate heading the moment a second module ships a
// phrase in an existing category.
func TestEngineStepDocsContiguityAcrossManyContributors(t *testing.T) {
	t.Parallel()

	eng := customComparatorEngine(t,
		withComparator("alpha-cmp", &validationComparator{name: "alpha-cmp", phrases: []core.ContributedPhrase{
			{Pattern: `^alpha holds$`, Group: "Shared", Summary: "s", Example: "e"},
		}}),
		withComparator("zeta-cmp", &validationComparator{name: "zeta-cmp", phrases: []core.ContributedPhrase{
			{Pattern: `^zeta holds$`, Group: "Shared", Summary: "s", Example: "e"},
			{Pattern: `^zeta also holds$`, Group: "Other", Summary: "s", Example: "e"},
		}}),
	)

	docs, err := EngineStepDocs(eng)
	if err != nil {
		t.Fatalf("EngineStepDocs: %v", err)
	}
	assertGroupsContiguous(t, docs)
}

// TestEngineStepDocsWithoutContributionsIsIdenticalToBuiltins is SC-009 for the
// reference: the overwhelmingly common engine contributes nothing and must render
// exactly what it rendered before this feature existed.
func TestEngineStepDocsWithoutContributionsIsIdenticalToBuiltins(t *testing.T) {
	t.Parallel()

	docs, err := EngineStepDocs(customComparatorEngine(t))
	if err != nil {
		t.Fatalf("EngineStepDocs: %v", err)
	}
	builtins := StepDocs()
	if len(docs) != len(builtins) {
		t.Fatalf("engine with no contributors rendered %d rows, want %d", len(docs), len(builtins))
	}
	for i := range builtins {
		if docs[i] != builtins[i] {
			t.Errorf("row %d differs: got %+v, want %+v", i, docs[i], builtins[i])
		}
	}
}

// assertGroupsContiguous is the invariant the markdown generator depends on, applied
// to any StepDoc list rather than only the built-in table.
func assertGroupsContiguous(t *testing.T, docs []StepDoc) {
	t.Helper()
	seen := map[string]bool{}
	prev := ""
	for _, d := range docs {
		if d.Group == prev {
			continue
		}
		if seen[d.Group] {
			t.Errorf("group %q reappears after group %q; groups must be contiguous or the generator emits a duplicate heading", d.Group, prev)
		}
		seen[d.Group] = true
		prev = d.Group
	}
}
