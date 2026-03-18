package analyzer

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
)

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

// UsageWindow contains utilization and reset time for a single rate-limit window.
type UsageWindow struct {
	Utilization float64
	ResetsAt    time.Time
}

// UsageStatus is returned by UsageChecker with current utilization across windows.
type UsageStatus struct {
	FiveHour UsageWindow
	SevenDay UsageWindow
}

// UsageChecker checks current API usage.
type UsageChecker interface {
	CheckUsage(ctx context.Context) (*UsageStatus, error)
}

// Notifier sends alerts.
type Notifier interface {
	Notify(ctx context.Context, title, message string)
}

// Orchestrator manages the analysis queue and a single worker that processes
// PRs sequentially, ensuring exclusive access to the shared git repository.
type Orchestrator struct {
	analyzer       *ClaudeCodeAnalyzer
	store          *store.Store
	log            zerolog.Logger
	usageChecker   UsageChecker
	usageThreshold float64
	notifier       Notifier

	// Analysis queue: keyed by PR number for dedup/upsert, ordered FIFO.
	mu     sync.Mutex
	items  map[int]github.PRData
	order  []int
	notify chan struct{}
}

// OrchestratorOption configures the Orchestrator.
type OrchestratorOption func(*Orchestrator)

// WithUsageChecker enables usage-based throttling. Analysis is paused
// when utilization exceeds threshold (0-100).
func WithUsageChecker(uc UsageChecker, threshold float64) OrchestratorOption {
	return func(o *Orchestrator) {
		o.usageChecker = uc
		o.usageThreshold = threshold
	}
}

// WithNotifier sets a notifier for usage alerts.
func WithNotifier(n Notifier) OrchestratorOption {
	return func(o *Orchestrator) {
		o.notifier = n
	}
}
