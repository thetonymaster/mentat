// Package kafkaecho is a standalone, third-party example extension for Mentat.
//
// It lives in its own Go module and imports ONLY the public facade
// (github.com/thetonymaster/mentat) plus the standard library — zero
// module-private (internal) imports. That constraint is the whole point: it proves
// the published surface is sufficient to write a custom adapter without forking
// Mentat (spec 007 US1 / SC-001, enforced by a CI import-lint).
//
// The narrative: kafkaecho "drives" a message-queue SUT that Mentat does not ship,
// by echoing the scenario input back as the run's answer ("pong: <scenario>"). It
// follows docs/extending/driver.md — most importantly, it honours tag-first
// correlation by keying every emitted trace on the RunID the engine injects.
//
// A single in-memory Bus couples the custom Driver (which publishes a trace keyed
// by spec.RunID) to the custom Store (which serves that trace back to the
// correlator by the same id). Wire them with New for the common case, or with
// NewDriver/NewStore over a shared Bus when you need finer control.
package kafkaecho

import (
	"sync"

	"github.com/thetonymaster/mentat"
)

// Adapter is the driver registration name. It is what a target's `adapter:` field
// references in config (like the built-in "shell"/"http"), so a scenario drives
// this SUT exactly like a built-in one.
const Adapter = "kafkaecho"

// StoreName is the trace-store registration name. It is what a Config.Store field
// selects (like the built-in "file"/"tempo").
const StoreName = "kafkaecho"

// Bus is a hermetic in-memory trace exchange shared between the custom Driver and
// the custom Store. The driver publishes a forest keyed by the engine-injected run
// id; the store serves that same id back to the correlator. The mutex keeps the map
// race-free under -race (the correlator fetches concurrently).
//
// It stands in for a real OpenTelemetry backend (Tempo): the driver's Publish is
// the analogue of exporting a trace tagged test.run.id=<runID>, and the store's
// lookup is the analogue of querying that tag.
type Bus struct {
	mu     sync.Mutex
	traces map[string]*mentat.Trace
}

// NewBus returns an empty Bus ready to share between a Driver and a Store.
func NewBus() *Bus { return &Bus{traces: map[string]*mentat.Trace{}} }

// Publish stores tr under the run id. The driver calls it once per run.
func (b *Bus) Publish(runID string, tr *mentat.Trace) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.traces[runID] = tr
}

// Lookup returns the trace stored under the run id and whether one was present.
func (b *Bus) Lookup(runID string) (*mentat.Trace, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tr, ok := b.traces[runID]
	return tr, ok
}

// New is the ergonomic constructor: it builds a fresh Bus and returns the driver
// and store factories that share it, ready to pass to mentat.WithDriver /
// mentat.WithStore. Use it when one Run owns both seams (the common case).
func New() (mentat.DriverFactory, mentat.StoreFactory) {
	bus := NewBus()
	driver := func(mentat.Config) (mentat.Driver, error) { return NewDriver(bus), nil }
	store := func(mentat.Config) (mentat.TraceStore, error) { return NewStore(bus), nil }
	return driver, store
}
