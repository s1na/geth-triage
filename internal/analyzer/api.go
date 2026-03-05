package analyzer

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/anthropic"
	"github.com/sina-geth/geth-triage/internal/github"
)

// APIAnalyzer implements PRAnalyzer using the Anthropic Messages API (single-shot).
type APIAnalyzer struct {
	client *anthropic.Client
}

func NewAPIAnalyzer(client *anthropic.Client) *APIAnalyzer {
	return &APIAnalyzer{client: client}
}

func (a *APIAnalyzer) AnalyzePR(ctx context.Context, pr github.PRData) (*AnalysisResult, error) {
	result, inputTokens, outputTokens, err := a.client.AnalyzePR(ctx, pr)
	if err != nil {
		return nil, err
	}
	return &AnalysisResult{
		Category:      result.Category,
		Confidence:    result.Confidence,
		Explanation:   result.Explanation,
		RelatedPRs:    result.RelatedPRs,
		Model:         a.client.Model(),
		PromptVersion: anthropic.PromptVersion,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
	}, nil
}

// APIBatchAnalyzer implements BatchAnalyzer using the Anthropic Batch API.
type APIBatchAnalyzer struct {
	client *anthropic.Client
	log    zerolog.Logger
}

func NewAPIBatchAnalyzer(client *anthropic.Client, log zerolog.Logger) *APIBatchAnalyzer {
	return &APIBatchAnalyzer{client: client, log: log}
}

func (b *APIBatchAnalyzer) CreateBatch(ctx context.Context, prs []github.PRData) (string, map[string]int, error) {
	return b.client.CreateBatch(ctx, prs)
}

func (b *APIBatchAnalyzer) PollBatch(ctx context.Context, batchID string) (string, int, int, int, int, error) {
	batch, err := b.client.PollBatch(ctx, batchID)
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	return string(batch.ProcessingStatus),
		int(batch.RequestCounts.Succeeded),
		int(batch.RequestCounts.Errored),
		int(batch.RequestCounts.Canceled),
		int(batch.RequestCounts.Expired),
		nil
}

func (b *APIBatchAnalyzer) CollectBatchResults(ctx context.Context, batchID string) (map[string]*AnalysisResult, error) {
	results, tokens, err := b.client.CollectBatchResults(ctx, batchID, b.log)
	if err != nil {
		return nil, fmt.Errorf("collect batch results: %w", err)
	}

	out := make(map[string]*AnalysisResult, len(results))
	for customID, r := range results {
		tok := tokens[customID]
		out[customID] = &AnalysisResult{
			Category:      r.Category,
			Confidence:    r.Confidence,
			Explanation:   r.Explanation,
			RelatedPRs:    r.RelatedPRs,
			Model:         b.client.Model(),
			PromptVersion: anthropic.PromptVersion,
			InputTokens:   tok[0],
			OutputTokens:  tok[1],
		}
	}
	return out, nil
}
