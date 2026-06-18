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
	cfg  config.Config
	cor  core.Correlator
	st   core.TraceStore
	sems map[string]chan struct{} // per-target concurrency gate
}

// Comparator resolves a named comparator from the global registry.
func (e *Engine) Comparator(name string) (core.Comparator, bool) {
	return registry.Comparator(name)
}

// Drive injects the run tag, runs the SUT via its adapter, then resolves and
// merges the run's trace. The per-target semaphore enforces max_concurrency.
func (e *Engine) Drive(ctx context.Context, target string, args []string) (core.Evidence, error) {
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
		Env:     map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": e.cfg.OTLPEndpoint},
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
