package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thetonymaster/mentat/internal/core"
)

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
		{
			// T031 — code-path-only rule: kill_grace is suite-wide in YAML, so a
			// per-target kill grace has no raw twin to compare against and cannot be a
			// TestConfigPathParity row. It still needs the same guard: shell.go:87 sets
			// cmd.WaitDelay only `if spec.KillGrace > 0`, and this is the budget the
			// driver actually runs under. Zero still means "inherit the suite value"
			// (asserted by the rows above) — only a negative value is rejected.
			name:         "negative kill grace on the target budget is a hard error",
			targetBudget: RunBudget{Timeout: 30 * time.Second, KillGrace: -2 * time.Second},
			wantErr:      true,
			wantErrSub:   []string{`target "a" kill_grace: must be > 0, got "-2s"`},
		},
		{
			// T031 — Timeout and Unbounded are contradictory halves of one decision and
			// YAML cannot express the pair at all (run_timeout is either a duration or
			// the literal "unbounded"). Resolve used to silently discard the Timeout,
			// which is the one outcome Constitution IV forbids: the caller wrote a bound
			// and got an unbounded run. Every other contradictory pair in this feature is
			// a hard error naming both fields, so this one is too.
			name:         "a bounded timeout together with unbounded is a hard error naming both",
			targetBudget: RunBudget{Timeout: 5 * time.Minute, Unbounded: true},
			wantErr:      true,
			wantErrSub:   []string{`target "a" run_timeout`, "Budget.Unbounded", "Budget.Timeout", "5m0s", "set exactly one of them"},
		},
		{
			// The suite level carries the same contradiction and the same rule.
			name:        "a bounded suite timeout together with unbounded is a hard error",
			suiteBudget: RunBudget{Timeout: 90 * time.Second, Unbounded: true},
			wantErr:     true,
			wantErrSub:  []string{"run_timeout", "Budget.Unbounded", "Budget.Timeout", "1m30s", "set exactly one of them"},
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
		{
			// T031 — Law 4 for the RESOLVED half of the run_timeout twin. The raw half
			// has always been hard-rejected; the resolved half was returned unvalidated,
			// and engine.go:274 arms a deadline only `if !budget.Unbounded &&
			// budget.Timeout > 0`. So a negative Timeout written in code silently
			// produced an UNBOUNDED run — the exact outcome the raw-path guard exists to
			// prevent. Each row pins IDENTICAL error text (the harness compares
			// loadErr.Error() to resolveErr.Error()), not merely both-non-nil.
			name: "T031 negative suite Budget.Timeout",
			yaml: "run_timeout: \"-5m0s\"\n",
			code: func() Config {
				return Config{Budget: RunBudget{Timeout: -5 * time.Minute}}
			},
			wantErr:    true,
			wantErrSub: []string{`run_timeout: must be > 0, got "-5m0s" (use "unbounded" for no limit)`},
		},
		{
			// T031 — same for the kill_grace twin. driver/shell.go:87 sets cmd.WaitDelay
			// only `if spec.KillGrace > 0`, so a negative KillGrace disarms the kill
			// escalation entirely and a signal-ignoring child is never reaped. It also
			// falsifies RunBudget's own documented invariant ("KillGrace is always > 0").
			name: "T031 negative suite Budget.KillGrace",
			yaml: "kill_grace: \"-1s\"\n",
			code: func() Config {
				return Config{Budget: RunBudget{KillGrace: -time.Second}}
			},
			wantErr:    true,
			wantErrSub: []string{`kill_grace: must be > 0, got "-1s"`},
		},
		{
			// T031 — same for the completeness twin. correlate.go:315 applies the settle
			// barrier only `if req.Contract.Settle > 0`, so a negative resolved Settle
			// silently DISARMS feature 008's flush barrier and returns absence verdicts
			// with no soundness guarantee behind them.
			name: "T031 negative target Completeness.Settle",
			yaml: "targets:\n  a: { adapter: shell, completeness: { settle: \"-3s\" } }\n",
			code: func() Config {
				return Config{Targets: map[string]Target{
					"a": {Adapter: "shell", Completeness: Completeness{Settle: -3 * time.Second}},
				}}
			},
			wantErr:    true,
			wantErrSub: []string{`target "a": completeness.settle: must be >= 0, got -3s`},
		},
		{
			// T031 — a per-target Budget carries the same hole as the suite one, and the
			// target budget is the one the driver actually runs under.
			name: "T031 negative target Budget.Timeout",
			yaml: "targets:\n  a: { adapter: shell, run_timeout: \"-1s\" }\n",
			code: func() Config {
				return Config{Targets: map[string]Target{
					"a": {Adapter: "shell", Budget: RunBudget{Timeout: -time.Second}},
				}}
			},
			wantErr:    true,
			wantErrSub: []string{`target "a" run_timeout: must be > 0, got "-1s" (use "unbounded" for no limit)`},
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
