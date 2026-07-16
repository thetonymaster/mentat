package config

import (
	"os"
	"path/filepath"
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
