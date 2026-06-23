package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
	"github.com/thetonymaster/mentat/internal/registry"
	"github.com/thetonymaster/mentat/internal/report"
)

func init() {
	report.RegisterBuiltins()
}

// errReporter is a test-only Reporter that always returns an error on Report.
type errReporter struct{ msg string }

func (e errReporter) Report(_ core.RunReport, _ io.Writer) error {
	return errors.New(e.msg)
}

func TestEmitReports_UnknownReporter(t *testing.T) {
	err := emitReports(core.RunReport{}, map[string]string{"nope": "/dev/null"})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("want unknown-reporter error containing %q, got %v", "nope", err)
	}
}

func TestEmitReports_WriteAndReadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	err := emitReports(core.RunReport{Total: 1}, map[string]string{"json": path})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("report file not written: %v", err)
	}
}

func TestEmitReports_CreateError(t *testing.T) {
	// A non-existent directory ensures os.Create fails.
	err := emitReports(core.RunReport{}, map[string]string{"json": "/nonexistent-dir/r.json"})
	if err == nil {
		t.Fatal("want create error, got nil")
	}
	if !strings.Contains(err.Error(), "create") {
		t.Fatalf("want error mentioning 'create', got: %v", err)
	}
}

func TestEmitReports_EmptyTargets(t *testing.T) {
	err := emitReports(core.RunReport{}, map[string]string{})
	if err != nil {
		t.Fatalf("empty targets should be a no-op, got: %v", err)
	}
}

func TestEmitReports_ReportError(t *testing.T) {
	const name = "test-fail-reporter"
	registry.RegisterReporter(name, errReporter{msg: "synthetic write failure"})
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	err := emitReports(core.RunReport{}, map[string]string{name: path})
	if err == nil {
		t.Fatal("want report error, got nil")
	}
	if !strings.Contains(err.Error(), "writing") {
		t.Fatalf("want error mentioning 'writing', got: %v", err)
	}
}

func TestParseDur(t *testing.T) {
	tests := []struct {
		name string
		s    string
		def  time.Duration
		want time.Duration
	}{
		{"empty uses default", "", 5 * time.Second, 5 * time.Second},
		{"parses valid duration", "200ms", time.Second, 200 * time.Millisecond},
		{"parses minutes", "2m", time.Second, 2 * time.Minute},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := parseDur(tt.s, tt.def)
			if got != tt.want {
				t.Fatalf("parseDur(%q, %v) = %v; want %v", tt.s, tt.def, got, tt.want)
			}
		})
	}
}

func TestOrDefault(t *testing.T) {
	tests := []struct {
		name string
		n    int
		def  int
		want int
	}{
		{"zero uses default", 0, 3, 3},
		{"non-zero uses n", 5, 3, 5},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := orDefault(tt.n, tt.def)
			if got != tt.want {
				t.Fatalf("orDefault(%d, %d) = %d; want %d", tt.n, tt.def, got, tt.want)
			}
		})
	}
}

// Ensure the fmt import is used.
var _ = fmt.Sprintf
