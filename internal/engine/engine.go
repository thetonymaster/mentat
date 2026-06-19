package engine

import (
	"context"
	"fmt"

	"github.com/thetonymaster/mentat/internal/config"
	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
)

// Engine wires configuration, a trace store, and a correlator into the
// Drive/Comparator lifecycle. Build is the only way to construct it.
type Engine struct {
	cfg    config.Config
	cor    core.Correlator
	st     core.TraceStore
	sems   map[string]chan struct{} // per-target concurrency gate
	pinned string                   // when set, Drive resolves this run id instead of driving
}

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

// Drive injects the run tag, runs the SUT via its adapter, then resolves and
// merges the run's trace. The per-target semaphore enforces max_concurrency.
// When PinRun has been called, Drive resolves the pinned run id from the store
// and returns it directly without injecting or running the SUT.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
	if e.pinned != "" {
		tr, err := e.cor.Resolve(ctx, e.st, e.pinned)
		if err != nil {
			return core.Evidence{}, fmt.Errorf("engine: resolve pinned run %q: %w", e.pinned, err)
		}
		return core.Evidence{RunID: e.pinned, Trace: tr}, nil
	}
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
	// Inject sets spec.RunID and spec.Tags["test.run.id"] in place (pointer
	// receiver), so the subsequent drv.Run value-copy carries the run id.
	runID := e.cor.Inject(ctx, &spec)

	sem := e.sems[target]
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return core.Evidence{}, fmt.Errorf("engine: drive %q: %w", target, ctx.Err())
	}
	defer func() { <-sem }()

	res, err := drv.Run(ctx, spec)
	if err != nil {
		return core.Evidence{}, fmt.Errorf("engine: drive %q: %w", target, err)
	}
	tr, err := e.cor.Resolve(ctx, e.st, runID)
	if err != nil {
		return core.Evidence{}, fmt.Errorf("engine: resolve run %q: %w", runID, err)
	}
	return core.Evidence{RunID: runID, Trace: tr, Output: res.Output}, nil
}
