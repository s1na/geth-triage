package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	GithubToken       string        `env:"GITHUB_TOKEN,required"`
	AnthropicAPIKey   string        `env:"ANTHROPIC_API_KEY,required"`
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
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
