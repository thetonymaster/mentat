package config

import (
	"strings"
	"testing"
)

func TestLoadAppliesPerAdapterConcurrencyDefaults(t *testing.T) {
	tests := []struct {
		name            string
		adapter         string
		wantConcurrency int
	}{
		{name: "shell defaults to 1", adapter: "shell", wantConcurrency: 1},
		{name: "mcp defaults to 1", adapter: "mcp", wantConcurrency: 1},
		{name: "http defaults to 8", adapter: "http", wantConcurrency: 8},
		{name: "grpc defaults to 8", adapter: "grpc", wantConcurrency: 8},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(`
tempo: { endpoint: "http://localhost:3200" }
otlpEndpoint: "http://localhost:4318"
poll: { interval: "200ms", stableFor: 3, timeout: "30s" }
targets:
  target-a:
    adapter: ` + tt.adapter + `
    command: ["go", "run", "./cmd"]
`)
			cfg, err := Load(data)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := cfg.Targets["target-a"].MaxConcurrency
			if got != tt.wantConcurrency {
				t.Fatalf("%s default concurrency = %d, want %d", tt.adapter, got, tt.wantConcurrency)
			}
			if cfg.OTLPEndpoint != "http://localhost:4318" {
				t.Fatalf("otlpEndpoint = %q, want %q", cfg.OTLPEndpoint, "http://localhost:4318")
			}
		})
	}
}

func TestLoadRejectsNegativeMaxConcurrency(t *testing.T) {
	_, err := Load([]byte(`targets: { mytarget: { adapter: shell, max_concurrency: -1 } }`))
	if err == nil {
		t.Fatal("expected error for negative max_concurrency")
	}
	msg := err.Error()
	if !strings.Contains(msg, "mytarget") {
		t.Fatalf("error %q does not contain target name %q", msg, "mytarget")
	}
	if !strings.Contains(msg, "-1") {
		t.Fatalf("error %q does not contain value %q", msg, "-1")
	}
}

func TestLoadRejectsUnknownAdapter(t *testing.T) {
	_, err := Load([]byte(`targets: { x: { adapter: telepathy } }`))
	if err == nil {
		t.Fatal("expected error for unknown adapter")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x") {
		t.Fatalf("error %q does not contain target name %q", msg, "x")
	}
	if !strings.Contains(msg, "telepathy") {
		t.Fatalf("error %q does not contain adapter value %q", msg, "telepathy")
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	_, err := Load([]byte("targets: [unterminated"))
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}
