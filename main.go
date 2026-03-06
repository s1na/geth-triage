package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/s1na/geth-triage/internal/analyzer"
	"github.com/s1na/geth-triage/internal/api"
	"github.com/s1na/geth-triage/internal/claude"
	"github.com/s1na/geth-triage/internal/config"
	ghclient "github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
	"github.com/s1na/geth-triage/internal/tlscert"
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

	// Init GitHub client and poller
	gh := ghclient.NewClient(cfg.GithubToken)
	poller := ghclient.NewPoller(gh, db, log)

	// Init Claude Code analyzer
	ccAnalyzer := analyzer.NewClaudeCodeAnalyzer(cfg.GethRepoPath, cfg.ClaudeCodeModel, cfg.ClaudeCodeMaxBudget, cfg.ClaudeCodeTimeout, log)
	if err := ccAnalyzer.EnsureRepo(ctx); err != nil {
		log.Fatal().Err(err).Msg("failed to ensure geth repo")
	}

	var opts []analyzer.OrchestratorOption
	if cfg.UsageThreshold > 0 {
		uc := claude.NewUsageChecker()
		opts = append(opts, analyzer.WithUsageChecker(uc, cfg.UsageThreshold))
		log.Info().Float64("threshold", cfg.UsageThreshold).Msg("usage-based throttling enabled")
	}

	az := analyzer.NewOrchestrator(ccAnalyzer, db, log, opts...)

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

	// Analysis worker — single goroutine processes the queue sequentially
	g.Go(func() error {
		az.Run(ctx)
		return nil
	})

	// On startup: sync metadata if overdue, then enqueue pending analysis
	g.Go(func() error {
		lastPollStr, _ := db.GetState(ctx, "last_poll_time")
		shouldPollNow := true
		if lastPollStr != "" {
			lastPoll, err := time.Parse(time.RFC3339, lastPollStr)
			if err == nil && time.Since(lastPoll) < cfg.MetadataPollInterval {
				shouldPollNow = false
			}
		}
		if shouldPollNow {
			runMetadataSync(ctx, poller, log)
		}
		if err := az.AnalyzePending(ctx); err != nil {
			log.Error().Err(err).Msg("failed to enqueue pending PRs")
		}
		return nil
	})

	// Fast metadata sync loop — keeps PR state (open/closed) fresh
	g.Go(func() error {
		ticker := time.NewTicker(cfg.MetadataPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				runMetadataSync(ctx, poller, log)
			}
		}
	})

	// Slow analysis loop — enqueues PRs whose HEAD SHA changed since last analysis
	g.Go(func() error {
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if err := az.AnalyzePending(ctx); err != nil {
					log.Error().Err(err).Msg("analysis check failed")
				} else {
					log.Info().Msg("analysis check complete")
				}
			}
		}
	})

	if err := g.Wait(); err != nil {
		log.Fatal().Err(err).Msg("service error")
	}
}

func runMetadataSync(ctx context.Context, poller *ghclient.Poller, log zerolog.Logger) {
	log.Info().Msg("syncing PR metadata from GitHub")
	if _, err := poller.Poll(ctx); err != nil {
		log.Error().Err(err).Msg("metadata sync failed")
	}
}
