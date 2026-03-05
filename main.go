package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/sina-geth/geth-triage/internal/analyzer"
	"github.com/sina-geth/geth-triage/internal/anthropic"
	"github.com/sina-geth/geth-triage/internal/api"
	"github.com/sina-geth/geth-triage/internal/claude"
	"github.com/sina-geth/geth-triage/internal/config"
	ghclient "github.com/sina-geth/geth-triage/internal/github"
	"github.com/sina-geth/geth-triage/internal/store"
	"github.com/sina-geth/geth-triage/internal/tlscert"
	"path/filepath"
)

func main() {
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err == nil {
		zerolog.SetGlobalLevel(level)
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
		// No batch support for claude code
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
		log.Info().Float64("threshold", cfg.UsageThreshold).Msg("usage-based throttling enabled")
	}

	az := analyzer.NewOrchestrator(prAnalyzer, db, log, opts...)

	// Check if analyzer supports repo management (for pre-cycle updates)
	var repoMgr analyzer.RepoManager
	if rm, ok := prAnalyzer.(analyzer.RepoManager); ok {
		repoMgr = rm
	}

	// Init HTTP server
	handler := api.NewServer(cfg.APIKey, db, az, gh, log)
	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	g, ctx := errgroup.WithContext(ctx)

	// Generate self-signed TLS cert if needed
	certDir := filepath.Dir(cfg.TLSCert)
	if err := os.MkdirAll(certDir, 0700); err != nil {
		log.Fatal().Err(err).Msg("failed to create TLS cert directory")
	}
	if err := tlscert.EnsureCert(cfg.TLSCert, cfg.TLSKey); err != nil {
		log.Fatal().Err(err).Msg("failed to generate TLS cert")
	}
	log.Info().Str("cert", cfg.TLSCert).Msg("TLS certificate ready")

	// HTTPS server
	g.Go(func() error {
		log.Info().Str("addr", cfg.ListenAddr).Msg("starting HTTPS server")
		if err := srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	// HTTP server
	httpSrv := &http.Server{
		Addr:    cfg.HTTPListenAddr,
		Handler: handler,
	}
	g.Go(func() error {
		log.Info().Str("addr", cfg.HTTPListenAddr).Msg("starting HTTP server")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	// Graceful shutdown
	g.Go(func() error {
		<-ctx.Done()
		log.Info().Msg("shutting down servers")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
		return httpSrv.Shutdown(shutdownCtx)
	})

	// Check if poll is overdue on startup
	g.Go(func() error {
		lastPollStr, _ := db.GetState(ctx, "last_poll_time")
		shouldPollNow := true
		if lastPollStr != "" {
			lastPoll, err := time.Parse(time.RFC3339, lastPollStr)
			if err == nil && time.Since(lastPoll) < cfg.PollInterval {
				shouldPollNow = false
			}
		}
		if shouldPollNow {
			runPollCycle(ctx, poller, az, repoMgr, log)
		}
		return nil
	})

	// PR polling loop
	g.Go(func() error {
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				runPollCycle(ctx, poller, az, repoMgr, log)
			}
		}
	})

	// Batch polling loop
	g.Go(func() error {
		// Poll pending batches on startup
		if err := az.PollPendingBatches(ctx); err != nil {
			log.Error().Err(err).Msg("startup batch poll failed")
		}

		ticker := time.NewTicker(cfg.BatchPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if err := az.PollPendingBatches(ctx); err != nil {
					log.Error().Err(err).Msg("batch poll failed")
				}
			}
		}
	})

	if err := g.Wait(); err != nil {
		log.Fatal().Err(err).Msg("service error")
	}
}

func runPollCycle(ctx context.Context, poller *ghclient.Poller, az *analyzer.Orchestrator, repoMgr analyzer.RepoManager, log zerolog.Logger) {
	if repoMgr != nil {
		if err := repoMgr.EnsureRepo(ctx); err != nil {
			log.Warn().Err(err).Msg("failed to update repo before poll cycle")
		}
	}
	log.Info().Msg("starting poll cycle")
	changed, err := poller.Poll(ctx)
	if err != nil {
		log.Error().Err(err).Msg("poll failed")
		return
	}
	if len(changed) == 0 {
		log.Info().Msg("no PRs need analysis")
		return
	}

	// Claude Code analyzer fetches diff/comments itself via gh CLI,
	// so we only need to fetch details for other analyzer types.
	toAnalyze := changed
	if repoMgr == nil {
		detailed, err := poller.FetchDetails(ctx, changed)
		if err != nil {
			log.Error().Err(err).Msg("failed to fetch PR details")
			return
		}
		toAnalyze = detailed
	}

	if err := az.Analyze(ctx, toAnalyze); err != nil {
		log.Error().Err(err).Msg("analysis failed")
	}
}
