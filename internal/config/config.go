package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thetonymaster/mentat/internal/core"
)

// Run-lifecycle defaults (feature 003). Generous enough not to break a slow-but-
// healthy agent run, but a hard stop for a runaway. Both are configurable; the
// existence of documented defaults is the requirement, the exact values are
// tunable. Worst-case scenario wall time per run = RunTimeout + KillGrace.
const (
	DefaultRunTimeout = 5 * time.Minute
	DefaultKillGrace  = 10 * time.Second
	// unboundedValue is the explicit opt-out of the run timeout — never the silent
	// default (Constitution IV): a run is bounded unless a human wrote this.
	unboundedValue = "unbounded"
)

// RunBudget is the resolved lifecycle bound for one SUT run: a Timeout (meaningful
// only when !Unbounded) plus the KillGrace between the polite signal and the
// forceful kill. Unbounded is explicit — there is no magic zero Timeout meaning
// "forever" (Constitution IV). KillGrace is always > 0.
type RunBudget struct {
	Timeout   time.Duration
	Unbounded bool
	KillGrace time.Duration
}

type Config struct {
	Store string `yaml:"store"`
	// StorePath is the fixture directory the "file" store replays from (US5). It is
	// REQUIRED when Store == "file" (validated in Load) and ignored otherwise.
	StorePath    string            `yaml:"storePath"`
	Tempo        Endpoint          `yaml:"tempo"`
	OTLPEndpoint string            `yaml:"otlpEndpoint"`
	Poll         PollSpec          `yaml:"poll"`
	Targets      map[string]Target `yaml:"targets"`
	Pricing      Pricing           `yaml:"pricing"`
	Expectations string            `yaml:"expectations"`
	Judge        JudgeConfig       `yaml:"judge"`
	// RunTimeout / KillGrace are the raw suite-level lifecycle knobs. RunTimeout is
	// a Go duration or "unbounded"; KillGrace is a Go duration > 0. Empty → default.
	RunTimeout string `yaml:"run_timeout"`
	KillGrace  string `yaml:"kill_grace"`
	// Budget is the resolved suite-level run budget, populated by Load.
	Budget RunBudget `yaml:"-"`
}

// DefaultJudgeModel is the pinned fast-tier default judge model (US6, judge-ledger
// Defaults policy). Haiku-class: ~80% cheaper per input and output token than the
// former Opus-tier default (SC-006 — Opus 4.8 $5/$25 per MTok vs Haiku 4.5 $1/$5 =
// 80% input and 80% output reduction), and unlike Opus it accepts the temperature
// knob best-of-N voting needs (see internal/judge/claude.go temperatureAcceptingFamilies,
// which matches the "haiku" substring). To upgrade accuracy, set judge.model in config
// — one line, documented in the README.
const DefaultJudgeModel = "claude-haiku-4-5"

// JudgeConfig configures the semantic (LLM-judge) result matcher. The whole block
// is optional — a project that never writes `the result means` never needs it; the
// defaults applied in Load make an omitted block valid.
type JudgeConfig struct {
	Backend string `yaml:"backend"` // default "claude"
	Model   string `yaml:"model"`   // default DefaultJudgeModel (fast-tier haiku)
	Votes   int    `yaml:"votes"`   // default 1; best-of-N majority (odd N required)
	// Temperature is applied only on models that accept it (Sonnet 4.6 / Haiku 4.5);
	// omitted on Opus-tier. Optional knob, default 0.
	Temperature float64 `yaml:"temperature"`
	// MaxCostUSD is the optional post-scenario judge-spend ceiling in USD (US6). Unset
	// or 0 means unlimited — today's behaviour, no budget accounting. When positive,
	// completed judge cost is summed after each scenario and the suite aborts once it
	// is exceeded (judge-ledger budget contract). A negative value is rejected at load
	// rather than silently treated as unlimited (Constitution IV).
	MaxCostUSD float64 `yaml:"max_cost_usd"`
}

type Endpoint struct {
	Endpoint string `yaml:"endpoint"`
}

type PollSpec struct {
	Interval    string `yaml:"interval"`
	Timeout     string `yaml:"timeout"`
	StableFor   int    `yaml:"stableFor"`
	SearchLimit int    `yaml:"searchLimit"`
}

type Target struct {
	Adapter        string   `yaml:"adapter"`
	Command        []string `yaml:"command"`
	MaxConcurrency int      `yaml:"max_concurrency"`
	HTTP           HTTP     `yaml:"http"`
	// RunTimeout is the raw per-target override (Go duration or "unbounded"); empty
	// inherits the suite value. Budget is the resolved effective budget for this
	// target (override → suite → default), populated by Load.
	RunTimeout string    `yaml:"run_timeout"`
	Budget     RunBudget `yaml:"-"`
	// Extract is the target's answer-extraction policy (US8); the block is optional
	// and an absent one means whole (today's TrimSpace behaviour). Validated in Load,
	// which precompiles the pattern once so the compiled regexp rides the policy.
	Extract ExtractConfig `yaml:"extract"`
	// Completeness is the target's optional trace-completeness policy (feature 008);
	// an absent block means settle mode with the adapter kind-default window. Load
	// validates the block and resolves the effective Settle window once at load time.
	Completeness Completeness `yaml:"completeness"`
}

// Completeness is a target's optional trace-completeness policy (feature 008,
// data-model config.Target additive). The whole block is optional: an omitted block
// means settle mode with the adapter's kind-default window. Load validates Mode and
// Settle and resolves the effective window (SettleRaw → Settle) once at load time
// (Constitution IV: no silent fallback — an unknown mode or a bad/negative duration
// is a hard, named load error).
type Completeness struct {
	// Mode is "settle" (the default when empty) or "strict"; any other value is a
	// hard load error. Load normalises an empty Mode to "settle".
	Mode string `yaml:"mode"`
	// SettleRaw is the raw `settle` YAML value, a Go duration string (e.g. "2s"). An
	// empty value applies the adapter kind-default (shell 2s / http 5s). Load parses
	// and validates it into Settle.
	SettleRaw string `yaml:"settle"`
	// Settle is the resolved minimum observation window measured from drive-return,
	// populated by Load (SettleRaw parsed, or the kind-default when omitted). Zero is
	// permitted.
	Settle time.Duration `yaml:"-"`
}

// ExtractConfig is the YAML form of a target's answer-extraction policy (US8,
// config-`extract` row). Mode is whole|marker|pattern (empty → whole). Marker is
// required for marker mode; Pattern is required for pattern mode and must compile
// with at least one capture group. Load validates the block and precompiles the
// pattern into the unexported compiled field so Policy() never recompiles per run.
type ExtractConfig struct {
	Mode    string `yaml:"mode"`
	Marker  string `yaml:"marker"`
	Pattern string `yaml:"pattern"`
	// compiled is the precompiled Pattern regexp, populated by Load for pattern mode
	// (nil for whole/marker). It is the single compile of the pattern for the whole run.
	compiled *regexp.Regexp
}

// Policy converts the validated config into the transport-free core.ExtractPolicy
// the driver applies. The compiled regexp (built once at Load) rides along, so the
// engine never recompiles the pattern per run.
func (e ExtractConfig) Policy() core.ExtractPolicy {
	return core.ExtractPolicy{Mode: e.Mode, Marker: e.Marker, Pattern: e.compiled}
}

// HTTP is the per-target request config used when adapter is "http".
type HTTP struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
}

// ModelRate is the YAML form of a per-model price (USD per million tokens). The
// engine converts config.Pricing to the transport-free core.Pricing so the
// comparator layer keeps importing only core/genai/trace, never config.
type ModelRate struct {
	InputPerMTok  float64 `yaml:"inputPerMTok"`
	OutputPerMTok float64 `yaml:"outputPerMTok"`
}

// Pricing maps a model name to its rate.
type Pricing map[string]ModelRate

// defaultConcurrency holds per-adapter concurrency defaults keyed ONLY to adapters
// that actually have a registered driver (feature 005, D3/FR-005). Adapter existence
// is validated at engine.Build against the driver registry — the single runtime
// source of truth — so this map must not grow into a second, driftable allowlist
// (the pre-005 map listed mcp/grpc that no driver implements). An adapter absent
// here is not rejected at load; it defaults to a conservative concurrency of 1.
var defaultConcurrency = map[string]int{"shell": 1, "http": 8}

// defaultSettle holds the per-adapter completeness settle-window defaults (feature
// 008, contracts §1), keyed ONLY to the adapters a driver actually implements and
// the contract commits a guarantee for: shell (spawned, 2s) and http (request-
// scoped, 5s). mcp/grpc are a documented forward-mapping only — no driver
// implements them — so they carry NO speculative default here (Constitution IV, no
// silent fallback). An adapter absent from this map keeps a zero window when settle
// is omitted (zero is an allowed, explicit value); real adapter existence is
// validated at engine.Build against the driver registry, as with defaultConcurrency.
var defaultSettle = map[string]time.Duration{
	"shell": 2 * time.Second,
	"http":  5 * time.Second,
}

func Load(data []byte) (Config, error) {
	var c Config
	// Strict decode: an unknown key at any nesting level is a hard, named error
	// rather than a silently-ignored typo that falls back to a default and quietly
	// changes verdict semantics (FR-004, Constitution IV). Mirrors the expectations
	// loader. yaml.v3 reports it as `field <name> not found in type config.<Type>`.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF means an empty (or comment-only) document: the old yaml.Unmarshal
	// treated that as a zero-value success, so preserve it here — defaults below
	// still apply. Any other decode error (including an unknown key) is hard.
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if c.Store == "" {
		c.Store = "tempo"
	}
	// The file store (US5) replays fixtures from storePath, so an empty storePath is
	// a hard load error rather than a silent default that would later scan the process
	// working directory (Constitution IV). For any other store storePath is ignored.
	if c.Store == "file" && strings.TrimSpace(c.StorePath) == "" {
		return Config{}, fmt.Errorf("storePath is required when store is %q", "file")
	}
	if c.Expectations == "" {
		c.Expectations = "expectations"
	}
	// A non-positive searchLimit (omitted or <= 0) defaults to 100 so Tempo.Query
	// always sends an explicit, truncation-guardable page size (research R3, A4).
	if c.Poll.SearchLimit <= 0 {
		c.Poll.SearchLimit = 100
	}
	// Resolve the suite-level run budget (feature 003). Defaults 5m / 10s; a
	// per-target run_timeout (resolved in the loop below) overrides the timeout,
	// while kill_grace is suite-wide. A typo or non-positive value fails loudly
	// here rather than becoming a silent default (Constitution IV).
	killGrace, err := resolveKillGrace(c.KillGrace)
	if err != nil {
		return Config{}, err
	}
	suiteTimeout, suiteUnbounded, err := resolveTimeout("run_timeout", c.RunTimeout, DefaultRunTimeout, false)
	if err != nil {
		return Config{}, err
	}
	c.Budget = RunBudget{Timeout: suiteTimeout, Unbounded: suiteUnbounded, KillGrace: killGrace}
	for name, t := range c.Targets {
		def, ok := defaultConcurrency[t.Adapter]
		if !ok {
			def = 1 // unknown-at-load adapter: existence is validated at engine.Build against the driver registry
		}
		if t.MaxConcurrency < 0 {
			return Config{}, fmt.Errorf("target %q: max_concurrency must be >= 0, got %d", name, t.MaxConcurrency)
		}
		if t.MaxConcurrency == 0 {
			t.MaxConcurrency = def
		}
		if t.Adapter == "http" {
			url := strings.TrimSpace(t.HTTP.URL)
			method := strings.TrimSpace(t.HTTP.Method)
			if url == "" {
				return Config{}, fmt.Errorf("target %q: http.url is required when adapter is http", name)
			}
			if method == "" {
				return Config{}, fmt.Errorf("target %q: http.method is required when adapter is http", name)
			}
			t.HTTP.URL = url
			t.HTTP.Method = method
		}
		tt, tu, terr := resolveTimeout(fmt.Sprintf("target %q run_timeout", name), t.RunTimeout, suiteTimeout, suiteUnbounded)
		if terr != nil {
			return Config{}, terr
		}
		t.Budget = RunBudget{Timeout: tt, Unbounded: tu, KillGrace: killGrace}
		re, eerr := validateExtract(name, t.Adapter, t.Extract)
		if eerr != nil {
			return Config{}, eerr
		}
		t.Extract.compiled = re
		comp, cerr := resolveCompleteness(name, t.Adapter, t.Completeness)
		if cerr != nil {
			return Config{}, cerr
		}
		t.Completeness = comp
		c.Targets[name] = t
	}
	if err := validatePricing(c.Pricing); err != nil {
		return Config{}, err
	}
	if c.Judge.Backend == "" {
		c.Judge.Backend = "claude"
	}
	if c.Judge.Model == "" {
		c.Judge.Model = DefaultJudgeModel
	}
	if c.Judge.Votes == 0 {
		c.Judge.Votes = 1
	}
	if err := validateJudge(c.Judge); err != nil {
		return Config{}, err
	}
	return c, nil
}

// validateJudge rejects a judge block that cannot yield a defined verdict: a vote
// count below 1, or an even count above 1 (best-of-N majority is undefined on a
// tie, so reject at load rather than only at runtime), or a temperature that is
// negative or non-finite. This mirrors validatePricing — fail fast with a wrapped
// error naming the offending value, never a silent fallback.
func validateJudge(j JudgeConfig) error {
	if j.Votes < 1 {
		return fmt.Errorf("judge.votes must be >= 1, got %d", j.Votes)
	}
	if j.Votes > 1 && j.Votes%2 == 0 {
		return fmt.Errorf("judge.votes must be odd, got %d (majority is undefined on an even-N tie)", j.Votes)
	}
	if j.Temperature < 0 || math.IsNaN(j.Temperature) || math.IsInf(j.Temperature, 0) {
		return fmt.Errorf("judge.temperature must be finite and >= 0, got %v", j.Temperature)
	}
	// votes>1 at temperature 0 sends near-identical calls, so best-of-N majority burns
	// cost without diversity. Reject loudly, naming BOTH remedies, rather than silently
	// auto-diversifying (Constitution IV): the human chooses a higher temperature or a
	// single vote (judge-ledger Defaults policy).
	if j.Votes > 1 && j.Temperature == 0 {
		return fmt.Errorf("judge: votes=%d with temperature=0 sends near-identical calls; raise temperature (e.g. 0.7) or set votes: 1", j.Votes)
	}
	if j.MaxCostUSD < 0 || math.IsNaN(j.MaxCostUSD) || math.IsInf(j.MaxCostUSD, 0) {
		return fmt.Errorf("judge.max_cost_usd must be finite and >= 0, got %v", j.MaxCostUSD)
	}
	return nil
}

// validateExtract validates a target's extract block and, for pattern mode,
// returns the compiled regexp so Load can cache it on the target (compile once,
// not per run). Whole (the default, empty mode) and marker modes return a nil
// regexp. Every failure is a hard, named load error — a marker/pattern mode
// missing its required field, a pattern that will not compile, or a pattern with
// no capture group (there would be nothing to extract), and an unknown mode value.
// No silent fallback to whole (Constitution IV).
//
// marker and pattern extraction are stdout-scoped and only the shell adapter
// produces stdout (core.ExtractAnswer runs in the shell driver; http sets Answer to
// the whole response body and never reads the policy). So a marker/pattern policy on
// any non-shell adapter is a LOUD load failure naming the target, adapter, and the
// shell requirement — never silently accepted and then ignored at runtime (FR-010,
// Constitution IV). whole/empty mode is the default no-op and stays valid everywhere.
func validateExtract(target, adapter string, e ExtractConfig) (*regexp.Regexp, error) {
	switch e.Mode {
	case "", core.ExtractWhole:
		return nil, nil
	case core.ExtractMarker:
		if adapter != "shell" {
			return nil, fmt.Errorf("target %q: extract mode %q requires the shell adapter (extraction reads stdout), but adapter is %q", target, core.ExtractMarker, adapter)
		}
		if e.Marker == "" {
			return nil, fmt.Errorf("target %q: extract marker is required when mode is %q", target, core.ExtractMarker)
		}
		return nil, nil
	case core.ExtractPattern:
		if adapter != "shell" {
			return nil, fmt.Errorf("target %q: extract mode %q requires the shell adapter (extraction reads stdout), but adapter is %q", target, core.ExtractPattern, adapter)
		}
		if e.Pattern == "" {
			return nil, fmt.Errorf("target %q: extract pattern is required when mode is %q", target, core.ExtractPattern)
		}
		re, err := regexp.Compile(e.Pattern)
		if err != nil {
			return nil, fmt.Errorf("target %q: extract pattern %q does not compile: %w", target, e.Pattern, err)
		}
		if re.NumSubexp() < 1 {
			return nil, fmt.Errorf("target %q: extract pattern %q must contain at least one capture group", target, e.Pattern)
		}
		return re, nil
	default:
		return nil, fmt.Errorf("target %q: unknown extract mode %q (want %q, %q, or %q)", target, e.Mode, core.ExtractWhole, core.ExtractMarker, core.ExtractPattern)
	}
}

// resolveCompleteness validates a target's completeness block and resolves the
// effective settle window (feature 008, contracts §4). Mode must be "settle" (empty
// → "settle") or "strict"; any other value is a hard load error naming the target
// and the offending value. SettleRaw must be a Go duration string: unparsable wraps
// the parse error and negative is rejected, both naming the target; zero is allowed.
// An omitted SettleRaw applies the adapter kind-default window.
func resolveCompleteness(target, adapter string, c Completeness) (Completeness, error) {
	switch c.Mode {
	case "":
		c.Mode = "settle"
	case "settle", "strict":
		// keep as written
	default:
		return Completeness{}, fmt.Errorf(`target %q: completeness.mode must be "settle" or "strict", got %q`, target, c.Mode)
	}
	raw := strings.TrimSpace(c.SettleRaw)
	if raw == "" {
		// Omitted: apply the adapter kind-default (shell 2s / http 5s). Adapters with
		// no registered default keep a zero window — not a speculative guarantee.
		c.Settle = defaultSettle[adapter]
		return c, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return Completeness{}, fmt.Errorf("target %q: completeness.settle: %w", target, err)
	}
	if d < 0 {
		return Completeness{}, fmt.Errorf("target %q: completeness.settle: must be >= 0, got %s", target, d)
	}
	c.Settle = d
	return c, nil
}

// validatePricing rejects pricing entries that would silently skew the cost a
// budgets/CEL run derives: an empty model name, or a rate that is negative or
// non-finite (NaN/±Inf). Zero is allowed (a free model). This is the config-load
// boundary mirror of the finite/non-negative check budgets already applies to an
// emitted cost_usd, so a bad rate fails fast here and never reaches costSum.
func validatePricing(p Pricing) error {
	for model, r := range p {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("pricing: model name must be non-empty")
		}
		if err := validateRate(model, "inputPerMTok", r.InputPerMTok); err != nil {
			return err
		}
		if err := validateRate(model, "outputPerMTok", r.OutputPerMTok); err != nil {
			return err
		}
	}
	return nil
}

func validateRate(model, field string, v float64) error {
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("pricing %q: %s must be finite and >= 0, got %v", model, field, v)
	}
	return nil
}

// resolveTimeout parses a run_timeout raw value into (timeout, unbounded). An empty
// raw inherits (defTimeout, defUnbounded) — the built-in default at suite level, or
// the resolved suite value at target level. The literal "unbounded" opts out of the
// timeout. Any other non-duration, or a non-positive duration, is a hard error
// naming the key and value (no silent fallback — Constitution IV).
func resolveTimeout(key, raw string, defTimeout time.Duration, defUnbounded bool) (time.Duration, bool, error) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "":
		return defTimeout, defUnbounded, nil
	case unboundedValue:
		return 0, true, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, false, fmt.Errorf("%s: invalid duration %q (want a Go duration like \"5m\" or %q)", key, raw, unboundedValue)
	}
	if d <= 0 {
		return 0, false, fmt.Errorf("%s: must be > 0, got %q (use %q for no limit)", key, raw, unboundedValue)
	}
	return d, false, nil
}

// resolveKillGrace parses the suite kill_grace. Empty → DefaultKillGrace. It must be
// a Go duration strictly greater than zero; a typo or non-positive value fails loudly.
func resolveKillGrace(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultKillGrace, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("kill_grace: invalid duration %q (want a Go duration like \"10s\")", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("kill_grace: must be > 0, got %q", raw)
	}
	return d, nil
}
