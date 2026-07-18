package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

func TestLoadAppliesPerAdapterConcurrencyDefaults(t *testing.T) {
	tests := []struct {
		name            string
		adapter         string
		extraYAML       string
		wantConcurrency int
	}{
		{name: "shell defaults to 1", adapter: "shell", wantConcurrency: 1},
		{
			name:    "http defaults to 8",
			adapter: "http",
			extraYAML: `
    http:
      url: "http://localhost:8080"
      method: GET`,
			wantConcurrency: 8,
		},
		// An adapter with no per-adapter default (existence is now validated at
		// engine.Build against the driver registry, D3/FR-005) falls back to a
		// conservative concurrency of 1 at load time rather than erroring.
		{name: "unlisted adapter defaults to 1", adapter: "telepathy", wantConcurrency: 1},
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

// TestLoadAcceptsUnregisteredAdapter proves the D3/FR-005 migration: adapter
// existence is no longer Load's concern (it moved to engine.Build against the
// driver registry, the single source of truth). Load accepts an adapter value it
// has no per-adapter default for, returning nil error and defaulting concurrency
// to 1. A misspelled adapter KEY (adaptor:) is still caught by strict decode —
// that path lives in TestLoadRejectsUnknownKeys and is a different failure class.
func TestLoadAcceptsUnregisteredAdapter(t *testing.T) {
	cfg, err := Load([]byte(`targets: { x: { adapter: telepathy } }`))
	if err != nil {
		t.Fatalf("Load rejected an unregistered adapter value, but existence is validated at engine.Build now: %v", err)
	}
	got := cfg.Targets["x"].MaxConcurrency
	if got != 1 {
		t.Fatalf("target x MaxConcurrency = %d, want 1 (unlisted-adapter default)", got)
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

// TestLoadStorePath pins the file-store config contract (US5): storePath is
// REQUIRED when store is "file" (empty → a hard load error naming the field) and
// carried through to Config; when store is anything else, storePath is ignored.
func TestLoadStorePath(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantPath   string
		wantErr    bool
		wantErrSub string
	}{
		{
			name:     "file store keeps storePath",
			yaml:     "store: file\nstorePath: ./captures\n",
			wantPath: "./captures",
		},
		{
			name:       "file store without storePath errors naming the field",
			yaml:       "store: file\n",
			wantErr:    true,
			wantErrSub: "storePath",
		},
		{
			name:     "non-file store ignores absent storePath",
			yaml:     "store: tempo\ntempo:\n  endpoint: http://localhost:3200\n",
			wantPath: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if c.StorePath != tt.wantPath {
				t.Fatalf("StorePath = %q, want %q", c.StorePath, tt.wantPath)
			}
		})
	}
}

// TestLoadExtract pins the targets.<n>.extract config contract (US8, data-model
// config-`extract` row): default (absent) is whole; marker requires a marker;
// pattern must compile AND carry at least one capture group; an unknown mode is a
// load error. Valid configs convert to the right core.ExtractPolicy, and the
// pattern is precompiled once at load (the compiled regexp rides the policy).
func TestLoadExtract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		yaml         string
		wantErr      bool
		errSub       string
		assertPolicy func(t *testing.T, p core.ExtractPolicy)
	}{
		{
			name: "absent extract defaults to whole (empty mode, nil pattern)",
			yaml: `targets:
  a: { adapter: shell, command: ["true"] }
`,
			assertPolicy: func(t *testing.T, p core.ExtractPolicy) {
				if p.Mode != "" {
					t.Fatalf("Mode = %q, want empty (whole default)", p.Mode)
				}
				if p.Pattern != nil {
					t.Fatalf("Pattern = %v, want nil for whole", p.Pattern)
				}
			},
		},
		{
			name: "explicit whole mode loads",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: whole }
`,
			assertPolicy: func(t *testing.T, p core.ExtractPolicy) {
				if p.Mode != core.ExtractWhole {
					t.Fatalf("Mode = %q, want %q", p.Mode, core.ExtractWhole)
				}
			},
		},
		{
			name: "marker mode with marker loads and converts",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: marker, marker: "ANSWER:" }
`,
			assertPolicy: func(t *testing.T, p core.ExtractPolicy) {
				if p.Mode != core.ExtractMarker || p.Marker != "ANSWER:" {
					t.Fatalf("policy = %+v, want mode=marker marker=ANSWER:", p)
				}
			},
		},
		{
			name: "marker mode without marker errors naming the requirement",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: marker }
`,
			wantErr: true,
			errSub:  "marker is required",
		},
		{
			name: "pattern mode with a valid single-group regex loads, compiles, and converts",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: pattern, pattern: 'id=(\w+)' }
`,
			assertPolicy: func(t *testing.T, p core.ExtractPolicy) {
				if p.Mode != core.ExtractPattern {
					t.Fatalf("Mode = %q, want %q", p.Mode, core.ExtractPattern)
				}
				if p.Pattern == nil {
					t.Fatal("Pattern is nil, want a precompiled regexp riding the policy")
				}
				m := p.Pattern.FindStringSubmatch("id=xyz")
				if len(m) < 2 || m[1] != "xyz" {
					t.Fatalf("compiled pattern did not capture as expected: %v", m)
				}
			},
		},
		{
			name: "pattern mode without pattern errors naming the requirement",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: pattern }
`,
			wantErr: true,
			errSub:  "pattern is required",
		},
		{
			name: "pattern that does not compile errors naming the pattern",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: pattern, pattern: 'id=(' }
`,
			wantErr: true,
			errSub:  "id=(",
		},
		{
			name: "pattern with zero capture groups errors",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: pattern, pattern: 'id=\w+' }
`,
			wantErr: true,
			errSub:  "capture group",
		},
		{
			name: "unknown mode errors naming the mode",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: telepathy }
`,
			wantErr: true,
			errSub:  "telepathy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSub)
				}
				return
			}
			p := cfg.Targets["a"].Extract.Policy()
			if tt.assertPolicy != nil {
				tt.assertPolicy(t, p)
			}
		})
	}
}

// TestLoadRejectsExtractOnNonShellAdapter pins FR-010 / Constitution IV (no silent
// fallbacks): answer extraction is stdout-scoped and only the shell adapter produces
// stdout, so a marker/pattern extract policy on a non-shell adapter (e.g. http) is a
// LOUD config-load failure naming the target, the adapter, and the shell requirement
// — rather than being silently accepted and then ignored at runtime (http.go sets
// Answer = whole body and never reads the policy). whole/empty/absent extract remains
// valid for every adapter (it is the default no-op), which is load-bearing for SC-008
// (zero verdict changes for existing http targets that carry no extract block).
func TestLoadRejectsExtractOnNonShellAdapter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errSubs []string // all must appear when wantErr
	}{
		{
			name: "http adapter with marker extract is rejected naming target, adapter, and shell",
			yaml: `targets:
  web:
    adapter: http
    http: { url: "http://localhost:8080", method: GET }
    extract: { mode: marker, marker: "ANSWER:" }
`,
			wantErr: true,
			errSubs: []string{"web", "http", "shell", "marker"},
		},
		{
			name: "http adapter with pattern extract is rejected naming target, adapter, and shell",
			yaml: `targets:
  web:
    adapter: http
    http: { url: "http://localhost:8080", method: GET }
    extract: { mode: pattern, pattern: 'id=(\w+)' }
`,
			wantErr: true,
			errSubs: []string{"web", "http", "shell", "pattern"},
		},
		{
			name: "shell adapter with marker extract still loads (primary supported case)",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: marker, marker: "ANSWER:" }
`,
			wantErr: false,
		},
		{
			name: "shell adapter with pattern extract still loads (primary supported case)",
			yaml: `targets:
  a:
    adapter: shell
    command: ["true"]
    extract: { mode: pattern, pattern: 'id=(\w+)' }
`,
			wantErr: false,
		},
		{
			name: "http adapter with no extract block still loads (SC-008 zero verdict changes)",
			yaml: `targets:
  web:
    adapter: http
    http: { url: "http://localhost:8080", method: GET }
`,
			wantErr: false,
		},
		{
			name: "http adapter with whole extract mode still loads",
			yaml: `targets:
  web:
    adapter: http
    http: { url: "http://localhost:8080", method: GET }
    extract: { mode: whole }
`,
			wantErr: false,
		},
		{
			name: "http adapter with empty extract mode still loads",
			yaml: `targets:
  web:
    adapter: http
    http: { url: "http://localhost:8080", method: GET }
    extract: { mode: "" }
`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load err=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			for _, sub := range tt.errSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q does not contain %q", err.Error(), sub)
				}
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
			name:        "block omitted entirely applies all defaults (model is fast-tier haiku)",
			yaml:        "store: tempo\n",
			wantBackend: "claude",
			wantModel:   "claude-haiku-4-5",
			wantVotes:   1,
			wantTemp:    0,
		},
		{
			name:        "empty backend defaults to claude (temperature set so votes>1 is valid)",
			yaml:        "judge:\n  model: claude-haiku-4-5\n  votes: 3\n  temperature: 0.7\n",
			wantBackend: "claude",
			wantModel:   "claude-haiku-4-5",
			wantVotes:   3,
			wantTemp:    0.7,
		},
		{
			name:        "empty model defaults to fast-tier haiku",
			yaml:        "judge:\n  backend: claude\n  votes: 1\n",
			wantBackend: "claude",
			wantModel:   "claude-haiku-4-5",
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
			name:    "odd votes 3 with temperature allowed",
			yaml:    "judge:\n  votes: 3\n  temperature: 0.7\n",
			wantErr: false,
		},
		{
			name:    "odd votes 5 with temperature allowed",
			yaml:    "judge:\n  votes: 5\n  temperature: 0.7\n",
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
		{
			name:    "negative max_cost_usd rejected (no silent unlimited fallback)",
			yaml:    "judge:\n  votes: 1\n  max_cost_usd: -0.01\n",
			wantErr: true,
			errSub:  "judge.max_cost_usd must be finite and >= 0",
		},
		{
			name:    "zero max_cost_usd allowed (unlimited)",
			yaml:    "judge:\n  votes: 1\n  max_cost_usd: 0\n",
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

// TestLoadJudgeMaxCostUSD proves the optional judge.max_cost_usd budget knob loads
// (US6): an explicit ceiling round-trips, and an omitted one defaults to 0 —
// unlimited, today's behaviour (no budget accounting).
func TestLoadJudgeMaxCostUSD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantMax float64
	}{
		{
			name:    "omitted defaults to unlimited (0)",
			yaml:    "judge:\n  votes: 1\n",
			wantMax: 0,
		},
		{
			name:    "explicit ceiling round-trips",
			yaml:    "judge:\n  votes: 1\n  max_cost_usd: 0.05\n",
			wantMax: 0.05,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Judge.MaxCostUSD != tt.wantMax {
				t.Errorf("Judge.MaxCostUSD = %v, want %v", c.Judge.MaxCostUSD, tt.wantMax)
			}
		})
	}
}

// TestLoadDefaultsJudgeModelToFastTier proves the US6 default swap (judge-ledger
// contract, Defaults policy): an omitted or empty judge.model resolves to the
// pinned fast-tier default (Haiku-class). The fast tier is >=80% cheaper per token
// than the former Opus-tier default (SC-006, price-sheet math in the PR) and,
// unlike Opus, accepts the temperature knob best-of-N voting needs.
func TestLoadDefaultsJudgeModelToFastTier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{name: "judge block omitted entirely", yaml: "store: tempo\n"},
		{name: "judge block present but model empty", yaml: "judge:\n  backend: claude\n  votes: 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Judge.Model != "claude-haiku-4-5" {
				t.Fatalf("Judge.Model = %q, want fast-tier default %q", c.Judge.Model, "claude-haiku-4-5")
			}
		})
	}
}

// TestLoadRejectsVotesWithoutTemperature proves the US6 best-of-N guard (judge-ledger
// contract, Defaults policy): votes>1 at temperature 0 sends near-identical calls, so
// majority voting is pointless. Load fails loudly with a message naming BOTH remedies
// — raise temperature OR drop to votes: 1 — rather than silently running a useless
// best-of-N (Constitution IV: no silent fallback). votes==1 (the default) and votes>1
// with a positive temperature both load clean.
func TestLoadRejectsVotesWithoutTemperature(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errSubs []string // all must appear when wantErr
	}{
		{
			name:    "votes 3 at temperature 0 rejected naming both remedies",
			yaml:    "judge:\n  votes: 3\n",
			wantErr: true,
			errSubs: []string{"votes=3", "temperature=0", "raise temperature", "set votes: 1"},
		},
		{
			name:    "votes 5 at temperature 0 rejected naming both remedies",
			yaml:    "judge:\n  votes: 5\n",
			wantErr: true,
			errSubs: []string{"votes=5", "raise temperature", "set votes: 1"},
		},
		{
			name:    "votes 3 with temperature 0.7 loads clean",
			yaml:    "judge:\n  votes: 3\n  temperature: 0.7\n",
			wantErr: false,
		},
		{
			name:    "votes 1 at temperature 0 (the defaults) loads clean",
			yaml:    "judge:\n  votes: 1\n  temperature: 0\n",
			wantErr: false,
		},
		{
			name:    "omitted judge block (votes defaults to 1) loads clean",
			yaml:    "store: tempo\n",
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
				for _, sub := range tt.errSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
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

// --- Feature 005 (D2): strict decode rejects unknown keys --------------------

// TestLoadRejectsUnknownKeys proves FR-004/SC-002: a misspelled or stray key at
// any nesting level is a hard, named error, not a silent fallback to defaults.
// One typo is injected per config section; the error must name the offending key.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantKey string // the offending key name must appear in the error
	}{
		{
			name:    "root-level typo",
			yaml:    "stroe: tempo\n",
			wantKey: "stroe",
		},
		{
			name:    "stray root key with no matching section",
			yaml:    "reporters: []\n",
			wantKey: "reporters",
		},
		{
			name:    "poll section typo",
			yaml:    "poll:\n  timout: 30s\n",
			wantKey: "timout",
		},
		{
			name:    "judge section typo",
			yaml:    "judge:\n  vote: 3\n",
			wantKey: "vote",
		},
		{
			name:    "tempo section typo",
			yaml:    "tempo:\n  endpont: http://localhost:3200\n",
			wantKey: "endpont",
		},
		{
			name:    "target typo",
			yaml:    "targets:\n  x:\n    adaptor: shell\n",
			wantKey: "adaptor",
		},
		{
			name:    "target http section typo",
			yaml:    "targets:\n  x:\n    adapter: http\n    http:\n      url: http://localhost:8080\n      mehtod: POST\n",
			wantKey: "mehtod",
		},
		{
			name:    "pricing wrong-case key (yaml.v3 is case-sensitive on tags)",
			yaml:    "pricing:\n  m: { inputPerMtok: 1.0, outputPerMTok: 1.0 }\n",
			wantKey: "inputPerMtok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected error for unknown key %q, got nil", tt.wantKey)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("error %q does not name the offending key %q", err.Error(), tt.wantKey)
			}
		})
	}
}

// TestLoadValidConfigUnchangedUnderStrictDecode is the regression guard for the
// strict decode: a representative config that mirrors the real mentat.yaml (all
// documented sections, a shell target with a per-target run_timeout override, and
// an http target with headers) must still load and resolve its values unchanged.
func TestLoadValidConfigUnchangedUnderStrictDecode(t *testing.T) {
	t.Parallel()
	data := []byte(`
tempo: { endpoint: "http://localhost:3200" }
otlpEndpoint: "http://localhost:4318"
poll: { interval: "200ms", timeout: "30s", stableFor: 3, searchLimit: 100 }
run_timeout: 5m
kill_grace: 7s
targets:
  research-agent:
    adapter: shell
    command: ["go", "run", "./cmd"]
    run_timeout: 2s
  checkout:
    adapter: http
    max_concurrency: 8
    http:
      url: "http://localhost:8080/orders"
      method: POST
      headers:
        Content-Type: application/json
`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load valid config: %v", err)
	}
	if cfg.Tempo.Endpoint != "http://localhost:3200" {
		t.Errorf("Tempo.Endpoint = %q, want %q", cfg.Tempo.Endpoint, "http://localhost:3200")
	}
	if cfg.Poll.SearchLimit != 100 {
		t.Errorf("Poll.SearchLimit = %d, want 100", cfg.Poll.SearchLimit)
	}
	shell := cfg.Targets["research-agent"]
	if shell.Adapter != "shell" {
		t.Errorf("research-agent Adapter = %q, want shell", shell.Adapter)
	}
	wantShellBudget := RunBudget{Timeout: 2 * time.Second, Unbounded: false, KillGrace: 7 * time.Second}
	if shell.Budget != wantShellBudget {
		t.Errorf("research-agent Budget = %+v, want %+v", shell.Budget, wantShellBudget)
	}
	http := cfg.Targets["checkout"]
	if http.Adapter != "http" {
		t.Errorf("checkout Adapter = %q, want http", http.Adapter)
	}
	if http.HTTP.Headers["Content-Type"] != "application/json" {
		t.Errorf("checkout Content-Type header = %q, want application/json", http.HTTP.Headers["Content-Type"])
	}
}

// TestLoadAbsentOptionalKeysApplyDefaults proves absence is not an unknown key: a
// minimal config, and an empty document, both load and receive documented defaults
// (store->tempo, poll.searchLimit->100, judge defaults, and a resolved budget).
func TestLoadAbsentOptionalKeysApplyDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{name: "minimal shell target", yaml: "targets:\n  a: { adapter: shell, command: [\"true\"] }\n"},
		{name: "empty document", yaml: ""},
		{name: "comment-only document", yaml: "# nothing here\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Store != "tempo" {
				t.Errorf("Store = %q, want tempo (default)", cfg.Store)
			}
			if cfg.Poll.SearchLimit != 100 {
				t.Errorf("Poll.SearchLimit = %d, want 100 (default)", cfg.Poll.SearchLimit)
			}
			if cfg.Judge.Backend != "claude" || cfg.Judge.Model != "claude-haiku-4-5" || cfg.Judge.Votes != 1 {
				t.Errorf("Judge = %+v, want defaults {claude claude-haiku-4-5 1 0}", cfg.Judge)
			}
			wantSuite := RunBudget{Timeout: DefaultRunTimeout, Unbounded: false, KillGrace: DefaultKillGrace}
			if cfg.Budget != wantSuite {
				t.Errorf("suite Budget = %+v, want %+v (defaults)", cfg.Budget, wantSuite)
			}
		})
	}
}

// TestLoadRepoMentatYAML is the CRITICAL regression guard: the repo's own
// mentat.yaml must still parse under strict decode. If a real key drifts from the
// structs, this fails loudly here instead of at `mentat run`.
func TestLoadRepoMentatYAML(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "mentat.yaml"))
	if err != nil {
		t.Skipf("mentat.yaml not found (%v); skipping repo-config regression guard", err)
	}
	if _, err := Load(data); err != nil {
		t.Fatalf("repo mentat.yaml must parse under strict decode: %v", err)
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

// --- Feature 008: per-target completeness block -----------------------------

// TestLoadCompletenessMode pins the per-target completeness.mode config contract
// (feature 008, data-model config.Target additive): an omitted block or an empty
// mode resolves to "settle" (the default), "strict" is accepted verbatim, and any
// other value is a hard load error naming the target and the offending value
// (contracts §4, Constitution IV — no silent fallback to a default mode).
func TestLoadCompletenessMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		yaml     string
		wantMode string
		wantErr  bool
		errExact string // exact error when wantErr
	}{
		{
			name:     "omitted completeness block defaults to settle",
			yaml:     "targets:\n  a: { adapter: shell, command: [\"true\"] }\n",
			wantMode: "settle",
		},
		{
			name:     "block present but mode omitted defaults to settle",
			yaml:     "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness: { settle: 2s }\n",
			wantMode: "settle",
		},
		{
			name:     "explicit settle mode preserved",
			yaml:     "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness: { mode: settle }\n",
			wantMode: "settle",
		},
		{
			name:     "explicit strict mode preserved",
			yaml:     "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness: { mode: strict }\n",
			wantMode: "strict",
		},
		{
			name:     "unknown mode is a hard error naming target and value",
			yaml:     "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness: { mode: eventual }\n",
			wantErr:  true,
			errExact: `target "a": completeness.mode must be "settle" or "strict", got "eventual"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				if err.Error() != tt.errExact {
					t.Fatalf("error = %q, want exactly %q", err.Error(), tt.errExact)
				}
				return
			}
			if got := cfg.Targets["a"].Completeness.Mode; got != tt.wantMode {
				t.Fatalf("Completeness.Mode = %q, want %q", got, tt.wantMode)
			}
		})
	}
}

// TestLoadCompletenessSettle pins the completeness.settle duration contract
// (feature 008, contracts §4): an explicit Go-duration string resolves to that
// window, zero is allowed, an unparsable value is a hard load error wrapping the
// parse error, and a negative value is a hard load error naming the target and the
// value (Constitution IV — no silent fallback to a default window).
func TestLoadCompletenessSettle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		settle     string // the completeness.settle YAML value
		wantSettle time.Duration
		wantErr    bool
		errSubs    []string // all must appear when wantErr
	}{
		{name: "explicit 3s resolves", settle: "3s", wantSettle: 3 * time.Second},
		{name: "explicit 750ms resolves", settle: "750ms", wantSettle: 750 * time.Millisecond},
		{name: "zero is allowed (no error)", settle: "0s", wantSettle: 0},
		{
			name:    "unparsable value wraps the parse error naming the target",
			settle:  "banana",
			wantErr: true,
			errSubs: []string{`target "a": completeness.settle:`, "banana"},
		},
		{
			name:    "negative value is rejected naming target and value",
			settle:  "-5s",
			wantErr: true,
			errSubs: []string{`target "a": completeness.settle: must be >= 0, got -5s`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			yaml := "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness:\n      mode: settle\n      settle: " + tt.settle + "\n"
			cfg, err := Load([]byte(yaml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range tt.errSubs {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not contain %q", err.Error(), sub)
					}
				}
				return
			}
			if got := cfg.Targets["a"].Completeness.Settle; got != tt.wantSettle {
				t.Fatalf("Completeness.Settle = %v, want %v", got, tt.wantSettle)
			}
		})
	}
}

// TestLoadCompletenessKindDefaults pins the adapter kind-default settle window
// (feature 008, contracts §1): when the completeness block omits settle, the shell
// (spawned) adapter resolves to 2s and http (request-scoped) to 5s. An explicit
// settle overrides the kind-default, and the default applies even in strict mode.
// mcp/grpc are a documented forward-mapping only — no driver implements them — so
// their defaults are intentionally NOT asserted here (no speculative surface).
func TestLoadCompletenessKindDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		yaml       string
		wantSettle time.Duration
	}{
		{
			name:       "shell with no completeness block defaults to 2s",
			yaml:       "targets:\n  a: { adapter: shell, command: [\"true\"] }\n",
			wantSettle: 2 * time.Second,
		},
		{
			name:       "http with no completeness block defaults to 5s",
			yaml:       "targets:\n  a:\n    adapter: http\n    http: { url: \"http://localhost:8080\", method: GET }\n",
			wantSettle: 5 * time.Second,
		},
		{
			name:       "shell strict mode with settle omitted still gets the 2s default",
			yaml:       "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness: { mode: strict }\n",
			wantSettle: 2 * time.Second,
		},
		{
			name:       "explicit settle overrides the shell kind-default",
			yaml:       "targets:\n  a:\n    adapter: shell\n    command: [\"true\"]\n    completeness: { settle: 1s }\n",
			wantSettle: 1 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Load([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Targets["a"].Completeness.Settle; got != tt.wantSettle {
				t.Fatalf("Completeness.Settle = %v, want %v", got, tt.wantSettle)
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

// TestResolveAppliesCompletenessKindDefaultOnCodePath is the first code-path
// (non-YAML) resolution test: a Config built as a struct literal in Go must get
// the same completeness kind-defaults Load applies to a YAML fixture — shell's
// 2s settle window and the "settle" mode (FR-008, contracts/config-resolve.md
// inventory #10).
func TestResolveAppliesCompletenessKindDefaultOnCodePath(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Targets: map[string]Target{
			"agent": {Adapter: "shell", Command: []string{"echo", "hi"}},
		},
	}
	if err := Resolve(&cfg); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := cfg.Targets["agent"].Completeness
	if got.Mode != "settle" {
		t.Fatalf("Completeness.Mode = %q, want %q", got.Mode, "settle")
	}
	if got.Settle != 2*time.Second {
		t.Fatalf("Completeness.Settle = %s, want %s", got.Settle, 2*time.Second)
	}
}

// TestResolveIsIdempotent pins Law 1 of contracts/config-resolve.md: for any c
// where Resolve succeeds, a second Resolve leaves c deep-equal and returns nil.
// This is not academic — the CLI path re-enters Resolve (Load, then mentat.Run)
// with an already-resolved Config, so a default that re-applied itself over an
// already-resolved twin field would silently change the effective contract on
// the second pass. Each row builds a FRESH Config (Targets is a map: a struct
// copy would share it and both passes would mutate the same targets).
func TestResolveIsIdempotent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  func() Config
	}{
		{
			name: "shell target taking every default",
			cfg: func() Config {
				return Config{Targets: map[string]Target{"a": {Adapter: "shell"}}}
			},
		},
		{
			name: "explicit completeness twin",
			cfg: func() Config {
				return Config{Targets: map[string]Target{
					"a": {Adapter: "shell", Completeness: Completeness{Mode: "strict", SettleRaw: "3s"}},
				}}
			},
		},
		{
			name: "unbounded suite budget with per-target override",
			cfg: func() Config {
				return Config{
					RunTimeout: unboundedValue,
					KillGrace:  "4s",
					Targets: map[string]Target{
						"a": {Adapter: "shell"},
						"b": {Adapter: "shell", RunTimeout: "90s"},
					},
				}
			},
		},
		{
			name: "compiled extract pattern",
			cfg: func() Config {
				return Config{Targets: map[string]Target{
					"a": {Adapter: "shell", Extract: ExtractConfig{Mode: core.ExtractPattern, Pattern: `answer: (\w+)`}},
				}}
			},
		},
		{
			// The explicit-twin paths are where re-entry bugs live: pass two sees a
			// Config whose resolved fields are now non-zero and must treat them as the
			// caller's own explicit values without re-deriving or conflicting.
			name: "explicit resolved twins written in code",
			cfg: func() Config {
				return Config{
					Budget: RunBudget{Timeout: 42 * time.Second, KillGrace: 3 * time.Second},
					Targets: map[string]Target{
						"a": {Adapter: "shell", Budget: RunBudget{Timeout: 30 * time.Second}},
						"b": {Adapter: "shell", Budget: RunBudget{Unbounded: true}},
						"c": {Adapter: "shell", Completeness: Completeness{Settle: 7 * time.Second}},
					},
				}
			},
		},
		{
			name: "http target with trimmed url and full judge block",
			cfg: func() Config {
				return Config{
					Targets: map[string]Target{
						"a": {Adapter: "http", HTTP: HTTP{URL: "  http://localhost:8080  ", Method: " POST "}},
					},
					Judge:   JudgeConfig{Votes: 3, Temperature: 0.7, MaxCostUSD: 1.5},
					Pricing: Pricing{"m": {InputPerMTok: 1, OutputPerMTok: 2}},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			once := tt.cfg()
			if err := Resolve(&once); err != nil {
				t.Fatalf("first Resolve on a fresh config: %v", err)
			}
			twice := tt.cfg()
			if err := Resolve(&twice); err != nil {
				t.Fatalf("first Resolve: %v", err)
			}
			if err := Resolve(&twice); err != nil {
				t.Fatalf("second Resolve must be a no-op returning nil, got: %v", err)
			}
			if !reflect.DeepEqual(once, twice) {
				t.Fatalf("Resolve is not idempotent:\n once  = %+v\n twice = %+v", once, twice)
			}
		})
	}
}

// TestResolveCompletenessExplicitValueWins pins Law 2 + the first half of Law 3
// of contracts/config-resolve.md for the Completeness raw/resolved twin. On the
// YAML path only SettleRaw can ever be set, so "empty raw" unambiguously meant
// "omitted" and taking the kind default was correct. On the code path a caller
// writes the RESOLVED field (Completeness{Settle: 7 * time.Second}) and leaves
// SettleRaw empty — clobbering that with the kind default would silently discard
// an explicit choice. A zero Settle with an empty raw is still indistinguishable
// from "unset" and takes the default; a caller wanting a genuine zero window
// writes SettleRaw: "0s", which is explicit.
func TestResolveCompletenessExplicitValueWins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		adapter    string
		in         Completeness
		wantMode   string
		wantSettle time.Duration
	}{
		{
			name:       "explicit resolved settle survives the kind default",
			adapter:    "shell",
			in:         Completeness{Settle: 7 * time.Second},
			wantMode:   "settle",
			wantSettle: 7 * time.Second,
		},
		{
			name:       "explicit resolved settle survives on an http target too",
			adapter:    "http",
			in:         Completeness{Mode: "strict", Settle: 250 * time.Millisecond},
			wantMode:   "strict",
			wantSettle: 250 * time.Millisecond,
		},
		{
			name:       "explicit resolved settle survives an adapter with no kind default",
			adapter:    "telepathy",
			in:         Completeness{Settle: 7 * time.Second},
			wantMode:   "settle",
			wantSettle: 7 * time.Second,
		},
		{
			name:       "zero settle with empty raw still takes the kind default",
			adapter:    "shell",
			in:         Completeness{},
			wantMode:   "settle",
			wantSettle: 2 * time.Second,
		},
		{
			name:       "non-empty raw wins over a zero resolved twin",
			adapter:    "shell",
			in:         Completeness{SettleRaw: "1500ms"},
			wantMode:   "settle",
			wantSettle: 1500 * time.Millisecond,
		},
		{
			name:       "explicit zero window is spelled in raw, not by leaving Settle zero",
			adapter:    "shell",
			in:         Completeness{SettleRaw: "0s"},
			wantMode:   "settle",
			wantSettle: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := Target{Adapter: tt.adapter, Completeness: tt.in}
			if tt.adapter == "http" {
				// url/method are unconditionally required for http (inventory #7); supply
				// them so this row exercises completeness rather than that guard.
				target.HTTP = HTTP{URL: "http://localhost:8080", Method: "POST"}
			}
			cfg := Config{Targets: map[string]Target{"a": target}}
			if err := Resolve(&cfg); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			got := cfg.Targets["a"].Completeness
			if got.Mode != tt.wantMode {
				t.Fatalf("Mode = %q, want %q", got.Mode, tt.wantMode)
			}
			if got.Settle != tt.wantSettle {
				t.Fatalf("Settle = %s, want %s", got.Settle, tt.wantSettle)
			}
		})
	}
}

// TestResolveCompletenessTwinConflict pins the second half of Law 3: when BOTH
// halves of the raw/resolved twin are set and they disagree, Resolve cannot know
// which the caller meant. Constitution IV forbids guessing, so this is a hard
// error naming BOTH fields and BOTH values rather than a silent
// last-writer-wins. Agreement (raw parses to exactly the resolved value) is NOT
// a conflict — that is the state a resolved config is already in, and rejecting
// it would break idempotency (Law 1).
func TestResolveCompletenessTwinConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		in         Completeness
		wantErr    bool
		wantErrSub []string
		wantSettle time.Duration
	}{
		{
			name:       "raw and resolved disagree is a hard error naming both",
			in:         Completeness{SettleRaw: "3s", Settle: 9 * time.Second},
			wantErr:    true,
			wantErrSub: []string{`"a"`, "completeness.settle", "3s", "Completeness.Settle", "9s"},
		},
		{
			name:       "raw zero against a non-zero resolved twin is still a conflict",
			in:         Completeness{SettleRaw: "0s", Settle: 5 * time.Second},
			wantErr:    true,
			wantErrSub: []string{"completeness.settle", "0s", "Completeness.Settle", "5s"},
		},
		{
			name:       "agreement is not a conflict",
			in:         Completeness{SettleRaw: "3s", Settle: 3 * time.Second},
			wantSettle: 3 * time.Second,
		},
		{
			name:       "agreement through a differently-spelled duration is not a conflict",
			in:         Completeness{SettleRaw: "3000ms", Settle: 3 * time.Second},
			wantSettle: 3 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{Targets: map[string]Target{"a": {Adapter: "shell", Completeness: tt.in}}}
			err := Resolve(&cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range tt.wantErrSub {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not name %q", err.Error(), sub)
					}
				}
				return
			}
			if got := cfg.Targets["a"].Completeness.Settle; got != tt.wantSettle {
				t.Fatalf("Settle = %s, want %s", got, tt.wantSettle)
			}
		})
	}
}

// TestResolveBudgetLaws applies Laws 2 and 3 to the OTHER raw/resolved twin:
// RunTimeout/KillGrace (raw strings) beside Budget (resolved RunBudget), at both
// suite and target level. This twin matters more than completeness because a zero
// RunBudget is not a harmless default on the code path — engine.go:272 only arms
// a deadline `if !budget.Unbounded && budget.Timeout > 0`, and shell.go:87 only
// sets cmd.WaitDelay `if spec.KillGrace > 0`. So a hand-built Config that skips
// resolution runs UNBOUNDED with no kill escalation, while the identical YAML
// gets 5m/10s. Unbounded must therefore stay explicit (RunBudget's own contract:
// there is no magic zero Timeout meaning forever), and a zero KillGrace on an
// otherwise-explicit budget takes the suite value rather than disarming the reap.
func TestResolveBudgetLaws(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		suiteRunTimeout  string
		suiteKillGrace   string
		suiteBudget      RunBudget
		targetRunTimeout string
		targetBudget     RunBudget
		wantTargetBudget RunBudget
		wantErr          bool
		wantErrSub       []string
	}{
		{
			name:             "zero budget with empty raw takes the suite default",
			wantTargetBudget: RunBudget{Timeout: DefaultRunTimeout, KillGrace: DefaultKillGrace},
		},
		{
			name:             "raw run_timeout wins over a zero budget",
			targetRunTimeout: "90s",
			wantTargetBudget: RunBudget{Timeout: 90 * time.Second, KillGrace: DefaultKillGrace},
		},
		{
			name:             "explicit target budget survives the suite default",
			targetBudget:     RunBudget{Timeout: 30 * time.Second},
			wantTargetBudget: RunBudget{Timeout: 30 * time.Second, KillGrace: DefaultKillGrace},
		},
		{
			name:             "explicit suite budget is inherited by a plain target",
			suiteBudget:      RunBudget{Timeout: 42 * time.Second, KillGrace: 3 * time.Second},
			wantTargetBudget: RunBudget{Timeout: 42 * time.Second, KillGrace: 3 * time.Second},
		},
		{
			name:             "explicit unbounded budget in code is preserved",
			targetBudget:     RunBudget{Unbounded: true},
			wantTargetBudget: RunBudget{Unbounded: true, KillGrace: DefaultKillGrace},
		},
		{
			name:             "explicit kill grace on the target budget survives",
			targetBudget:     RunBudget{Timeout: 30 * time.Second, KillGrace: time.Second},
			wantTargetBudget: RunBudget{Timeout: 30 * time.Second, KillGrace: time.Second},
		},
		{
			name:             "agreement between raw and explicit budget is not a conflict",
			targetRunTimeout: "90s",
			targetBudget:     RunBudget{Timeout: 90 * time.Second},
			wantTargetBudget: RunBudget{Timeout: 90 * time.Second, KillGrace: DefaultKillGrace},
		},
		{
			name:             "unbounded raw agreeing with an unbounded budget is not a conflict",
			targetRunTimeout: unboundedValue,
			targetBudget:     RunBudget{Unbounded: true},
			wantTargetBudget: RunBudget{Unbounded: true, KillGrace: DefaultKillGrace},
		},
		{
			name:             "target raw conflicting with an explicit target budget is a hard error",
			targetRunTimeout: "90s",
			targetBudget:     RunBudget{Timeout: 30 * time.Second},
			wantErr:          true,
			wantErrSub:       []string{`"a"`, "run_timeout", "90s", "Budget.Timeout", "30s"},
		},
		{
			name:             "raw unbounded conflicting with a bounded explicit budget is a hard error",
			targetRunTimeout: unboundedValue,
			targetBudget:     RunBudget{Timeout: 30 * time.Second},
			wantErr:          true,
			wantErrSub:       []string{"run_timeout", unboundedValue, "Budget"},
		},
		{
			name:            "suite raw conflicting with an explicit suite budget is a hard error",
			suiteRunTimeout: "1m",
			suiteBudget:     RunBudget{Timeout: 2 * time.Minute},
			wantErr:         true,
			wantErrSub:      []string{"run_timeout", "1m", "Budget.Timeout", "2m"},
		},
		{
			name:           "suite kill_grace conflicting with an explicit budget kill grace is a hard error",
			suiteKillGrace: "5s",
			suiteBudget:    RunBudget{KillGrace: 9 * time.Second},
			wantErr:        true,
			wantErrSub:     []string{"kill_grace", "5s", "Budget.KillGrace", "9s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{
				RunTimeout: tt.suiteRunTimeout,
				KillGrace:  tt.suiteKillGrace,
				Budget:     tt.suiteBudget,
				Targets: map[string]Target{
					"a": {Adapter: "shell", RunTimeout: tt.targetRunTimeout, Budget: tt.targetBudget},
				},
			}
			err := Resolve(&cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				for _, sub := range tt.wantErrSub {
					if !strings.Contains(err.Error(), sub) {
						t.Fatalf("error %q does not name %q", err.Error(), sub)
					}
				}
				return
			}
			if got := cfg.Targets["a"].Budget; got != tt.wantTargetBudget {
				t.Fatalf("target budget = %+v, want %+v", got, tt.wantTargetBudget)
			}
		})
	}
}

// effectiveCompleteness / effectiveTarget / effectiveConfig project a resolved
// Config down to what the ENGINE actually consumes. The projection is the whole
// point of the parity table: a YAML fixture necessarily sets the raw string
// fields (run_timeout: "90s"), while idiomatic Go sets the resolved twin
// (Budget{Timeout: 90 * time.Second}). Deep-equalling whole Configs would force
// every code row to restate the YAML strings, which would prove only that
// yaml.Decode works — not that a library caller writing normal Go gets the same
// behaviour. Comparing the projection is the claim FR-008..FR-010 actually make.
type effectiveCompleteness struct {
	Mode   string
	Settle time.Duration
}

type effectiveTarget struct {
	Adapter        string
	MaxConcurrency int
	HTTP           HTTP
	Budget         RunBudget
	Completeness   effectiveCompleteness
	Policy         core.ExtractPolicy
}

type effectiveConfig struct {
	Store        string
	StorePath    string
	Expectations string
	SearchLimit  int
	Budget       RunBudget
	Judge        JudgeConfig
	Pricing      Pricing
	Targets      map[string]effectiveTarget
}

func effective(c Config) effectiveConfig {
	// Always build the map so a config with no targets projects to an empty
	// non-nil map on both paths (Load leaves a nil map; a struct literal may too).
	targets := make(map[string]effectiveTarget, len(c.Targets))
	for name, t := range c.Targets {
		targets[name] = effectiveTarget{
			Adapter:        t.Adapter,
			MaxConcurrency: t.MaxConcurrency,
			HTTP:           t.HTTP,
			Budget:         t.Budget,
			Completeness:   effectiveCompleteness{Mode: t.Completeness.Mode, Settle: t.Completeness.Settle},
			Policy:         t.Extract.Policy(),
		}
	}
	return effectiveConfig{
		Store:        c.Store,
		StorePath:    c.StorePath,
		Expectations: c.Expectations,
		SearchLimit:  c.Poll.SearchLimit,
		Budget:       c.Budget,
		Judge:        c.Judge,
		Pricing:      c.Pricing,
		Targets:      targets,
	}
}

// TestConfigPathParity is the proof obligation of contracts/config-resolve.md:
// ONE ROW PER BEHAVIOUR in the 13-entry inventory, each expressing the same
// logical configuration BOTH ways — a YAML fixture through Load and a struct
// literal through Resolve — asserting either deep-equal effective contracts or
// identical error text. Identical TEXT, not merely both-non-nil: a code-path
// caller must get the same diagnostic a YAML author gets, or the error is a
// different error (Law 4).
func TestConfigPathParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		yaml       string
		code       func() Config
		wantErr    bool
		wantErrSub []string
		assert     func(t *testing.T, c Config)
	}{
		{
			// inventory #1 — store defaults to "tempo"
			name: "#1 store default",
			yaml: "tempo:\n  endpoint: http://localhost:3200\n",
			code: func() Config {
				return Config{Tempo: Endpoint{Endpoint: "http://localhost:3200"}}
			},
			assert: func(t *testing.T, c Config) {
				if c.Store != "tempo" {
					t.Fatalf("Store = %q, want %q", c.Store, "tempo")
				}
			},
		},
		{
			// inventory #2 — file store requires storePath (hard error)
			name:       "#2 file store without storePath",
			yaml:       "store: file\n",
			code:       func() Config { return Config{Store: "file"} },
			wantErr:    true,
			wantErrSub: []string{"storePath is required", `"file"`},
		},
		{
			// inventory #3 — expectations directory default
			name: "#3 expectations default",
			yaml: "store: tempo\n",
			code: func() Config { return Config{Store: "tempo"} },
			assert: func(t *testing.T, c Config) {
				if c.Expectations != "expectations" {
					t.Fatalf("Expectations = %q, want %q", c.Expectations, "expectations")
				}
			},
		},
		{
			// inventory #4 — poll.searchLimit defaults to 100
			name: "#4 search limit default",
			yaml: "poll:\n  interval: 200ms\n  stableFor: 3\n",
			code: func() Config {
				return Config{Poll: PollSpec{Interval: "200ms", StableFor: 3}}
			},
			assert: func(t *testing.T, c Config) {
				if c.Poll.SearchLimit != 100 {
					t.Fatalf("SearchLimit = %d, want 100", c.Poll.SearchLimit)
				}
			},
		},
		{
			// inventory #5 — kill_grace + suite run_timeout resolve into Budget. The
			// code row writes the RESOLVED twin, which is the idiomatic Go spelling.
			name: "#5 suite budget resolution",
			yaml: "run_timeout: 90s\nkill_grace: 4s\n",
			code: func() Config {
				return Config{Budget: RunBudget{Timeout: 90 * time.Second, KillGrace: 4 * time.Second}}
			},
			assert: func(t *testing.T, c Config) {
				want := RunBudget{Timeout: 90 * time.Second, KillGrace: 4 * time.Second}
				if c.Budget != want {
					t.Fatalf("Budget = %+v, want %+v", c.Budget, want)
				}
			},
		},
		{
			// inventory #6 — per-adapter concurrency defaults (shell 1, http 8)
			name: "#6 per-target concurrency defaults",
			yaml: `targets:
  sh: { adapter: shell, command: ["true"] }
  api: { adapter: http, http: { url: "http://localhost:8080", method: GET } }
`,
			code: func() Config {
				return Config{Targets: map[string]Target{
					"sh":  {Adapter: "shell", Command: []string{"true"}},
					"api": {Adapter: "http", HTTP: HTTP{URL: "http://localhost:8080", Method: "GET"}},
				}}
			},
			assert: func(t *testing.T, c Config) {
				if got := c.Targets["sh"].MaxConcurrency; got != 1 {
					t.Fatalf("shell MaxConcurrency = %d, want 1", got)
				}
				if got := c.Targets["api"].MaxConcurrency; got != 8 {
					t.Fatalf("http MaxConcurrency = %d, want 8", got)
				}
			},
		},
		{
			// inventory #7a — http url/method are required (hard error)
			name: "#7a http url required",
			yaml: `targets:
  api: { adapter: http, http: { method: GET } }
`,
			code: func() Config {
				return Config{Targets: map[string]Target{
					"api": {Adapter: "http", HTTP: HTTP{Method: "GET"}},
				}}
			},
			wantErr:    true,
			wantErrSub: []string{`"api"`, "http.url is required"},
		},
		{
			// inventory #7b — url/method are trimmed (normalize)
			name: "#7b http url and method are trimmed",
			yaml: `targets:
  api: { adapter: http, http: { url: "  http://localhost:8080  ", method: "  GET  " } }
`,
			code: func() Config {
				return Config{Targets: map[string]Target{
					"api": {Adapter: "http", HTTP: HTTP{URL: "  http://localhost:8080  ", Method: "  GET  "}},
				}}
			},
			assert: func(t *testing.T, c Config) {
				got := c.Targets["api"].HTTP
				if got.URL != "http://localhost:8080" || got.Method != "GET" {
					t.Fatalf("HTTP = %+v, want trimmed url/method", got)
				}
			},
		},
		{
			// inventory #8 — per-target Budget resolution, written as the resolved
			// twin on the code path.
			name: "#8 target budget resolution",
			yaml: `kill_grace: 4s
targets:
  a: { adapter: shell, run_timeout: 90s }
`,
			code: func() Config {
				return Config{
					Budget: RunBudget{Timeout: DefaultRunTimeout, KillGrace: 4 * time.Second},
					Targets: map[string]Target{
						"a": {Adapter: "shell", Budget: RunBudget{Timeout: 90 * time.Second}},
					},
				}
			},
			assert: func(t *testing.T, c Config) {
				want := RunBudget{Timeout: 90 * time.Second, KillGrace: 4 * time.Second}
				if got := c.Targets["a"].Budget; got != want {
					t.Fatalf("target budget = %+v, want %+v", got, want)
				}
			},
		},
		{
			// inventory #9 — extract validates AND precompiles; Policy() must carry a
			// non-nil compiled pattern on BOTH paths, or the code path would recompile
			// (or nil-pointer) at drive time.
			name: "#9 extract pattern compiles on both paths",
			yaml: `targets:
  a:
    adapter: shell
    extract: { mode: pattern, pattern: "answer: (\\w+)" }
`,
			code: func() Config {
				return Config{Targets: map[string]Target{
					"a": {Adapter: "shell", Extract: ExtractConfig{Mode: core.ExtractPattern, Pattern: `answer: (\w+)`}},
				}}
			},
			assert: func(t *testing.T, c Config) {
				p := c.Targets["a"].Extract.Policy()
				if p.Pattern == nil {
					t.Fatal("ExtractConfig.Policy().Pattern is nil: the pattern was not precompiled")
				}
				if got := p.Pattern.FindStringSubmatch("answer: yes"); len(got) != 2 || got[1] != "yes" {
					t.Fatalf("compiled pattern did not extract: %v", got)
				}
			},
		},
		{
			// inventory #10 — completeness kind-defaults (shell 2s / http 5s)
			name: "#10 completeness kind defaults",
			yaml: `targets:
  sh: { adapter: shell }
  api: { adapter: http, http: { url: "http://localhost:8080", method: GET } }
`,
			code: func() Config {
				return Config{Targets: map[string]Target{
					"sh":  {Adapter: "shell"},
					"api": {Adapter: "http", HTTP: HTTP{URL: "http://localhost:8080", Method: "GET"}},
				}}
			},
			assert: func(t *testing.T, c Config) {
				if got := c.Targets["sh"].Completeness; got.Mode != "settle" || got.Settle != 2*time.Second {
					t.Fatalf("shell completeness = %+v, want settle/2s", got)
				}
				if got := c.Targets["api"].Completeness; got.Mode != "settle" || got.Settle != 5*time.Second {
					t.Fatalf("http completeness = %+v, want settle/5s", got)
				}
			},
		},
		{
			// inventory #11 — validatePricing (hard error)
			name: "#11 negative pricing rate",
			yaml: "pricing:\n  gpt: { inputPerMTok: -1, outputPerMTok: 2 }\n",
			code: func() Config {
				return Config{Pricing: Pricing{"gpt": {InputPerMTok: -1, OutputPerMTok: 2}}}
			},
			wantErr:    true,
			wantErrSub: []string{`"gpt"`, "inputPerMTok", "-1"},
		},
		{
			// inventory #12 — judge backend/model/votes defaults
			name: "#12 judge defaults",
			yaml: "store: tempo\n",
			code: func() Config { return Config{Store: "tempo"} },
			assert: func(t *testing.T, c Config) {
				want := JudgeConfig{Backend: "claude", Model: DefaultJudgeModel, Votes: 1}
				if c.Judge != want {
					t.Fatalf("Judge = %+v, want %+v", c.Judge, want)
				}
			},
		},
		{
			// inventory #13 — validateJudge even-vote rule (hard error)
			name:       "#13 even judge votes",
			yaml:       "judge:\n  votes: 2\n  temperature: 0.7\n",
			code:       func() Config { return Config{Judge: JudgeConfig{Votes: 2, Temperature: 0.7}} },
			wantErr:    true,
			wantErrSub: []string{"judge.votes must be odd", "2"},
		},
		{
			// T012(a) divergence suspect: zero Target.Budget semantics on the code path.
			// VERDICT: confirmed-divergence-fixed.
			// EVIDENCE (read, not assumed): engine.go:272 arms the per-run deadline only
			// `if !budget.Unbounded && budget.Timeout > 0`, and driver/shell.go:87 sets
			// cmd.WaitDelay only `if spec.KillGrace > 0`. So a hand-built Config handed
			// straight to the engine — zero RunBudget — ran with NO deadline and NO kill
			// escalation, while byte-identical YAML got 5m/10s. engine.go:240-241 even
			// documents the hole ("a zero-value budget from a hand-built config").
			// Resolve closes it: this row asserts not just equality but the two
			// PROPERTIES those guards test, so the row fails if either regresses to zero.
			name: "T012a zero budget on the code path gets the same bound as YAML",
			yaml: `targets:
  a: { adapter: shell }
`,
			code: func() Config {
				return Config{Targets: map[string]Target{"a": {Adapter: "shell"}}}
			},
			assert: func(t *testing.T, c Config) {
				b := c.Targets["a"].Budget
				if b.Timeout <= 0 || b.Unbounded {
					t.Fatalf("budget = %+v: engine.go:272 would arm no deadline at all", b)
				}
				if b.KillGrace <= 0 {
					t.Fatalf("budget = %+v: shell.go:87 would set no WaitDelay, so a signal-ignoring child is never reaped", b)
				}
				want := RunBudget{Timeout: DefaultRunTimeout, KillGrace: DefaultKillGrace}
				if b != want {
					t.Fatalf("budget = %+v, want %+v", b, want)
				}
			},
		},
		{
			// T012(b) divergence suspect: validateJudge's Load-only rules.
			// VERDICT: confirmed-divergence-fixed for the temperature-pairing rule.
			// EVIDENCE (read at build.go:98-104): Build's defense-in-depth re-check is
			// ONLY `votes < 1 || votes%2 == 0`. votes=3 with temperature=0 is positive
			// and odd, so it sails through Build untouched — the code path burned 3x the
			// judge cost on near-identical calls with no diagnostic, where YAML failed
			// loudly at load. Routing this rule through Resolve is what closes it.
			name:       "T012b judge votes>1 at temperature 0",
			yaml:       "judge:\n  votes: 3\n",
			code:       func() Config { return Config{Judge: JudgeConfig{Votes: 3}} },
			wantErr:    true,
			wantErrSub: []string{"votes=3", "temperature=0", "raise temperature"},
		},
		{
			// T012(b), second half. VERDICT: confirmed-divergence-fixed.
			// EVIDENCE: build.go re-checks votes only — it never inspects MaxCostUSD, so
			// nothing downstream of a hand-built Config rejected a negative spend
			// ceiling. JudgeConfig.MaxCostUSD's own doc commits to rejecting it "at load
			// rather than silently treated as unlimited (Constitution IV)"; before
			// Resolve that promise simply did not hold for library callers.
			name:       "T012b negative judge max_cost_usd",
			yaml:       "judge:\n  max_cost_usd: -1\n",
			code:       func() Config { return Config{Judge: JudgeConfig{MaxCostUSD: -1}} },
			wantErr:    true,
			wantErrSub: []string{"judge.max_cost_usd must be finite and >= 0", "-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loaded, loadErr := Load([]byte(tt.yaml))
			built := tt.code()
			resolveErr := Resolve(&built)

			if tt.wantErr {
				if loadErr == nil {
					t.Fatalf("Load succeeded but the row expects a hard error")
				}
				if resolveErr == nil {
					t.Fatalf("Resolve succeeded but Load failed with %q: the code path silently accepts a config the YAML path rejects", loadErr)
				}
				if loadErr.Error() != resolveErr.Error() {
					t.Fatalf("error text diverges between paths:\n  Load:    %v\n  Resolve: %v", loadErr, resolveErr)
				}
				for _, sub := range tt.wantErrSub {
					if !strings.Contains(loadErr.Error(), sub) {
						t.Fatalf("error %q does not name %q", loadErr.Error(), sub)
					}
				}
				return
			}

			if loadErr != nil {
				t.Fatalf("Load: %v", loadErr)
			}
			if resolveErr != nil {
				t.Fatalf("Resolve: %v", resolveErr)
			}
			if diff := reflect.DeepEqual(effective(loaded), effective(built)); !diff {
				t.Fatalf("effective contract diverges between paths:\n  YAML: %+v\n  code: %+v", effective(loaded), effective(built))
			}
			if tt.assert != nil {
				// Assert on BOTH resolutions: deep-equality alone would be satisfied by
				// two identically-wrong configs.
				tt.assert(t, loaded)
				tt.assert(t, built)
			}
		})
	}
}
