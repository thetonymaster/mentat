package kafkaecho

import (
	"context"
	"fmt"
	"strings"

	"github.com/thetonymaster/mentat"
)

// Driver is a toy mentat.Driver that "drives" a message-queue SUT Mentat does not
// ship, by echoing the scenario input. It follows docs/extending/driver.md.
type Driver struct {
	bus *Bus
}

// NewDriver returns a Driver publishing traces onto bus (shared with a Store).
func NewDriver(bus *Bus) *Driver { return &Driver{bus: bus} }

// Run drives one scenario. Contract obligations (docs/extending/driver.md):
//
//  1. Tag-first correlation (Constitution II): the engine injected spec.RunID
//     before calling us, so we key the emitted trace on exactly that id — the
//     stand-in for exporting an OTLP trace tagged test.run.id=<runID>. Dropping it
//     would produce a trace the correlator can never resolve.
//  2. No silent fallbacks (Constitution IV): a missing run id is a hard,
//     descriptive error, never a zero-value success.
//  3. A populated Output: comparators read Evidence.Output only (Constitution I),
//     so the echoed answer goes in Output.Answer (what `the result contains …`
//     asserts on) and Output.Stdout.
func (d *Driver) Run(_ context.Context, spec mentat.RunSpec) (mentat.RunResult, error) {
	if spec.RunID == "" {
		// Obligation 2: without an injected run id, tag-first correlation is
		// impossible — fail loudly rather than publish under an empty key.
		return mentat.RunResult{}, fmt.Errorf("kafkaecho: empty RunSpec.RunID; the correlator must inject a run id for tag-first correlation")
	}

	answer := "pong: " + requestPayload(spec)

	// Obligation 1: emit a minimal 1-root-span forest keyed on the injected run id.
	root := &mentat.Span{
		ID:     "root",
		Name:   "kafkaecho.consume",
		Kind:   mentat.KindServer,
		Status: mentat.StatusOk,
		Attrs:  map[string]string{"test.run.id": spec.RunID},
	}
	d.bus.Publish(spec.RunID, &mentat.Trace{
		RunID: spec.RunID,
		Roots: []*mentat.Span{root},
		Spans: []*mentat.Span{root},
	})

	// Obligation 3: return a populated Output and echo the run id back.
	return mentat.RunResult{
		RunID:  spec.RunID,
		Output: mentat.Output{Answer: answer, Stdout: answer},
	}, nil
}

// requestPayload derives the message this echo driver replies to. Scenario/prompt
// steps carry no request body (spec.Input is empty) and pass the scenario name as
// `--scenario <name>` in Command; a request-body step would fill spec.Input. Prefer
// the explicit body, then the scenario name, then the raw command — an echo has no
// error path here, only a most-specific-wins choice.
func requestPayload(spec mentat.RunSpec) string {
	if body := strings.TrimSpace(spec.Input); body != "" {
		return body
	}
	for i, arg := range spec.Command {
		if arg == "--scenario" && i+1 < len(spec.Command) {
			return spec.Command[i+1]
		}
	}
	return strings.TrimSpace(strings.Join(spec.Command, " "))
}
