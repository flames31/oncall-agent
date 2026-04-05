package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Data sources
	PrometheusURL string `yaml:"prometheus_url"`
	LokiURL       string `yaml:"loki_url"`

	// LLM — Groq (OpenAI-compatible endpoint)
	GroqAPIKey string `yaml:"groq_api_key"`
	GroqModel  string `yaml:"groq_model"`

	// Slack
	SlackBotToken string `yaml:"slack_bot_token"`
	SlackChannel  string `yaml:"slack_channel"`

	// Alerting sources
	PagerDutySecret string `yaml:"pagerduty_secret"`

	// Storage
	PostgresDSN string `yaml:"postgres_dsn"`

	// Tuning
	WorkerCount          int `yaml:"worker_count"`
	DedupWindowSeconds   int `yaml:"dedup_window_seconds"`
	MaxLLMIterations     int `yaml:"max_llm_iterations"`
	InvestigationTimeout int `yaml:"investigation_timeout_seconds"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// ${VAR} placeholders in the YAML are expanded from the environment
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.setDefaults()
	return &cfg, cfg.validate()
}

func (c *Config) setDefaults() {
	if c.WorkerCount == 0 {
		c.WorkerCount = 5
	}
	if c.DedupWindowSeconds == 0 {
		c.DedupWindowSeconds = 30
	}
	if c.MaxLLMIterations == 0 {
		c.MaxLLMIterations = 8
	}
	if c.InvestigationTimeout == 0 {
		c.InvestigationTimeout = 45
	}
	if c.GroqModel == "" {
		c.GroqModel = "llama-3.3-70b-versatile"
	}
}

func (c *Config) validate() error {
	if c.GroqAPIKey == "" {
		return fmt.Errorf("groq_api_key is required")
	}
	if c.PostgresDSN == "" {
		return fmt.Errorf("postgres_dsn is required")
	}
	if c.SlackBotToken == "" {
		return fmt.Errorf("slack_bot_token is required")
	}
	return nil
}
