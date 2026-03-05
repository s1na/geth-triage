package analyzer

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/github"
	"github.com/sina-geth/geth-triage/internal/store"
)

// PRAnalyzer is the core interface for analyzing PRs.
// Implementations decide how to gather context and produce a verdict.
type PRAnalyzer interface {
	// AnalyzePR analyzes a single PR and returns the result.
	// The implementation is responsible for gathering whatever context it needs.
	AnalyzePR(ctx context.Context, pr github.PRData) (*AnalysisResult, error)
}

// AnalysisResult is the output of a single PR analysis.
type AnalysisResult struct {
	Category      string  `json:"category"`
	Confidence    float64 `json:"confidence"`
	Explanation   string  `json:"explanation"`
	RelatedPRs    []int   `json:"related_prs"`
	Model         string  `json:"model"`
	PromptVersion string  `json:"prompt_version"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
}

// Orchestrator manages analysis scheduling, batching, and persistence.
// It delegates the actual analysis to a PRAnalyzer implementation.
type Orchestrator struct {
	analyzer       PRAnalyzer
	store          *store.Store
	log            zerolog.Logger
	batchAnalyzer  BatchAnalyzer
	batchThreshold int
}

// BatchAnalyzer is an optional interface for analyzers that support async batch processing.
type BatchAnalyzer interface {
	// CreateBatch submits multiple PRs for async analysis.
	// Returns a batch ID and a map of custom_id -> PR number.
	CreateBatch(ctx context.Context, prs []github.PRData) (batchID string, customIDMap map[string]int, err error)

	// PollBatch checks batch status. Returns status and counts.
	PollBatch(ctx context.Context, batchID string) (status string, succeeded, errored, canceled, expired int, err error)

	// CollectBatchResults retrieves results from a completed batch.
	CollectBatchResults(ctx context.Context, batchID string) (map[string]*AnalysisResult, error)
}

// RepoManager is an optional interface for analyzers that need repo preparation before analysis cycles.
type RepoManager interface {
	EnsureRepo(ctx context.Context) error
}

// OrchestratorOption configures the Orchestrator.
type OrchestratorOption func(*Orchestrator)

// WithBatchAnalyzer enables batch processing support.
func WithBatchAnalyzer(ba BatchAnalyzer, threshold int) OrchestratorOption {
	return func(o *Orchestrator) {
		o.batchAnalyzer = ba
		o.batchThreshold = threshold
	}
}
