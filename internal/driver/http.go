package driver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/baggage"

	"github.com/thetonymaster/mentat/internal/core"
)

// httpClientTimeout bounds the request so a stalled SUT cannot hang a run.
const httpClientTimeout = 30 * time.Second

const (
	tagRunID       = "test.run.id"
	tagScenario    = "test.scenario"
	headerScenario = "X-Scenario"
	headerBaggage  = "baggage"
)

type httpDriver struct{ hc *http.Client }

// NewHTTP returns the http driver adapter. It is a plain, un-instrumented,
// non-exporting HTTP client: it injects correlation baggage only (no traceparent),
// so the SUT's first server span roots the trace (spec §3).
func NewHTTP() core.Driver {
	return httpDriver{hc: &http.Client{Timeout: httpClientTimeout}}
}

func (d httpDriver) Run(ctx context.Context, spec core.RunSpec) (core.RunResult, error) {
	if spec.HTTP.URL == "" {
		return core.RunResult{}, fmt.Errorf("http: empty URL for target %q", spec.Target)
	}
	if spec.HTTP.Method == "" {
		return core.RunResult{}, fmt.Errorf("http: empty method for target %q", spec.Target)
	}
	scenario := scenarioFromArgs(spec.Command)

	req, err := http.NewRequestWithContext(ctx, spec.HTTP.Method, spec.HTTP.URL, bytes.NewReader([]byte(spec.Input)))
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: build request %s %s: %w", spec.HTTP.Method, spec.HTTP.URL, err)
	}
	for k, v := range spec.HTTP.Headers {
		req.Header.Set(k, v)
	}
	if scenario != "" {
		req.Header.Set(headerScenario, scenario)
	}
	bag, err := buildBaggage(spec.Tags[tagRunID], scenario)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: build baggage for run %q: %w", spec.RunID, err)
	}
	if bag != "" {
		req.Header.Set(headerBaggage, bag)
	}

	resp, err := d.hc.Do(req)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: %s %s for run %q: %w", spec.HTTP.Method, spec.HTTP.URL, spec.RunID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.RunResult{}, fmt.Errorf("http: read response body for run %q: %w", spec.RunID, err)
	}

	// A non-2xx response is data the comparators judge, not a driver error.
	out := core.Output{
		Status: resp.StatusCode,
		Body:   body,
		Answer: string(body),
	}
	return core.RunResult{RunID: spec.RunID, Output: out}, nil
}

// scenarioFromArgs extracts the value of the --scenario flag from the driver
// args (the http adapter consumes args directly, the way the shell adapter hands
// them to a subprocess). Absent --scenario yields "" — a valid empty scenario,
// not an error.
func scenarioFromArgs(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--scenario" {
			return args[i+1]
		}
	}
	return ""
}

// buildBaggage renders the W3C baggage header value carrying the correlation tags.
// Empty runID and empty scenario both yield no member; an all-empty result returns
// "" so the caller omits the header.
func buildBaggage(runID, scenario string) (string, error) {
	var members []baggage.Member
	if runID != "" {
		m, err := baggage.NewMember(tagRunID, runID)
		if err != nil {
			return "", fmt.Errorf("baggage member %s=%q: %w", tagRunID, runID, err)
		}
		members = append(members, m)
	}
	if scenario != "" {
		m, err := baggage.NewMember(tagScenario, scenario)
		if err != nil {
			return "", fmt.Errorf("baggage member %s=%q: %w", tagScenario, scenario, err)
		}
		members = append(members, m)
	}
	if len(members) == 0 {
		return "", nil
	}
	b, err := baggage.New(members...)
	if err != nil {
		return "", fmt.Errorf("build baggage: %w", err)
	}
	return b.String(), nil
}
