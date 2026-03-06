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

func NewOrchestrator(a PRAnalyzer, s *store.Store, log zerolog.Logger, opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{
		analyzer: a,
		store:    s,
	}
	for _, opt := range opts {
		opt(o)
	}
	// Store logger on the struct for use in methods
	o.log = log
	return o
}

// AnalyzePending queries the DB for PRs that still need analysis and analyzes them.
// This is used on startup to resume work interrupted by usage throttling or restarts.
func (o *Orchestrator) AnalyzePending(ctx context.Context) error {
	if o.promptVersion == "" {
		return nil
	}
	pending, err := o.store.PRsNeedingAnalysis(ctx, o.promptVersion)
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
	return o.analyzeSequential(ctx, prs)
}

func storePRToData(pr store.PullRequest) github.PRData {
	var labels []string
	_ = json.Unmarshal(pr.Labels, &labels)
	return github.PRData{
		Number:        pr.Number,
		Title:         pr.Title,
		Author:        pr.Author,
		Labels:        labels,
		HeadSHA:       pr.HeadSHA,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		CommentsCount: pr.CommentsCount,
		CreatedAt:     pr.CreatedAt,
		UpdatedAt:     pr.UpdatedAt,
	}
}

// Analyze routes PRs to batch or single analysis based on configuration.
func (o *Orchestrator) Analyze(ctx context.Context, prs []github.PRData) error {
	if len(prs) == 0 {
		return nil
	}

	if o.batchAnalyzer != nil && len(prs) > o.batchThreshold {
		return o.analyzeBatch(ctx, prs)
	}
	return o.analyzeSequential(ctx, prs)
}

// AnalyzeSingle analyzes one PR and persists the result.
func (o *Orchestrator) AnalyzeSingle(ctx context.Context, pr github.PRData) (*store.Analysis, error) {
	result, err := o.analyzer.AnalyzePR(ctx, pr)
	if err != nil {
		return nil, err
	}

	analysis := o.resultToStoreAnalysis(pr.Number, result)

	if err := o.store.InsertAnalysis(ctx, analysis); err != nil {
		return nil, fmt.Errorf("insert analysis: %w", err)
	}
	if err := o.store.SetAnalyzedSHA(ctx, pr.Number, pr.HeadSHA); err != nil {
		o.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to set analyzed SHA")
	}

	return analysis, nil
}

func (o *Orchestrator) analyzeSequential(ctx context.Context, prs []github.PRData) error {
	o.log.Info().Int("count", len(prs)).Msg("analyzing PRs sequentially")

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

// waitForUsage checks usage and blocks until utilization drops below threshold.
// Returns error only if the context is cancelled.
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

func (o *Orchestrator) analyzeBatch(ctx context.Context, prs []github.PRData) error {
	o.log.Info().Int("count", len(prs)).Msg("analyzing PRs via batch")

	batchID, customIDMap, err := o.batchAnalyzer.CreateBatch(ctx, prs)
	if err != nil {
		return fmt.Errorf("create batch: %w", err)
	}

	job := &store.BatchJob{
		BatchID:       batchID,
		Status:        "in_progress",
		TotalRequests: len(prs),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := o.store.InsertBatchJob(ctx, job); err != nil {
		return fmt.Errorf("insert batch job: %w", err)
	}

	for customID, prNumber := range customIDMap {
		br := &store.BatchRequest{
			BatchID:  batchID,
			CustomID: customID,
			PRNumber: prNumber,
		}
		if err := o.store.InsertBatchRequest(ctx, br); err != nil {
			o.log.Error().Err(err).Str("custom_id", customID).Msg("failed to insert batch request")
		}
	}

	o.log.Info().Str("batch_id", batchID).Int("requests", len(prs)).Msg("batch created")
	return nil
}

// PollPendingBatches checks all in-progress batches and processes completed ones.
// No-op if batch analysis is not configured.
func (o *Orchestrator) PollPendingBatches(ctx context.Context) error {
	if o.batchAnalyzer == nil {
		return nil
	}

	jobs, err := o.store.PendingBatchJobs(ctx)
	if err != nil {
		return fmt.Errorf("get pending batches: %w", err)
	}

	for _, job := range jobs {
		status, succeeded, errored, canceled, expired, err := o.batchAnalyzer.PollBatch(ctx, job.BatchID)
		if err != nil {
			o.log.Error().Err(err).Str("batch_id", job.BatchID).Msg("failed to poll batch")
			continue
		}

		if err := o.store.UpdateBatchJob(ctx, job.BatchID, status, succeeded, errored, canceled, expired); err != nil {
			o.log.Error().Err(err).Str("batch_id", job.BatchID).Msg("failed to update batch job")
		}

		o.log.Info().
			Str("batch_id", job.BatchID).
			Str("status", status).
			Int("succeeded", succeeded).
			Int("errored", errored).
			Msg("batch poll")

		if status == "ended" {
			if err := o.processCompletedBatch(ctx, job.BatchID); err != nil {
				o.log.Error().Err(err).Str("batch_id", job.BatchID).Msg("failed to process completed batch")
			}
		}
	}
	return nil
}

func (o *Orchestrator) processCompletedBatch(ctx context.Context, batchID string) error {
	results, err := o.batchAnalyzer.CollectBatchResults(ctx, batchID)
	if err != nil {
		return fmt.Errorf("collect batch results: %w", err)
	}

	for customID, result := range results {
		br, err := o.store.GetBatchRequestByCustomID(ctx, customID)
		if err != nil || br == nil {
			o.log.Error().Err(err).Str("custom_id", customID).Msg("failed to find batch request")
			continue
		}

		analysis := o.resultToStoreAnalysis(br.PRNumber, result)
		if err := o.store.InsertAnalysis(ctx, analysis); err != nil {
			o.log.Error().Err(err).Int("pr", br.PRNumber).Msg("failed to insert analysis")
			continue
		}

		pr, err := o.store.GetPR(ctx, br.PRNumber)
		if err == nil && pr != nil {
			_ = o.store.SetAnalyzedSHA(ctx, br.PRNumber, pr.HeadSHA)
		}

		o.log.Info().Int("pr", br.PRNumber).Str("category", result.Category).Float64("confidence", result.Confidence).Msg("batch result stored")
	}

	return nil
}

func (o *Orchestrator) resultToStoreAnalysis(prNumber int, result *AnalysisResult) *store.Analysis {
	relatedJSON, _ := json.Marshal(result.RelatedPRs)
	return &store.Analysis{
		PRNumber:      prNumber,
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
}
