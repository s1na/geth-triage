package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/analyzer"
	"github.com/sina-geth/geth-triage/internal/anthropic"
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
	ac := anthropic.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicModel)
	poller := ghclient.NewPoller(gh, db, log)
	prAnalyzer := analyzer.NewAPIAnalyzer(ac)
	batchAnalyzer := analyzer.NewAPIBatchAnalyzer(ac, log)
	az := analyzer.NewOrchestrator(prAnalyzer, db, log, analyzer.WithBatchAnalyzer(batchAnalyzer, cfg.BatchThreshold))

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

	// Step 2: Fetch details (diffs, comments) for PRs needing analysis
	log.Info().Int("count", len(changed)).Msg("fetching PR details...")
	detailed, err := poller.FetchDetails(ctx, changed)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to fetch PR details")
	}

	// Step 3: Analyze
	log.Info().Int("count", len(detailed)).Msg("starting analysis...")
	if err := az.Analyze(ctx, detailed); err != nil {
		log.Fatal().Err(err).Msg("failed to analyze PRs")
	}

	log.Info().Msg("batch processing complete")
}
