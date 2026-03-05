package config

import (
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	GithubToken       string        `env:"GITHUB_TOKEN,required"`
	AnthropicAPIKey   string        `env:"ANTHROPIC_API_KEY"`
	APIKey            string        `env:"API_KEY,required"`
	PollInterval      time.Duration `env:"POLL_INTERVAL" envDefault:"4h"`
	BatchPollInterval time.Duration `env:"BATCH_POLL_INTERVAL" envDefault:"5m"`
	ListenAddr        string        `env:"LISTEN_ADDR" envDefault:":8443"`
	HTTPListenAddr    string        `env:"HTTP_LISTEN_ADDR" envDefault:":8080"`
	DBPath            string        `env:"DB_PATH" envDefault:"/data/geth-triage.db"`
	AnthropicModel    string        `env:"ANTHROPIC_MODEL" envDefault:"claude-sonnet-4-20250514"`
	BatchThreshold    int           `env:"BATCH_THRESHOLD" envDefault:"10"`
	MaxDiffLines      int           `env:"MAX_DIFF_LINES" envDefault:"500"`
	LogLevel          string        `env:"LOG_LEVEL" envDefault:"info"`
	TLSCert           string        `env:"TLS_CERT" envDefault:"/data/tls/cert.pem"`
	TLSKey            string        `env:"TLS_KEY" envDefault:"/data/tls/key.pem"`

	// Analyzer type: "api" (default) or "claudecode"
	AnalyzerType        string        `env:"ANALYZER_TYPE" envDefault:"api"`
	GethRepoPath        string        `env:"GETH_REPO_PATH" envDefault:"./go-ethereum"`
	ClaudeCodeModel     string        `env:"CLAUDE_CODE_MODEL" envDefault:"sonnet"`
	ClaudeCodeMaxBudget string        `env:"CLAUDE_CODE_MAX_BUDGET" envDefault:"0.50"`
	ClaudeCodeTimeout   time.Duration `env:"CLAUDE_CODE_TIMEOUT" envDefault:"5m"`
}

func Load() (*Config, error) {
	// Load .env file if present (does not override existing env vars)
	_ = godotenv.Load()
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
