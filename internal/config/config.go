package config

import (
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	GithubToken          string        `env:"GITHUB_TOKEN,required"`
	APIKey               string        `env:"API_KEY,required"`
	MetadataPollInterval time.Duration `env:"METADATA_POLL_INTERVAL" envDefault:"15m"`
	AnalysisInterval     time.Duration `env:"POLL_INTERVAL" envDefault:"4h"`
	ListenAddr           string        `env:"LISTEN_ADDR" envDefault:":8443"`
	HTTPListenAddr       string        `env:"HTTP_LISTEN_ADDR" envDefault:":8080"`
	DBPath               string        `env:"DB_PATH" envDefault:"/data/geth-triage.db"`
	LogLevel             string        `env:"LOG_LEVEL" envDefault:"info"`
	TLSCert              string        `env:"TLS_CERT" envDefault:"/data/tls/cert.pem"`
	TLSKey               string        `env:"TLS_KEY" envDefault:"/data/tls/key.pem"`

	// GitHub webhook secret for signature verification.
	GithubWebhookSecret string `env:"GITHUB_WEBHOOK_SECRET" envDefault:""`

	// Claude Code analyzer settings
	GethRepoPath        string        `env:"GETH_REPO_PATH" envDefault:"./go-ethereum"`
	ClaudeCodeModel     string        `env:"CLAUDE_CODE_MODEL" envDefault:"sonnet"`
	ClaudeCodeMaxBudget string        `env:"CLAUDE_CODE_MAX_BUDGET" envDefault:"0.50"`
	ClaudeCodeTimeout   time.Duration `env:"CLAUDE_CODE_TIMEOUT" envDefault:"5m"`

	// Usage threshold (0-100): pause analysis when Claude session utilization exceeds this %.
	// The OAuth usage API reports a 5-hour rolling window utilization.
	// Set to 0 to disable usage checking.
	UsageThreshold float64 `env:"USAGE_THRESHOLD" envDefault:"80"`

	// Pushover notifications. Leave empty to disable.
	PushoverToken string `env:"PUSHOVER_TOKEN" envDefault:""`
	PushoverUser  string `env:"PUSHOVER_USER" envDefault:""`
}

func Load() (*Config, error) {
	_ = godotenv.Load()
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
