package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/anthropic"
	"github.com/sina-geth/geth-triage/internal/github"
	"github.com/sina-geth/geth-triage/internal/store"
)

type Analyzer struct {
	anthropic      *anthropic.Client
	store          *store.Store
	batchThreshold int
	log            zerolog.Logger
}

func New(ac *anthropic.Client, s *store.Store, batchThreshold int, log zerolog.Logger) *Analyzer {
	return &Analyzer{
		anthropic:      ac,
		store:          s,
		batchThreshold: batchThreshold,
		log:            log,
	}
}

// Analyze decides whether to use Batch or Messages API based on count.
func (a *Analyzer) Analyze(ctx context.Context, prs []github.PRData) error {
	if len(prs) == 0 {
		return nil
	}

	if len(prs) > a.batchThreshold {
		return a.analyzeBatch(ctx, prs)
	}
	return a.analyzeMessages(ctx, prs)
}

// AnalyzeSingle analyzes a single PR via Messages API (synchronous).
func (a *Analyzer) AnalyzeSingle(ctx context.Context, pr github.PRData) (*store.Analysis, error) {
	result, inputTokens, outputTokens, err := a.anthropic.AnalyzePR(ctx, pr)
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
		Model:         a.anthropic.Model(),
		PromptVersion: anthropic.PromptVersion,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
		CreatedAt:     time.Now().UTC(),
	}

	if err := a.store.InsertAnalysis(ctx, analysis); err != nil {
		return nil, fmt.Errorf("insert analysis: %w", err)
	}
	if err := a.store.SetAnalyzedSHA(ctx, pr.Number, pr.HeadSHA); err != nil {
		a.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to set analyzed SHA")
	}

	return analysis, nil
}

func (a *Analyzer) analyzeMessages(ctx context.Context, prs []github.PRData) error {
	a.log.Info().Int("count", len(prs)).Msg("analyzing PRs via Messages API")

	for _, pr := range prs {
		_, err := a.AnalyzeSingle(ctx, pr)
		if err != nil {
			a.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to analyze PR")
			continue
		}
		a.log.Info().Int("pr", pr.Number).Msg("analyzed PR")
	}
	return nil
}

func (a *Analyzer) analyzeBatch(ctx context.Context, prs []github.PRData) error {
	a.log.Info().Int("count", len(prs)).Msg("analyzing PRs via Batch API")

	batchID, customIDMap, err := a.anthropic.CreateBatch(ctx, prs)
	if err != nil {
		return fmt.Errorf("create batch: %w", err)
	}

	// Persist batch job
	job := &store.BatchJob{
		BatchID:       batchID,
		Status:        "in_progress",
		TotalRequests: len(prs),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := a.store.InsertBatchJob(ctx, job); err != nil {
		return fmt.Errorf("insert batch job: %w", err)
	}

	// Persist batch request mappings
	for customID, prNumber := range customIDMap {
		br := &store.BatchRequest{
			BatchID:  batchID,
			CustomID: customID,
			PRNumber: prNumber,
		}
		if err := a.store.InsertBatchRequest(ctx, br); err != nil {
			a.log.Error().Err(err).Str("custom_id", customID).Msg("failed to insert batch request")
		}
	}

	a.log.Info().Str("batch_id", batchID).Int("requests", len(prs)).Msg("batch created")
	return nil
}

// ProcessCompletedBatch retrieves results from a completed batch and stores analyses.
func (a *Analyzer) ProcessCompletedBatch(ctx context.Context, batchID string) error {
	results, tokens, err := a.anthropic.CollectBatchResults(ctx, batchID, a.log)
	if err != nil {
		return fmt.Errorf("collect batch results: %w", err)
	}

	for customID, result := range results {
		br, err := a.store.GetBatchRequestByCustomID(ctx, customID)
		if err != nil || br == nil {
			a.log.Error().Err(err).Str("custom_id", customID).Msg("failed to find batch request")
			continue
		}

		relatedJSON, _ := json.Marshal(result.RelatedPRs)
		tok := tokens[customID]

		analysis := &store.Analysis{
			PRNumber:      br.PRNumber,
			Category:      result.Category,
			Confidence:    result.Confidence,
			Explanation:   result.Explanation,
			RelatedPRs:    relatedJSON,
			Model:         a.anthropic.Model(),
			PromptVersion: anthropic.PromptVersion,
			InputTokens:   tok[0],
			OutputTokens:  tok[1],
			CreatedAt:     time.Now().UTC(),
		}

		if err := a.store.InsertAnalysis(ctx, analysis); err != nil {
			a.log.Error().Err(err).Int("pr", br.PRNumber).Msg("failed to insert analysis")
			continue
		}

		// Update analyzed SHA
		pr, err := a.store.GetPR(ctx, br.PRNumber)
		if err == nil && pr != nil {
			_ = a.store.SetAnalyzedSHA(ctx, br.PRNumber, pr.HeadSHA)
		}

		a.log.Info().Int("pr", br.PRNumber).Str("category", result.Category).Float64("confidence", result.Confidence).Msg("batch result stored")
	}

	return nil
}

// PollPendingBatches checks all in-progress batches and processes completed ones.
func (a *Analyzer) PollPendingBatches(ctx context.Context) error {
	jobs, err := a.store.PendingBatchJobs(ctx)
	if err != nil {
		return fmt.Errorf("get pending batches: %w", err)
	}

	for _, job := range jobs {
		batch, err := a.anthropic.PollBatch(ctx, job.BatchID)
		if err != nil {
			a.log.Error().Err(err).Str("batch_id", job.BatchID).Msg("failed to poll batch")
			continue
		}

		status := string(batch.ProcessingStatus)
		if err := a.store.UpdateBatchJob(ctx, job.BatchID, status,
			int(batch.RequestCounts.Succeeded),
			int(batch.RequestCounts.Errored),
			int(batch.RequestCounts.Canceled),
			int(batch.RequestCounts.Expired),
		); err != nil {
			a.log.Error().Err(err).Str("batch_id", job.BatchID).Msg("failed to update batch job")
		}

		a.log.Info().
			Str("batch_id", job.BatchID).
			Str("status", status).
			Int64("succeeded", batch.RequestCounts.Succeeded).
			Int64("errored", batch.RequestCounts.Errored).
			Msg("batch poll")

		if batch.ProcessingStatus == "ended" {
			if err := a.ProcessCompletedBatch(ctx, job.BatchID); err != nil {
				a.log.Error().Err(err).Str("batch_id", job.BatchID).Msg("failed to process completed batch")
			}
		}
	}
	return nil
}
