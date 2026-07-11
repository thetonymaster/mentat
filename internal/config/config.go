package config

import (
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Store        string            `yaml:"store"`
	Tempo        Endpoint          `yaml:"tempo"`
	OTLPEndpoint string            `yaml:"otlpEndpoint"`
	Poll         PollSpec          `yaml:"poll"`
	Targets      map[string]Target `yaml:"targets"`
	Pricing      Pricing           `yaml:"pricing"`
	Expectations string            `yaml:"expectations"`
	Judge        JudgeConfig       `yaml:"judge"`
}

// JudgeConfig configures the semantic (LLM-judge) result matcher. The whole block
// is optional — a project that never writes `the result means` never needs it; the
// defaults applied in Load make an omitted block valid.
type JudgeConfig struct {
	Backend string `yaml:"backend"` // default "claude"
	Model   string `yaml:"model"`   // default "claude-opus-4-8"
	Votes   int    `yaml:"votes"`   // default 1; best-of-N majority (odd N required)
	// Temperature is applied only on models that accept it (Sonnet 4.6 / Haiku 4.5);
	// omitted on Opus-tier. Optional knob, default 0.
	Temperature float64 `yaml:"temperature"`
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

var defaultConcurrency = map[string]int{"shell": 1, "mcp": 1, "http": 8, "grpc": 8}

func Load(data []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if c.Store == "" {
		c.Store = "tempo"
	}
	if c.Expectations == "" {
		c.Expectations = "expectations"
	}
	// A non-positive searchLimit (omitted or <= 0) defaults to 100 so Tempo.Query
	// always sends an explicit, truncation-guardable page size (research R3, A4).
	if c.Poll.SearchLimit <= 0 {
		c.Poll.SearchLimit = 100
	}
	for name, t := range c.Targets {
		def, ok := defaultConcurrency[t.Adapter]
		if !ok {
			return Config{}, fmt.Errorf("target %q: unknown adapter %q", name, t.Adapter)
		}
		if t.MaxConcurrency < 0 {
			return Config{}, fmt.Errorf("target %q: max_concurrency must be >= 0, got %d", name, t.MaxConcurrency)
		}
		if t.MaxConcurrency == 0 {
			t.MaxConcurrency = def
			c.Targets[name] = t
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
			c.Targets[name] = t
		}
	}
	if err := validatePricing(c.Pricing); err != nil {
		return Config{}, err
	}
	if c.Judge.Backend == "" {
		c.Judge.Backend = "claude"
	}
	if c.Judge.Model == "" {
		c.Judge.Model = "claude-opus-4-8"
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
	return nil
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
