package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Tempo        Endpoint          `yaml:"tempo"`
	OTLPEndpoint string            `yaml:"otlpEndpoint"`
	Poll         PollSpec          `yaml:"poll"`
	Targets      map[string]Target `yaml:"targets"`
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

var defaultConcurrency = map[string]int{"shell": 1, "mcp": 1, "http": 8, "grpc": 8}

func Load(data []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
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
	return c, nil
}
