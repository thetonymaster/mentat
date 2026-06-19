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
}

type Endpoint struct {
	Endpoint string `yaml:"endpoint"`
}

type PollSpec struct {
	Interval  string `yaml:"interval"`
	Timeout   string `yaml:"timeout"`
	StableFor int    `yaml:"stableFor"`
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
