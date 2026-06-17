package core

//go:generate mockgen -source=core.go -destination=mocks/mock_core.go -package=mocks

import (
	"context"
	"strings"

	"github.com/thetonymaster/mentat/internal/trace"
)

// Output is the driver-captured boundary result of a run.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int    // shell adapters
	Status   int    // http adapters (HTTP status)
	Body     []byte // http adapters
	Answer   string // extracted result (see ExtractAnswer)
}

// Evidence is everything a comparator may inspect about a single run.
type Evidence struct {
	RunID  string
	Trace  *trace.Trace
	Output Output
}

type Verdict struct {
	Pass    bool
	Reasons []string
}

// Expectation is comparator-specific config; each comparator type-asserts its own.
type Expectation = any

type Comparator interface {
	Name() string
	Compare(ctx context.Context, ev Evidence, e Expectation) (Verdict, error)
}

// RunSpec is the driver input. The adapter applies RunID/Tags via its transport.
type RunSpec struct {
	Target  string
	Adapter string
	Command []string // shell adapter argv
	Env     map[string]string
	Input   string // prompt / request body
	RunID   string
	Tags    map[string]string // test.run.id, test.scenario, test.case
}

type RunResult struct {
	RunID          string
	PrimaryTraceID string
	Output         Output
}

type Driver interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

type TraceQuery struct {
	Tag   string // e.g. "test.run.id"
	Value string
}

type TraceRef struct{ TraceID string }

type StoreCaps struct{ StructuralQuery bool }

type TraceStore interface {
	GetByID(ctx context.Context, id string) (*trace.Trace, error)
	Query(ctx context.Context, q TraceQuery) ([]TraceRef, error)
	Caps() StoreCaps
}

type Correlator interface {
	Inject(ctx context.Context, spec *RunSpec) (runID string)
	Resolve(ctx context.Context, store TraceStore, runID string) (*trace.Trace, error)
}

// ExtractAnswer applies the project-wide convention: stdout is the result.
func ExtractAnswer(stdout string) string { return strings.TrimSpace(stdout) }
