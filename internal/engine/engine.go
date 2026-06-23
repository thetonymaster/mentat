package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// Engine wires configuration, a trace store, and a correlator into the
// Drive/Comparator lifecycle. Build is the only way to construct it.
type Engine struct {
	cfg     config.Config
	cor     core.Correlator
	st      core.TraceStore
	sems    map[string]chan struct{} // per-target concurrency gate
	pinned  string                   // when set, Drive resolves this run id instead of driving
	pricing core.Pricing
}

// Pricing returns the per-model cost table wired at Build (may be nil).
func (e *Engine) Pricing() core.Pricing { return e.pricing }

// PinRun makes subsequent Drive calls resolve runID from the store instead of
// running the SUT — used by `mentatctl agent replay` to re-evaluate a stored run.
//
// Invariant: PinRun MUST be called before any concurrent Drive (i.e. at single-threaded
// composition/setup time, the same discipline the registry uses — populated before
// concurrent scenario execution begins). No mutex is needed because this is a setup-time
// operation, not a concurrent one.
func (e *Engine) PinRun(runID string) { e.pinned = runID }

// Comparator resolves a named comparator from the global registry.
func (e *Engine) Comparator(name string) (core.Comparator, bool) {
	return registry.Comparator(name)
}

// AggregateComparator resolves a named aggregate comparator from the registry.
func (e *Engine) AggregateComparator(name string) (core.AggregateComparator, bool) {
	return registry.AggregateComparator(name)
}

// Drive injects the run tag, runs the SUT, then resolves and merges its trace.
// When PinRun was called, it resolves the pinned run id without driving.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	if e.pinned != "" {
		tr, err := e.cor.Resolve(ctx, e.st, e.pinned)
		if err != nil {
			return core.Evidence{}, fmt.Errorf("engine: resolve pinned run %q: %w", e.pinned, err)
		}
		return core.Evidence{RunID: e.pinned, Trace: tr}, nil
	}
	ev, err := e.driveOnce(ctx, target, args)
	if err != nil {
		return core.Evidence{}, err
	}
	return ev, nil
}

// driveOnce performs one live drive. On a harness failure it returns an Evidence
// flagged Failed (with RunID + FailureKind) AND the wrapped error, so single-run
// Drive can surface the error while multi-run DriveN can record the sample.
// Structural errors (unknown target/adapter) carry an empty RunID.
func (e *Engine) driveOnce(ctx context.Context, target string, args []string) (core.Evidence, error) {
	t, ok := e.cfg.Targets[target]
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: unknown target %q", target)
	}
	drv, ok := registry.Driver(t.Adapter)
	if !ok {
		return core.Evidence{}, fmt.Errorf("engine: no driver for adapter %q", t.Adapter)
	}
	spec := core.RunSpec{
		Target:  target,
		Adapter: t.Adapter,
		Command: append(append([]string{}, t.Command...), args...),
		HTTP: core.HTTPSpec{
			URL:     t.HTTP.URL,
			Method:  t.HTTP.Method,
			Headers: t.HTTP.Headers,
		},
		Env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": e.cfg.OTLPEndpoint},
	}
	runID := e.cor.Inject(ctx, &spec)

	sem := e.sems[target]
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver}, fmt.Errorf("engine: drive %q: %w", target, ctx.Err())
	}
	defer func() { <-sem }()

	res, err := drv.Run(ctx, spec)
	if err != nil {
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindDriver}, fmt.Errorf("engine: drive %q: %w", target, err)
	}
	tr, err := e.cor.Resolve(ctx, e.st, runID)
	if err != nil {
		return core.Evidence{RunID: runID, Failed: true, FailureKind: core.FailureKindResolve}, fmt.Errorf("engine: resolve run %q: %w", runID, err)
	}
	return core.Evidence{RunID: runID, Trace: tr, Output: res.Output}, nil
}

// DriveN runs the scenario n times and returns one Evidence per run. A harness
// failure on an iteration becomes a typed failed sample (not an aborted batch);
// a structural error aborts. Serial by default; parallel iterations each acquire
// the existing per-target semaphore, with results collected by index.
func (e *Engine) DriveN(ctx context.Context, target string, args []string, n int, parallel bool) ([]core.Evidence, error) {
	if n < 1 {
		return nil, fmt.Errorf("engine: DriveN needs n>=1, got %d", n)
	}
	if e.pinned != "" && n > 1 {
		return nil, fmt.Errorf("engine: cannot multi-run a pinned scenario (n=%d); replay is deterministic", n)
	}
	evs := make([]core.Evidence, n)
	collect := func(i int) error {
		ev, err := e.driveOnce(ctx, target, args)
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
			if err := collect(i); err != nil {
				return nil, err
			}
		}
		return evs, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("engine: DriveN %q cancelled: %w", target, ctx.Err())
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var structErr error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := collect(i); err != nil {
				mu.Lock()
				if structErr == nil {
					structErr = err
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if structErr != nil {
		return nil, structErr
	}
	return evs, nil
}
