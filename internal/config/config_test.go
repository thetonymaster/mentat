package config

import (
	"strings"
	"testing"
)

func TestLoadAppliesPerAdapterConcurrencyDefaults(t *testing.T) {
	tests := []struct {
		name            string
		adapter         string
		extraYAML       string
		wantConcurrency int
	}{
		{name: "shell defaults to 1", adapter: "shell", wantConcurrency: 1},
		{name: "mcp defaults to 1", adapter: "mcp", wantConcurrency: 1},
		{
			name:    "http defaults to 8",
			adapter: "http",
			extraYAML: `
    http:
      url: "http://localhost:8080"
      method: GET`,
			wantConcurrency: 8,
		},
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
    command: ["go", "run", "./cmd"]` + tt.extraYAML + `
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

func TestLoadStore(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantStore string
	}{
		{
			name:      "defaults to tempo when unset",
			yaml:      "tempo:\n  endpoint: http://localhost:3200\n",
			wantStore: "tempo",
		},
		{
			name:      "keeps explicit store",
			yaml:      "store: jaeger\ntempo:\n  endpoint: http://localhost:3200\n",
			wantStore: "jaeger",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Store != tt.wantStore {
				t.Fatalf("Store = %q, want %q", c.Store, tt.wantStore)
			}
		})
	}
}

func TestLoadPricing(t *testing.T) {
	data := []byte(`
store: tempo
pricing:
  claude-opus-4-8:   { inputPerMTok: 15.0, outputPerMTok: 75.0 }
  claude-sonnet-4-6: { inputPerMTok: 3.0,  outputPerMTok: 15.0 }
`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	opus, ok := cfg.Pricing["claude-opus-4-8"]
	if !ok {
		t.Fatalf("pricing missing claude-opus-4-8; got %+v", cfg.Pricing)
	}
	if opus.InputPerMTok != 15.0 || opus.OutputPerMTok != 75.0 {
		t.Fatalf("opus rate = %+v, want {15 75}", opus)
	}
	if cfg.Pricing["claude-sonnet-4-6"].OutputPerMTok != 15.0 {
		t.Fatalf("sonnet outputPerMTok = %v, want 15", cfg.Pricing["claude-sonnet-4-6"].OutputPerMTok)
	}
}

func TestLoadHTTPTarget(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErr     bool
		wantErrSub  string
		wantURL     string
		wantMethod  string
		wantConc    int
		wantHeaders map[string]string
	}{
		{
			name: "valid http target loads with default concurrency 8",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
      method: POST
      headers:
        Content-Type: application/json
`,
			wantURL:     "http://localhost:8080/orders",
			wantMethod:  "POST",
			wantConc:    8,
			wantHeaders: map[string]string{"Content-Type": "application/json"},
		},
		{
			name: "http target without headers loads (headers optional)",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
      method: POST
`,
			wantURL:    "http://localhost:8080/orders",
			wantMethod: "POST",
			wantConc:   8,
		},
		{
			name: "http target missing url is a descriptive error",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      method: POST
`,
			wantErr:    true,
			wantErrSub: `target "checkout": http.url is required`,
		},
		{
			name: "http target missing method is a descriptive error",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
`,
			wantErr:    true,
			wantErrSub: `target "checkout": http.method is required`,
		},
		{
			name: "http target whitespace-only url is a descriptive error",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "   "
      method: POST
`,
			wantErr:    true,
			wantErrSub: `target "checkout": http.url is required`,
		},
		{
			name: "http target whitespace-only method is a descriptive error",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "http://localhost:8080/orders"
      method: "   "
`,
			wantErr:    true,
			wantErrSub: `target "checkout": http.method is required`,
		},
		{
			name: "http target trims surrounding whitespace on url and method",
			yaml: `
targets:
  checkout:
    adapter: http
    http:
      url: "  http://localhost:8080/orders  "
      method: "  POST  "
`,
			wantURL:    "http://localhost:8080/orders",
			wantMethod: "POST",
			wantConc:   8,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := cfg.Targets["checkout"]
			if got.HTTP.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.HTTP.URL, tt.wantURL)
			}
			if got.HTTP.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", got.HTTP.Method, tt.wantMethod)
			}
			if got.MaxConcurrency != tt.wantConc {
				t.Errorf("MaxConcurrency = %d, want %d", got.MaxConcurrency, tt.wantConc)
			}
			for k, v := range tt.wantHeaders {
				if got.HTTP.Headers[k] != v {
					t.Errorf("Headers[%q] = %q, want %q", k, got.HTTP.Headers[k], v)
				}
			}
		})
	}
}
