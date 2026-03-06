package github

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/store"
)

type Poller struct {
	client *Client
	store  *store.Store
	log    zerolog.Logger
}

func NewPoller(client *Client, store *store.Store, log zerolog.Logger) *Poller {
	return &Poller{client: client, store: store, log: log}
}

// PollResult contains the results of a metadata sync.
type PollResult struct {
	NewPRs []PRData // PRs not previously in the store
}

// Poll fetches all open PRs and upserts them into the store.
func (p *Poller) Poll(ctx context.Context) (*PollResult, error) {
	p.log.Info().Msg("polling GitHub for open PRs")
	start := time.Now()

	prs, err := p.client.ListOpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	p.log.Info().Int("count", len(prs)).Dur("duration", time.Since(start)).Msg("fetched open PRs")

	result := &PollResult{}
	for _, pr := range prs {
		existing, err := p.store.GetPR(ctx, pr.Number)
		if err != nil {
			p.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to get PR from store")
			continue
		}

		labelsJSON, _ := json.Marshal(pr.Labels)
		storePR := &store.PullRequest{
			Number:        pr.Number,
			Title:         pr.Title,
			Author:        pr.Author,
			State:         pr.State,
			Labels:        labelsJSON,
			HeadSHA:       pr.HeadSHA,
			Additions:     pr.Additions,
			Deletions:     pr.Deletions,
			CommentsCount: pr.CommentsCount,
			CreatedAt:     pr.CreatedAt,
			UpdatedAt:     pr.UpdatedAt,
			FetchedAt:     time.Now().UTC(),
		}
		if err := p.store.UpsertPR(ctx, storePR); err != nil {
			p.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to upsert PR")
			continue
		}

		if existing == nil {
			result.NewPRs = append(result.NewPRs, pr)
		}
	}

	// Mark PRs no longer open on GitHub as closed
	openNumbers := make(map[int]bool, len(prs))
	for _, pr := range prs {
		openNumbers[pr.Number] = true
	}
	closed, err := p.store.CloseStale(ctx, openNumbers)
	if err != nil {
		p.log.Error().Err(err).Msg("failed to close stale PRs")
	} else if closed > 0 {
		p.log.Info().Int("closed", closed).Msg("marked stale PRs as closed")
	}

	// Update last poll time
	if err := p.store.SetState(ctx, "last_poll_time", time.Now().UTC().Format(time.RFC3339)); err != nil {
		p.log.Error().Err(err).Msg("failed to set last poll time")
	}

	p.log.Info().Int("total", len(prs)).Int("new", len(result.NewPRs)).Msg("poll complete")
	return result, nil
}
