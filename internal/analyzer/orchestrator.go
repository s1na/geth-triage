package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
)

func NewOrchestrator(a *ClaudeCodeAnalyzer, s *store.Store, log zerolog.Logger, opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{
		analyzer: a,
		store:    s,
	}
	for _, opt := range opts {
		opt(o)
	}
	o.log = log
	return o
}

// AnalyzePending queries the DB for PRs that still need analysis and analyzes them.
// This is used on startup to resume work interrupted by usage throttling or restarts.
func (o *Orchestrator) AnalyzePending(ctx context.Context) error {
	pending, err := o.store.PRsNeedingAnalysis(ctx, ClaudeCodePromptVersion)
	if err != nil {
		return fmt.Errorf("query pending PRs: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	o.log.Info().Int("count", len(pending)).Msg("resuming analysis of pending PRs")

	var prs []github.PRData
	for _, pr := range pending {
		prs = append(prs, storePRToData(pr))
	}
	return o.Analyze(ctx, prs)
}

func storePRToData(pr store.PullRequest) github.PRData {
	var labels []string
	_ = json.Unmarshal(pr.Labels, &labels)
	return github.PRData{
		Number:        pr.Number,
		Title:         pr.Title,
		Author:        pr.Author,
		State:         pr.State,
		Labels:        labels,
		HeadSHA:       pr.HeadSHA,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		CommentsCount: pr.CommentsCount,
		CreatedAt:     pr.CreatedAt,
		UpdatedAt:     pr.UpdatedAt,
	}
}

// Analyze processes PRs sequentially with usage throttling.
func (o *Orchestrator) Analyze(ctx context.Context, prs []github.PRData) error {
	if len(prs) == 0 {
		return nil
	}
	o.log.Info().Int("count", len(prs)).Msg("analyzing PRs")

	for i, pr := range prs {
		if err := o.waitForUsage(ctx); err != nil {
			o.log.Warn().Err(err).Int("remaining", len(prs)-i).Msg("aborting analysis")
			return nil
		}

		_, err := o.AnalyzeSingle(ctx, pr)
		if err != nil {
			o.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to analyze PR")
			continue
		}
		o.log.Info().Int("pr", pr.Number).Msg("analyzed PR")
	}
	return nil
}

// AnalyzeSingle analyzes one PR and persists the result.
func (o *Orchestrator) AnalyzeSingle(ctx context.Context, pr github.PRData) (*store.Analysis, error) {
	result, err := o.analyzer.AnalyzePR(ctx, pr)
	if err != nil {
		return nil, err
	}

	relatedJSON, _ := json.Marshal(result.RelatedPRs)
	analysis := &store.Analysis{
		PRNumber:      pr.Number,
		Category:      result.Category,
		Confidence:    result.Confidence,
		Explanation:   result.Explanation,
		RelatedPRs:    relatedJSON,
		Model:         result.Model,
		PromptVersion: result.PromptVersion,
		InputTokens:   result.InputTokens,
		OutputTokens:  result.OutputTokens,
		CreatedAt:     time.Now().UTC(),
	}

	if err := o.store.InsertAnalysis(ctx, analysis); err != nil {
		return nil, fmt.Errorf("insert analysis: %w", err)
	}
	if err := o.store.SetAnalyzedSHA(ctx, pr.Number, pr.HeadSHA); err != nil {
		o.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to set analyzed SHA")
	}

	return analysis, nil
}

// waitForUsage checks usage and blocks until utilization drops below threshold.
func (o *Orchestrator) waitForUsage(ctx context.Context) error {
	if o.usageChecker == nil || o.usageThreshold <= 0 {
		return nil
	}
	for {
		status, err := o.usageChecker.CheckUsage(ctx)
		if err != nil {
			o.log.Warn().Err(err).Msg("failed to check usage, continuing anyway")
			return nil
		}
		o.log.Info().Float64("utilization", status.Utilization).Float64("threshold", o.usageThreshold).Msg("usage check")
		if status.Utilization < o.usageThreshold {
			return nil
		}

		waitDur := time.Until(status.ResetsAt)
		if waitDur <= 0 {
			waitDur = 5 * time.Minute
		}
		o.log.Warn().
			Float64("utilization", status.Utilization).
			Time("resets_at", status.ResetsAt).
			Dur("wait", waitDur).
			Msg("usage threshold exceeded, waiting for reset")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDur):
		}
	}
}
