package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/anthropic"
	"github.com/sina-geth/geth-triage/internal/config"
	ghclient "github.com/sina-geth/geth-triage/internal/github"
)

func main() {
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <pr-number> [pr-number...]\n", os.Args[0])
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	gh := ghclient.NewClient(cfg.GithubToken, cfg.MaxDiffLines)
	ac := anthropic.NewClient(cfg.AnthropicAPIKey, cfg.AnthropicModel)

	for _, arg := range os.Args[1:] {
		num, err := strconv.Atoi(arg)
		if err != nil {
			log.Error().Str("arg", arg).Msg("invalid PR number, skipping")
			continue
		}

		log.Info().Int("pr", num).Msg("fetching PR from GitHub...")
		pr, err := gh.FetchPRDetail(ctx, num)
		if err != nil {
			log.Error().Err(err).Int("pr", num).Msg("failed to fetch PR")
			continue
		}

		log.Info().
			Str("title", pr.Title).
			Str("author", pr.Author).
			Int("additions", pr.Additions).
			Int("deletions", pr.Deletions).
			Int("comments", len(pr.Comments)).
			Int("diff_len", len(pr.Diff)).
			Msg("fetched PR, sending to Claude...")

		result, inputTok, outputTok, err := ac.AnalyzePR(ctx, *pr)
		if err != nil {
			log.Error().Err(err).Int("pr", num).Msg("analysis failed")
			continue
		}

		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Printf("\n=== PR #%d: %s ===\n", num, pr.Title)
		fmt.Printf("Author: %s | +%d/-%d | %d comments\n", pr.Author, pr.Additions, pr.Deletions, pr.CommentsCount)
		fmt.Printf("Tokens: %d in / %d out\n\n", inputTok, outputTok)
		fmt.Println(string(out))
		fmt.Println()
	}
}
