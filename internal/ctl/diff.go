package ctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/genai"
	"github.com/thetonymaster/mentat/internal/trace"
)

// seqFunc selects an ordered identity sequence from a run's trace. Tool and
// service domains differ only in which selector they pass to diffWith.
type seqFunc func(*trace.Trace) ([]string, error)

func toolSeq(tr *trace.Trace) ([]string, error) {
	var out []string
	for _, s := range tr.ByOp(genai.OpExecuteTool) {
		out = append(out, s.Attr(genai.ToolName))
	}
	return out, nil
}

// Diff compares the ordered tool sequences of two runs, position by position.
func Diff(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error {
	return diffWith(ctx, cor, st, idA, idB, w, toolSeq, "tool")
}

// DiffServices compares the ordered service sequences of two runs (the service
// domain), reusing the sequence comparator's Kind:"service" selection.
func DiffServices(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer) error {
	return diffWith(ctx, cor, st, idA, idB, w, comparator.ServiceSequence, "service")
}

func diffWith(ctx context.Context, cor core.Correlator, st core.TraceStore, idA, idB string, w io.Writer, sel seqFunc, noun string) error {
	// Both runs are saved/historical, so their known-complete resolves are
	// independent single fetch passes — overlap them instead of paying the
	// resolve latency twice (feature 004, US3). Both goroutines run to
	// completion; failures are then surfaced deterministically (A before B)
	// with BOTH errors joined when both fail — the second failure is never
	// lost silently, and each error names the run id that failed.
	var (
		ta, tb     *trace.Trace
		errA, errB error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); ta, errA = Resolve(ctx, cor, st, idA) }()
	go func() { defer wg.Done(); tb, errB = Resolve(ctx, cor, st, idB) }()
	wg.Wait()
	if errA != nil || errB != nil {
		var errs []error
		if errA != nil {
			errs = append(errs, fmt.Errorf("diff: run %s: %w", idA, errA))
		}
		if errB != nil {
			errs = append(errs, fmt.Errorf("diff: run %s: %w", idB, errB))
		}
		return errors.Join(errs...)
	}
	a, err := sel(ta)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idA, err)
	}
	b, err := sel(tb)
	if err != nil {
		return fmt.Errorf("diff: run %s: %w", idB, err)
	}
	if _, err := fmt.Fprintf(w, "A=%s  B=%s\n", idA, idB); err != nil {
		return fmt.Errorf("diff: write header: %w", err)
	}
	if equalSeq(a, b) {
		if _, err := fmt.Fprintf(w, "%s sequences identical\n", noun); err != nil {
			return fmt.Errorf("diff: write identical line: %w", err)
		}
		return nil
	}
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		av, bv := at(a, i), at(b, i)
		mark := " "
		if av != bv {
			mark = "≠"
		}
		if _, err := fmt.Fprintf(w, "%2d %s A:%-15s B:%s\n", i+1, mark, av, bv); err != nil {
			return fmt.Errorf("diff: write line %d: %w", i+1, err)
		}
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
