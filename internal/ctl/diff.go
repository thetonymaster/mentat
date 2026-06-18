package ctl

import (
	"context"
	"fmt"
	"io"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

func toolSeq(tr *trace.Trace) []string {
	var out []string
	for _, s := range tr.ByOp(genai.OpExecuteTool) {
		out = append(out, s.Attr(genai.ToolName))
	}
	return out
}

// Diff compares the ordered tool sequences of two runs, position by position.
func Diff(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error {
	ta, err := Resolve(ctx, cor, st, idA)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idA, err)
	}
	tb, err := Resolve(ctx, cor, st, idB)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idB, err)
	}
	a, b := toolSeq(ta), toolSeq(tb)
	fmt.Fprintf(w, "A=%s  B=%s\n", idA, idB)
	if equalSeq(a, b) {
		fmt.Fprintln(w, "tool sequences identical")
		return nil
	}
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		av, bv := at(a, i), at(b, i)
		mark := " "
		if av != bv {
			mark = "≠"
		}
		fmt.Fprintf(w, "%2d %s A:%-15s B:%s\n", i+1, mark, av, bv)
	}
	return nil
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "—"
}
