package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/thetonymaster/mentat/internal/comparator"
	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/expectations"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/trace"
)

// maxConcurrentResolves bounds concurrent trace resolutions per engine. The
// per-target slot bounds SUT execution only (FR-001), so this separate, generous
// bound is what keeps a large parallel batch from stampeding the trace store —
// without re-serializing the batch. Internal constant by design, not user-facing
// configuration (feature 004, research R2).
const maxConcurrentResolves = 8

// Engine wires configuration, a trace store, and a correlator into the
// Drive/Comparator lifecycle. Build is the only way to construct it.
type Engine struct {
	cfg      config.Config
	cor      core.Correlator
	st       core.TraceStore
	sems     map[string]chan struct{} // per-target concurrency gate (SUT execution only)
	pinned   string                   // when set, Drive resolves this run id instead of driving
	pricing  core.Pricing
	patterns expectations.Patterns
	logger   *slog.Logger       // silent (discard) by default; emits drive.start lifecycle narration to stderr
	reg      *registry.Registry // this engine's own sealed seam registry (comparators/drivers/matchers)

	// resolveSem gates cor.Resolve calls at maxConcurrentResolves. Lazily built on
	// first use (sync.Once, race-free) rather than wired in Build: it is a fixed
	// internal bound, not a configurable seam, and lazy init keeps every existing
	// Engine construction path working unchanged.
	resolveOnce sync.Once
	resolveSem  chan struct{}
}

// withResolveSlot runs fn under the engine-wide resolve bound. Waiting for a
// resolve slot honours ctx so a cancelled run never blocks on the gate.
func (e *Engine) withResolveSlot(ctx context.Context, fn func(ctx context.Context) (*trace.Trace, error)) (*trace.Trace, error) {
	e.resolveOnce.Do(func() { e.resolveSem = make(chan struct{}, maxConcurrentResolves) })
	select {
	case e.resolveSem <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("engine: wait for trace-resolution slot: %w", ctx.Err())
	}
	defer func() { <-e.resolveSem }()
	return fn(ctx)
}

// resolve runs the LIVE stability-gated cor.Resolve under the resolve bound —
// the only resolution mode reachable from live drives (FR-004).
func (e *Engine) resolve(ctx context.Context, runID string) (*trace.Trace, error) {
	return e.withResolveSlot(ctx, func(ctx context.Context) (*trace.Trace, error) {
		return e.cor.Resolve(ctx, e.st, runID)
	})
}

// resolveComplete runs the KNOWN-COMPLETE cor.ResolveComplete under the same
// resolve bound. Reached only via the pinned branch: a pinned run is by
// definition saved/historical — PinRun's sole production caller is
// ctl.ReplayFeature (feature 004, plan U1) — so replay skips the stability
// sleep while live paths (Drive unpinned, DriveN) cannot reach this mode.
func (e *Engine) resolveComplete(ctx context.Context, runID string) (*trace.Trace, error) {
	return e.withResolveSlot(ctx, func(ctx context.Context) (*trace.Trace, error) {
		return e.cor.ResolveComplete(ctx, e.st, runID)
	})
}

// Pricing returns the per-model cost table wired at Build (may be nil).
func (e *Engine) Pricing() core.Pricing { return e.pricing }

// ShapePattern resolves a named sidecar shape pattern loaded at Build. The bool is false
// for an unknown name; the step layer pre-checks names in sc.Before so this is a safety net.
func (e *Engine) ShapePattern(name string) ([]comparator.ShapeExpectation, bool) {
	return e.patterns.Get(name)
}

// PinRun makes subsequent Drive calls resolve runID from the store instead of
// running the SUT — used by `mentatctl agent replay` to re-evaluate a stored run.
//
// Invariant: PinRun MUST be called before any concurrent Drive (i.e. at single-threaded
// composition/setup time, the same discipline the registry uses — populated before
// concurrent scenario execution begins). No mutex is needed because this is a setup-time
// operation, not a concurrent one.
func (e *Engine) PinRun(runID string) { e.pinned = runID }

// Adapter reports the adapter kind of a configured target ("shell", "http", …) and
// whether the target exists. Read-only; the step layer uses it to reject a
// request-body step against an adapter that does not consume a body (only http
// does), so the body is never silently discarded (Constitution IV).
func (e *Engine) Adapter(target string) (string, bool) {
	t, ok := e.cfg.Targets[target]
	return t.Adapter, ok
}

// Comparator resolves a named comparator from this engine's own registry.
func (e *Engine) Comparator(name string) (core.Comparator, bool) {
	return e.reg.Comparator(name)
}

// AggregateComparator resolves a named aggregate comparator from this engine's registry.
func (e *Engine) AggregateComparator(name string) (core.AggregateComparator, bool) {
	return e.reg.AggregateComparator(name)
}

// Drive injects the run tag, runs the SUT, then resolves and merges its trace.
// When PinRun was called, it resolves the pinned run id without driving.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	if e.pinned != "" {
		tr, err := e.resolveComplete(ctx, e.pinned)
		if err != nil {
			return core.Evidence{}, fmt.Errorf("engine: resolve pinned run %q: %w", e.pinned, err)
		}
		return core.Evidence{RunID: e.pinned, Trace: tr}, nil
	}
	ev, err := e.driveOnce(ctx, target, args, "")
	if err != nil {
		return core.Evidence{}, err
	}
	return ev, nil
}

// driveOnce performs one live drive. On a harness failure it returns an Evidence
// flagged Failed (with RunID + FailureKind) AND the wrapped error, so single-run
// Drive can surface the error while multi-run DriveN can record the sample.
// Structural errors (unknown target/adapter) carry an empty RunID.
func (e *Engine) driveOnce(ctx context.Context, target string, args []string, input string) (core.Evidence, error) {
	t, ok := e.cfg.Targets[target]
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: unknown target %q", target)
	}
	drv, ok := e.reg.Driver(t.Adapter)
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: no driver for adapter %q", t.Adapter)
	}
	// The target's resolved run budget bounds this run (feature 003). KillGrace rides
	// the spec so the driver reaps the process tree without importing config. The run
	// context derives its deadline from the scenario context and the budget timeout,
	// so cancellation composes: whichever bound (scenario, budget, poll) fires first
	// wins. A non-positive Timeout means no per-run bound (a zero-value budget from a
	// hand-built config); "unbounded" is the explicit opt-out.
	budget := t.Budget
	// Only inject OTEL_EXPORTER_OTLP_ENDPOINT when one is configured (FR-006/SC-004).
	// The shell driver appends spec.Env AFTER os.Environ(), so injecting an empty
	// value would override the SUT's working ambient endpoint and export nowhere
	// (audit D4). Absent key ⇒ ambient survives; configured value ⇒ config wins.
	env := map[string]string{}
	if e.cfg.OTLPEndpoint != "" {
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = e.cfg.OTLPEndpoint
	}
	spec := core.RunSpec{
		Target:  target,
		Adapter: t.Adapter,
		Command: append(append([]string{}, t.Command...), args...),
		Input:   input,
		HTTP: core.HTTPSpec{
			URL:     t.HTTP.URL,
			Method:  t.HTTP.Method,
			Headers: t.HTTP.Headers,
		},
		Env:       env,
		KillGrace: budget.KillGrace,
		// The target's answer-extraction policy (US8), converted from validated
		// config (pattern precompiled once at load). The shell driver applies it to
		// stdout; the zero value is whole, so targets without `extract` are unchanged.
		Extract: t.Extract.Policy(),
	}
	runID := e.cor.Inject(ctx, &spec)
	e.logger.InfoContext(ctx, "drive.start", "target", target, "adapter", t.Adapter, "command", spec.Command, "run_id", runID)

	runCtx := ctx
	if !budget.Unbounded && budget.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, budget.Timeout)
		defer cancel()
	}

	sem := e.sems[target]
	select {
	case sem <- struct{}{}:
	case <-runCtx.Done():
		werr := budgetTimeout(ctx, runCtx, target, "drive", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: drive %q: %w", target, runCtx.Err())
		}
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver, FailureMsg: werr.Error()}, werr
	}
	// The slot bounds SUT execution only (FR-001): it is released explicitly the
	// moment drv.Run returns — never held through trace resolution — so parallel
	// runs overlap their ingestion waits instead of summing them. Explicit release
	// on every path below, not defer-to-end-of-run (feature 004, research R2).

	// If the batch/scenario was cancelled while we waited for the slot, do not start
	// the SUT — its result would be discarded (FR-008). Abort before driving.
	if runCtx.Err() != nil {
		<-sem
		werr := budgetTimeout(ctx, runCtx, target, "drive", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: drive %q: %w", target, runCtx.Err())
		}
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver, FailureMsg: werr.Error()}, werr
	}

	res, err := drv.Run(runCtx, spec)
	<-sem // SUT finished (or failed): free the slot before any resolution work
	if err != nil {
		werr := budgetTimeout(ctx, runCtx, target, "drive", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: drive %q: %w", target, err)
		}
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver, FailureMsg: werr.Error()}, werr
	}
	tr, err := e.resolve(runCtx, runID)
	if err != nil {
		werr := budgetTimeout(ctx, runCtx, target, "resolve", budget)
		if werr == nil {
			werr = fmt.Errorf("engine: resolve run %q: %w", runID, err)
		}
		// Retain the real driver Output: the driver succeeded, only resolution failed.
		return core.Evidence{RunID: runID, Output: res.Output, Failed: true, FailureKind: core.FailureKindResolve, FailureMsg: werr.Error()}, werr
	}
	return core.Evidence{RunID: runID, Trace: tr, Output: res.Output}, nil
}

// budgetTimeout returns the FR-007 per-run-budget-timeout error for a phase
// (drive/resolve), or nil when the failure was NOT a budget timeout (the caller
// then wraps the underlying cause with its usual message). A budget timeout is when
// the derived run context passed its deadline while the parent scenario context is
// still live — a scenario/suite cancellation would also cancel the parent, and is
// reported as cancellation, not a run-budget timeout. DeadlineExceeded is wrapped so
// callers can still errors.Is the returned error.
func budgetTimeout(parent, run context.Context, target, phase string, budget config.RunBudget) error {
	if !errors.Is(run.Err(), context.DeadlineExceeded) || parent.Err() != nil {
		return nil
	}
	return fmt.Errorf("engine: %s %q: run timeout after %s (phase: %s): %w", phase, target, budget.Timeout, phase, context.DeadlineExceeded)
}

// DriveN runs the scenario n times and returns one Evidence per run, driving with
// an empty request body/Input. It is the stable entry point for the prompt/scenario
// drive steps, which carry their argument via Command, not the body. Body-bearing
// steps (US4's HTTP request body) call DriveNInput.
func (e *Engine) DriveN(ctx context.Context, target string, args []string, n int, parallel bool) ([]core.Evidence, error) {
	return e.DriveNInput(ctx, target, args, "", n, parallel)
}

// DriveNInput is DriveN with an explicit request body/Input threaded into each
// run's RunSpec.Input (US4). A non-empty input is the HTTP request body the
// driver sends verbatim; the shell driver ignores Input, so prompt/scenario runs
// pass "" and are byte-identical to before. Semantics are otherwise DriveN's: a
// harness failure on an iteration becomes a typed failed sample (not an aborted
// batch); a structural error aborts. Serial by default; parallel iterations each
// acquire the existing per-target semaphore, with results collected by index.
func (e *Engine) DriveNInput(ctx context.Context, target string, args []string, input string, n int, parallel bool) ([]core.Evidence, error) {
	if n < 1 {
		return nil, fmt.Errorf("engine: DriveN needs n>=1, got %d", n)
	}
	if e.pinned != "" {
		if n > 1 {
			return nil, fmt.Errorf("engine: cannot multi-run a pinned scenario (n=%d); replay is deterministic", n)
		}
		// A pinned engine replays a SAVED run: route through Drive's pinned
		// branch (known-complete resolve, no Inject, no SUT execution) instead of
		// driveOnce's live path. The godog steps always call DriveN, so without
		// this the pinned branch was unreachable from replay: DriveN(n=1)
		// re-drove the SUT and resolved a fresh injected run id (feature 004,
		// plan U1, FR-004).
		ev, err := e.Drive(ctx, target, args)
		if err != nil {
			return nil, err
		}
		return []core.Evidence{ev}, nil
	}
	evs := make([]core.Evidence, n)
	collect := func(ctx context.Context, i int) error {
		ev, err := e.driveOnce(ctx, target, args, input)
		if err != nil && ev.RunID == "" {
			return err // structural error: abort
		}
		evs[i] = ev // success, or a typed failed sample (ev.Failed)
		return nil
	}
	if !parallel {
		for i := 0; i < n; i++ {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
			}
			if err := collect(ctx, i); err != nil {
				return nil, err
			}
		}
		return evs, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
	}
	// Wrap the batch so the first structural error cancels iterations that have not
	// yet started driving (FR-008): driveOnce honours batchCtx at the semaphore and
	// just before driving. Iterations already driving finish (bounded by their run
	// budget) — the guarantee is "not yet started ⇒ never starts", not mid-flight kill.
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var structErr error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if batchCtx.Err() != nil {
				return // batch already doomed: never start this iteration
			}
			if err := collect(batchCtx, i); err != nil {
				mu.Lock()
				if structErr == nil {
					structErr = err
				}
				mu.Unlock()
				cancelBatch()
			}
		}(i)
	}
	wg.Wait()
	if structErr != nil {
		return nil, structErr
	}
	// Mirror the serial path: if the parent context was cancelled mid-batch (after
	// the pre-check, while goroutines ran) with no structural error, un-started
	// iterations left zero-value Evidence. Surface the cancellation rather than
	// returning a silent partial success (CLAUDE.md invariant #4). Check the parent
	// ctx, not batchCtx — a structural cancel of batchCtx already returned via structErr.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
	}
	return evs, nil
}
