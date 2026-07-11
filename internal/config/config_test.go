package config

import (
	"strings"
	"testing"
	"time"
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

func TestLoadRejectsInvalidPricing(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errSub  string
	}{
		{
			name:    "negative inputPerMTok rejected",
			yaml:    "pricing:\n  claude-opus-4-8: { inputPerMTok: -1.0, outputPerMTok: 75.0 }\n",
			wantErr: true,
			errSub:  `claude-opus-4-8": inputPerMTok`,
		},
		{
			name:    "negative outputPerMTok rejected",
			yaml:    "pricing:\n  claude-sonnet-4-6: { inputPerMTok: 3.0, outputPerMTok: -15.0 }\n",
			wantErr: true,
			errSub:  `claude-sonnet-4-6": outputPerMTok`,
		},
		{
			name:    "NaN inputPerMTok rejected",
			yaml:    "pricing:\n  m: { inputPerMTok: .nan, outputPerMTok: 1.0 }\n",
			wantErr: true,
			errSub:  `m": inputPerMTok`,
		},
		{
			name:    "infinite outputPerMTok rejected",
			yaml:    "pricing:\n  m: { inputPerMTok: 1.0, outputPerMTok: .inf }\n",
			wantErr: true,
			errSub:  `m": outputPerMTok`,
		},
		{
			name:    "empty model name rejected",
			yaml:    "pricing:\n  \"\": { inputPerMTok: 1.0, outputPerMTok: 1.0 }\n",
			wantErr: true,
			errSub:  "model name must be non-empty",
		},
		{
			name:    "zero rate is allowed (free model)",
			yaml:    "pricing:\n  free-model: { inputPerMTok: 0.0, outputPerMTok: 0.0 }\n",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestLoadExpectationsDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"defaults when absent", "store: tempo\n", "expectations"},
		{"explicit value preserved", "store: tempo\nexpectations: ./exp\n", "./exp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Expectations != tt.want {
				t.Errorf("Expectations = %q, want %q", c.Expectations, tt.want)
			}
		})
	}
}

func TestLoadPollSearchLimitDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want int
	}{
		{
			name: "defaults to 100 when poll block omitted",
			yaml: "store: tempo\n",
			want: 100,
		},
		{
			name: "defaults to 100 when searchLimit omitted from poll block",
			yaml: "poll:\n  interval: 200ms\n  timeout: 30s\n  stableFor: 3\n",
			want: 100,
		},
		{
			name: "explicit searchLimit preserved",
			yaml: "poll:\n  searchLimit: 250\n",
			want: 250,
		},
		{
			name: "non-positive searchLimit defaults to 100",
			yaml: "poll:\n  searchLimit: 0\n",
			want: 100,
		},
		{
			name: "negative searchLimit defaults to 100",
			yaml: "poll:\n  searchLimit: -5\n",
			want: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Poll.SearchLimit != tt.want {
				t.Fatalf("Poll.SearchLimit = %d, want %d", c.Poll.SearchLimit, tt.want)
			}
		})
	}
}

func TestLoadJudgeDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		yaml        string
		wantBackend string
		wantModel   string
		wantVotes   int
		wantTemp    float64
	}{
		{
			name:        "block omitted entirely applies all defaults",
			yaml:        "store: tempo\n",
			wantBackend: "claude",
			wantModel:   "claude-opus-4-8",
			wantVotes:   1,
			wantTemp:    0,
		},
		{
			name:        "empty backend defaults to claude",
			yaml:        "judge:\n  model: claude-haiku-4-5\n  votes: 3\n",
			wantBackend: "claude",
			wantModel:   "claude-haiku-4-5",
			wantVotes:   3,
			wantTemp:    0,
		},
		{
			name:        "empty model defaults to claude-opus-4-8",
			yaml:        "judge:\n  backend: claude\n  votes: 1\n",
			wantBackend: "claude",
			wantModel:   "claude-opus-4-8",
			wantVotes:   1,
			wantTemp:    0,
		},
		{
			name:        "zero votes defaults to 1",
			yaml:        "judge:\n  backend: claude\n  model: claude-opus-4-8\n",
			wantBackend: "claude",
			wantModel:   "claude-opus-4-8",
			wantVotes:   1,
			wantTemp:    0,
		},
		{
			name:        "fully specified valid block round-trips",
			yaml:        "judge:\n  backend: claude\n  model: claude-sonnet-4-6\n  votes: 5\n  temperature: 0.7\n",
			wantBackend: "claude",
			wantModel:   "claude-sonnet-4-6",
			wantVotes:   5,
			wantTemp:    0.7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Judge.Backend != tt.wantBackend {
				t.Errorf("Judge.Backend = %q, want %q", c.Judge.Backend, tt.wantBackend)
			}
			if c.Judge.Model != tt.wantModel {
				t.Errorf("Judge.Model = %q, want %q", c.Judge.Model, tt.wantModel)
			}
			if c.Judge.Votes != tt.wantVotes {
				t.Errorf("Judge.Votes = %d, want %d", c.Judge.Votes, tt.wantVotes)
			}
			if c.Judge.Temperature != tt.wantTemp {
				t.Errorf("Judge.Temperature = %v, want %v", c.Judge.Temperature, tt.wantTemp)
			}
		})
	}
}

func TestLoadRejectsInvalidJudge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errSub  string
	}{
		{
			name:    "negative votes rejected",
			yaml:    "judge:\n  votes: -1\n",
			wantErr: true,
			errSub:  "judge.votes must be >= 1, got -1",
		},
		{
			name:    "even votes 2 rejected",
			yaml:    "judge:\n  votes: 2\n",
			wantErr: true,
			errSub:  "judge.votes must be odd, got 2",
		},
		{
			name:    "even votes 4 rejected",
			yaml:    "judge:\n  votes: 4\n",
			wantErr: true,
			errSub:  "judge.votes must be odd, got 4",
		},
		{
			name:    "odd votes 3 allowed",
			yaml:    "judge:\n  votes: 3\n",
			wantErr: false,
		},
		{
			name:    "odd votes 5 allowed",
			yaml:    "judge:\n  votes: 5\n",
			wantErr: false,
		},
		{
			name:    "negative temperature rejected",
			yaml:    "judge:\n  temperature: -0.5\n",
			wantErr: true,
			errSub:  "judge.temperature must be finite and >= 0, got -0.5",
		},
		{
			name:    "NaN temperature rejected",
			yaml:    "judge:\n  temperature: .nan\n",
			wantErr: true,
			errSub:  "judge.temperature must be finite and >= 0",
		},
		{
			name:    "infinite temperature rejected",
			yaml:    "judge:\n  temperature: .inf\n",
			wantErr: true,
			errSub:  "judge.temperature must be finite and >= 0",
		},
		{
			name:    "zero temperature allowed",
			yaml:    "judge:\n  votes: 1\n  temperature: 0\n",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
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

// --- Feature 003: run lifecycle budget (run_timeout / kill_grace) ------------

func TestLoadResolvesRunBudget(t *testing.T) {
	const defTimeout = 5 * time.Minute
	const defGrace = 10 * time.Second

	tests := []struct {
		name       string
		yaml       string
		wantSuite  RunBudget
		wantTarget RunBudget // resolved budget for target "a"
	}{
		{
			name: "omitted keys apply documented defaults and target inherits",
			yaml: `
targets:
  a: { adapter: shell, command: ["true"] }
`,
			wantSuite:  RunBudget{Timeout: defTimeout, Unbounded: false, KillGrace: defGrace},
			wantTarget: RunBudget{Timeout: defTimeout, Unbounded: false, KillGrace: defGrace},
		},
		{
			name: "explicit suite values resolve and target inherits",
			yaml: `
run_timeout: 2m
kill_grace: 3s
targets:
  a: { adapter: shell, command: ["true"] }
`,
			wantSuite:  RunBudget{Timeout: 2 * time.Minute, Unbounded: false, KillGrace: 3 * time.Second},
			wantTarget: RunBudget{Timeout: 2 * time.Minute, Unbounded: false, KillGrace: 3 * time.Second},
		},
		{
			name: "suite unbounded opt-in propagates to target",
			yaml: `
run_timeout: unbounded
targets:
  a: { adapter: shell, command: ["true"] }
`,
			wantSuite:  RunBudget{Timeout: 0, Unbounded: true, KillGrace: defGrace},
			wantTarget: RunBudget{Timeout: 0, Unbounded: true, KillGrace: defGrace},
		},
		{
			name: "per-target override wins over suite; kill_grace from suite",
			yaml: `
run_timeout: 5m
kill_grace: 7s
targets:
  a: { adapter: shell, command: ["true"], run_timeout: 10m }
`,
			wantSuite:  RunBudget{Timeout: defTimeout, Unbounded: false, KillGrace: 7 * time.Second},
			wantTarget: RunBudget{Timeout: 10 * time.Minute, Unbounded: false, KillGrace: 7 * time.Second},
		},
		{
			name: "per-target unbounded override while suite is bounded",
			yaml: `
run_timeout: 5m
targets:
  a: { adapter: shell, command: ["true"], run_timeout: unbounded }
`,
			wantSuite:  RunBudget{Timeout: defTimeout, Unbounded: false, KillGrace: defGrace},
			wantTarget: RunBudget{Timeout: 0, Unbounded: true, KillGrace: defGrace},
		},
		{
			name: "per-target bounded override while suite is unbounded",
			yaml: `
run_timeout: unbounded
targets:
  a: { adapter: shell, command: ["true"], run_timeout: 30s }
`,
			wantSuite:  RunBudget{Timeout: 0, Unbounded: true, KillGrace: defGrace},
			wantTarget: RunBudget{Timeout: 30 * time.Second, Unbounded: false, KillGrace: defGrace},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Budget != tt.wantSuite {
				t.Fatalf("suite Budget = %+v, want %+v", cfg.Budget, tt.wantSuite)
			}
			got := cfg.Targets["a"].Budget
			if got != tt.wantTarget {
				t.Fatalf("target %q Budget = %+v, want %+v", "a", got, tt.wantTarget)
			}
		})
	}
}

func TestLoadRejectsBadLifecycleConfig(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantErrSub []string // all substrings must appear
	}{
		{
			name:       "suite run_timeout typo is a parse error",
			yaml:       `run_timeout: foo`,
			wantErrSub: []string{"run_timeout", "foo"},
		},
		{
			name: "per-target run_timeout typo names the target",
			yaml: `
targets:
  a: { adapter: shell, command: ["true"], run_timeout: nope }
`,
			wantErrSub: []string{"run_timeout", "a", "nope"},
		},
		{
			name:       "zero kill_grace is rejected (must be > 0)",
			yaml:       `kill_grace: 0s`,
			wantErrSub: []string{"kill_grace"},
		},
		{
			name:       "negative kill_grace is rejected",
			yaml:       `kill_grace: -5s`,
			wantErrSub: []string{"kill_grace"},
		},
		{
			name:       "kill_grace typo is a parse error",
			yaml:       `kill_grace: soon`,
			wantErrSub: []string{"kill_grace", "soon"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			for _, sub := range tt.wantErrSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q does not contain %q", err.Error(), sub)
				}
			}
		})
	}
}
