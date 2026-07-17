package steps

import (
	"strings"
	"testing"
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
