package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/analyzer"
	"github.com/sina-geth/geth-triage/internal/anthropic"
	"github.com/sina-geth/geth-triage/internal/claude"
	"github.com/sina-geth/geth-triage/internal/config"
	ghclient "github.com/sina-geth/geth-triage/internal/github"
	"github.com/sina-geth/geth-triage/internal/store"
)

func main() {
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Init store
	db, err := store.New(ctx, cfg.DBPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to init store")
	}
	defer db.Close()

	// Init clients
	gh := ghclient.NewClient(cfg.GithubToken, cfg.MaxDiffLines)
	poller := ghclient.NewPoller(gh, db, log)

	var prAnalyzer analyzer.PRAnalyzer
	var opts []analyzer.OrchestratorOption

	switch cfg.AnalyzerType {
	case "claudecode":
		ccAnalyzer := analyzer.NewClaudeCodeAnalyzer(cfg.GethRepoPath, cfg.ClaudeCodeModel, cfg.ClaudeCodeMaxBudget, cfg.ClaudeCodeTimeout, log)
		if err := ccAnalyzer.EnsureRepo(ctx); err != nil {
			log.Fatal().Err(err).Msg("failed to ensure geth repo")
		}
		prAnalyzer = ccAnalyzer
	case "api":
		if cfg.AnthropicAPIKey == "" {
			log.Fatal().Msg("ANTHROPIC_API_KEY is required when ANALYZER_TYPE=api")
		}
		ac := anthropic.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicModel)
		prAnalyzer = analyzer.NewAPIAnalyzer(ac)
		batchAnalyzer := analyzer.NewAPIBatchAnalyzer(ac, log)
		opts = append(opts, analyzer.WithBatchAnalyzer(batchAnalyzer, cfg.BatchThreshold))
	default:
		log.Fatal().Str("type", cfg.AnalyzerType).Msg("unknown ANALYZER_TYPE")
	}

	if cfg.UsageThreshold > 0 {
		uc := claude.NewUsageChecker()
		opts = append(opts, analyzer.WithUsageChecker(uc, cfg.UsageThreshold))
	}

	az := analyzer.NewOrchestrator(prAnalyzer, db, log, opts...)

	// Step 1: Fetch all open PRs
	log.Info().Msg("fetching all open PRs from GitHub...")
	changed, err := poller.Poll(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to poll PRs")
	}

	if len(changed) == 0 {
		log.Info().Msg("no PRs need analysis")
		return
	}

	// Step 2: Fetch details for non-Claude Code analyzers (Claude Code uses gh CLI)
	toAnalyze := changed
	if cfg.AnalyzerType != "claudecode" {
		log.Info().Int("count", len(changed)).Msg("fetching PR details...")
		detailed, err := poller.FetchDetails(ctx, changed)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to fetch PR details")
		}
		toAnalyze = detailed
	}

	// Step 3: Analyze
	log.Info().Int("count", len(toAnalyze)).Msg("starting analysis...")
	if err := az.Analyze(ctx, toAnalyze); err != nil {
		log.Fatal().Err(err).Msg("failed to analyze PRs")
	}

	log.Info().Msg("batch processing complete")
}
